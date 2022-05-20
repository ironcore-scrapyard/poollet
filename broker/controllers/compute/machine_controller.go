// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package compute

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	commonv1alpha1 "github.com/onmetal/onmetal-api/apis/common/v1alpha1"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	networkingv1alpha1 "github.com/onmetal/onmetal-api/apis/networking/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	computehelper "github.com/onmetal/poollet/api/compute/helper"
	computefields "github.com/onmetal/poollet/api/compute/index/fields"
	"github.com/onmetal/poollet/broker"
	brokerclient "github.com/onmetal/poollet/broker/client"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	"github.com/onmetal/poollet/broker/provider"
	"github.com/onmetal/poollet/broker/sync"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type MachineReconciler struct {
	Provider provider.Provider

	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme

	TargetClient client.Client

	PoolName string

	TargetPoolLabels map[string]string
	TargetPoolName   string

	ClusterName string
	Domain      domain.Domain
}

func (r *MachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	machine := &computev1alpha1.Machine{}
	if err := r.Get(ctx, req.NamespacedName, machine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, machine)
}

func (r *MachineReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.ClusterName)
}

func (r *MachineReconciler) finalizer() string {
	return r.domain().Slash("machine")
}

func (r *MachineReconciler) reconcileExists(ctx context.Context, log logr.Logger, machine *computev1alpha1.Machine) (ctrl.Result, error) {
	if !computehelper.MachineRunsInMachinePool(machine, r.PoolName) {
		return ctrl.Result{}, nil
	}

	if !machine.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, machine)
	}

	return r.reconcile(ctx, log, machine)
}

func (r *MachineReconciler) delete(ctx context.Context, log logr.Logger, machine *computev1alpha1.Machine) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(machine, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, machine)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}
	log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Deleting target if exists")
	existed, err := clientutils.DeleteIfExists(ctx, r.TargetClient, &computev1alpha1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      machine.Name,
		},
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting target: %w", err)
	}

	if !existed {
		log.V(1).Info("Target already gone, removing finalizer")
		if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, machine, r.finalizer()); err != nil {
			return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
		}
		log.V(1).Info("Removed finalizer")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Target deletion issued")
	return ctrl.Result{Requeue: true}, nil
}

func (r *MachineReconciler) setTargetPoolRefIfSpecifiedAndUnset(targetMachine *computev1alpha1.Machine) {
	if r.TargetPoolName == "" || targetMachine.Spec.MachinePoolRef != nil {
		return
	}
	targetMachine.Spec.MachinePoolRef = &corev1.LocalObjectReference{
		Name: r.TargetPoolName,
	}
}

func (r *MachineReconciler) patchStatus(ctx context.Context, machine *computev1alpha1.Machine, state computev1alpha1.MachineState) error {
	base := machine.DeepCopy()
	machine.Status.State = state
	if err := r.Status().Patch(ctx, machine, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching status: %w", err)
	}
	return nil
}

func (r *MachineReconciler) reconcile(ctx context.Context, log logr.Logger, machine *computev1alpha1.Machine) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, machine)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}
	log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, machine, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Applying target")
	targetMachine, err := r.applyTarget(ctx, machine, targetNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	if targetMachine == nil {
		log.V(1).Info("Dependencies of target are not yet ready")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Patching status")
	if err := r.patchStatus(ctx, machine, targetMachine.Status.State); err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Successfully reconciled machine")
	return ctrl.Result{}, nil
}

func (r *MachineReconciler) registerImagePullSecretMutation(ctx context.Context, machine, target *computev1alpha1.Machine, b *sync.CompositeMutationBuilder) error {
	imagePullSecretRef := machine.Spec.ImagePullSecretRef
	if imagePullSecretRef == nil {
		b.Add(func() {
			target.Spec.ImagePullSecretRef = nil
		})
		return nil
	}

	imagePullSecretKey := client.ObjectKey{Namespace: machine.Namespace, Name: imagePullSecretRef.Name}
	targetImagePullSecret := &corev1.Secret{}
	if err := r.Provider.Target(ctx, imagePullSecretKey, targetImagePullSecret); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting image pull secret %s target: %w", imagePullSecretKey, err)
		}
		b.PartialSync = true
		return nil
	}

	b.Add(func() {
		target.Spec.ImagePullSecretRef = &corev1.LocalObjectReference{Name: targetImagePullSecret.Name}
	})
	return nil
}

func (r *MachineReconciler) registerIgnitionMutation(ctx context.Context, machine, target *computev1alpha1.Machine, b *sync.CompositeMutationBuilder) error {
	ignitionRef := machine.Spec.IgnitionRef
	if ignitionRef == nil {
		b.Add(func() {
			target.Spec.IgnitionRef = nil
		})
		return nil
	}

	ignitionKey := client.ObjectKey{Namespace: machine.Namespace, Name: ignitionRef.Name}
	targetIgnition := &corev1.ConfigMap{}
	if err := r.Provider.Target(ctx, ignitionKey, targetIgnition); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting ignition %s target: %w", ignitionKey, err)
		}
		b.PartialSync = true
		return nil
	}

	b.Add(func() {
		target.Spec.IgnitionRef = &commonv1alpha1.ConfigMapKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: targetIgnition.Name},
			Key:                  ignitionRef.Key,
		}
	})
	return nil
}

func (r *MachineReconciler) targetVolumeSource(ctx context.Context, machine *computev1alpha1.Machine, src *computev1alpha1.VolumeSource) (*computev1alpha1.VolumeSource, bool, error) {
	switch {
	case src.EmptyDisk != nil:
		return &computev1alpha1.VolumeSource{
			EmptyDisk: src.EmptyDisk.DeepCopy(),
		}, true, nil
	case src.VolumeClaimRef != nil:
		volumeClaim := &storagev1alpha1.VolumeClaim{}
		volumeClaimKey := client.ObjectKey{Namespace: machine.Namespace, Name: src.VolumeClaimRef.Name}
		if err := r.Get(ctx, volumeClaimKey, volumeClaim); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, false, fmt.Errorf("error getting volume claim %s: %w", src.VolumeClaimRef.Name, err)
			}
			return nil, false, nil
		}

		if volumeClaim.Status.Phase != storagev1alpha1.VolumeClaimBound || volumeClaim.Spec.VolumeRef == nil {
			return nil, false, nil
		}

		targetVolumeClaim := &storagev1alpha1.VolumeClaim{}
		if err := r.Provider.Target(ctx, volumeClaimKey, targetVolumeClaim); err != nil {
			if !brokererrors.IsNotSyncedOrNotFound(err) {
				return nil, false, fmt.Errorf("error getting volume claim %s target: %w", volumeClaimKey, err)
			}
			return nil, false, nil
		}

		return &computev1alpha1.VolumeSource{
			VolumeClaimRef: &corev1.LocalObjectReference{
				Name: targetVolumeClaim.Name,
			},
		}, true, nil
	default:
		return nil, false, fmt.Errorf("unrecognized volume source %#v", src)
	}
}

func (r *MachineReconciler) registerVolumesMutation(ctx context.Context, machine, target *computev1alpha1.Machine, b *sync.CompositeMutationBuilder) error {
	var (
		// unhandledNames serves for tracking volumes that we could not sync yet.
		// Reasons are if e.g. the corresponding claim / hasn't been synced.
		// In these cases, we want to keep any value that is there.
		unhandledNames = sets.NewString()
		targetVolumes  []computev1alpha1.Volume
	)

	for _, volume := range machine.Spec.Volumes {
		src, ok, err := r.targetVolumeSource(ctx, machine, &volume.VolumeSource)
		if err != nil {
			return fmt.Errorf("[volume %s] %w", volume.Name, err)
		}

		if !ok {
			b.PartialSync = true
			unhandledNames.Insert(volume.Name)
		}
		if src != nil {
			targetVolumes = append(targetVolumes, computev1alpha1.Volume{Name: volume.Name, VolumeSource: *src})
		}
	}

	b.Add(func() {
		var unhandledVolumes []computev1alpha1.Volume
		for _, volume := range target.Spec.Volumes {
			if unhandledNames.Has(volume.Name) {
				unhandledVolumes = append(unhandledVolumes, volume)
			}
		}
		target.Spec.Volumes = append(unhandledVolumes, targetVolumes...)
	})
	return nil
}

func (r *MachineReconciler) targetNetworkInterfaceSource(ctx context.Context, machine, target *computev1alpha1.Machine, nicName string, src *computev1alpha1.NetworkInterfaceSource) (*computev1alpha1.NetworkInterfaceSource, bool, error) {
	switch {
	case src.NetworkInterfaceRef != nil || src.Ephemeral != nil:
		var actualNicName string
		if src.NetworkInterfaceRef != nil {
			actualNicName = src.NetworkInterfaceRef.Name
		} else {
			actualNicName = fmt.Sprintf("%s-%s", machine.Name, nicName)
		}

		nic := &networkingv1alpha1.NetworkInterface{}
		nicKey := client.ObjectKey{Namespace: machine.Namespace, Name: actualNicName}
		if err := r.Get(ctx, nicKey, nic); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, false, fmt.Errorf("error getting nic %s: %w", nicKey, err)
			}
			return nil, false, nil
		}

		if nic.Status.Phase != networkingv1alpha1.NetworkInterfacePhaseBound ||
			nic.Spec.MachineRef == nil ||
			nic.Spec.MachineRef.Name != machine.Name {
			return nil, false, nil
		}

		targetNic := &networkingv1alpha1.NetworkInterface{}
		if err := r.Provider.Target(ctx, client.ObjectKeyFromObject(nic), targetNic); err != nil {
			if !brokererrors.IsNotSyncedOrNotFound(err) {
				return nil, false, fmt.Errorf("error getting network interface %s target: %w", nicKey, err)
			}
			return nil, false, nil
		}
		return &computev1alpha1.NetworkInterfaceSource{
			NetworkInterfaceRef: &corev1.LocalObjectReference{
				Name: targetNic.Name,
			},
		}, true, nil
	default:
		return nil, false, fmt.Errorf("unrecognized network interface source %#v", src)
	}
}

func (r *MachineReconciler) registerNetworkInterfacesMutation(ctx context.Context, machine, target *computev1alpha1.Machine, b *sync.CompositeMutationBuilder) error {
	var (
		// unhandledNames serves for tracking volumes that we could not sync yet.
		// Reasons are if e.g. the corresponding nic hasn't been synced.
		// In these cases, we want to keep any value that is there.
		unhandledNames = sets.NewString()
		targetNics     []computev1alpha1.NetworkInterface
	)
	for _, nic := range machine.Spec.NetworkInterfaces {
		src, ok, err := r.targetNetworkInterfaceSource(ctx, machine, target, nic.Name, &nic.NetworkInterfaceSource)
		if err != nil {
			return fmt.Errorf("[network interface %s] %w", nic.Name, err)
		}

		if !ok {
			b.PartialSync = true
			unhandledNames.Insert(nic.Name)
		}
		if src != nil {
			targetNics = append(targetNics, computev1alpha1.NetworkInterface{Name: nic.Name, NetworkInterfaceSource: *src})
		}
	}

	b.Add(func() {
		var unhandled []computev1alpha1.NetworkInterface
		for _, nic := range target.Spec.NetworkInterfaces {
			if unhandledNames.Has(nic.Name) {
				unhandled = append(unhandled, nic)
			}
		}
		target.Spec.NetworkInterfaces = append(unhandled, targetNics...)
	})
	return nil
}

func (r *MachineReconciler) registerDefaultMutation(ctx context.Context, machine, target *computev1alpha1.Machine, b *sync.CompositeMutationBuilder) error {
	b.Add(func() {
		target.Spec.MachineClassRef = machine.Spec.MachineClassRef
		target.Spec.MachinePoolSelector = r.TargetPoolLabels
		if r.TargetPoolName != "" && target.Spec.MachinePoolRef == nil {
			target.Spec.MachinePoolRef = &corev1.LocalObjectReference{Name: r.TargetPoolName}
		}
		r.setTargetPoolRefIfSpecifiedAndUnset(target)
		target.Spec.Image = machine.Spec.Image
		target.Spec.EFIVars = machine.Spec.EFIVars
	})
	return nil
}

func (r *MachineReconciler) applyTarget(ctx context.Context, machine *computev1alpha1.Machine, targetNamespace string) (*computev1alpha1.Machine, error) {
	var (
		target = &computev1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace,
				Name:      machine.Name,
			},
		}
		b sync.CompositeMutationBuilder
	)

	if err := r.registerImagePullSecretMutation(ctx, machine, target, &b); err != nil {
		return nil, err
	}
	if err := r.registerIgnitionMutation(ctx, machine, target, &b); err != nil {
		return nil, err
	}
	if err := r.registerVolumesMutation(ctx, machine, target, &b); err != nil {
		return nil, err
	}
	if err := r.registerNetworkInterfacesMutation(ctx, machine, target, &b); err != nil {
		return nil, err
	}
	if err := r.registerDefaultMutation(ctx, machine, target, &b); err != nil {
		return nil, err
	}

	if _, err := brokerclient.BrokerControlledCreateOrPatch(ctx, r.TargetClient, r.ClusterName, machine, target,
		b.Mutate(target),
	); err != nil {
		return nil, sync.IgnorePartialCreate(err)
	}
	return target, nil
}

func (r *MachineReconciler) Target(ctx context.Context, key client.ObjectKey, targetObj client.Object) error {
	targetMachine := targetObj.(*computev1alpha1.Machine)

	machine := &computev1alpha1.Machine{}
	if err := r.Get(ctx, key, machine); err != nil {
		return err
	}

	if !computehelper.MachineRunsInMachinePool(machine, r.PoolName) {
		return fmt.Errorf("machine %s is not running in machine pool %s", key, r.PoolName)
	}

	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, machine)
	if err != nil {
		return err
	}

	targetKey := client.ObjectKey{Namespace: targetNamespace, Name: machine.Name}
	if err := r.TargetClient.Get(ctx, targetKey, targetMachine); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return brokererrors.NewNotSynced(computev1alpha1.Resource("machines"), machine.Name)
	}
	return nil
}

func (r *MachineReconciler) SetupWithManager(mgr broker.Manager) error {
	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		For(&computev1alpha1.Machine{}).
		OwnsTarget(&computev1alpha1.Machine{}).
		WatchTargetNamespaceCreated().
		ReferencesViaField(&corev1.Secret{}, computefields.MachineSpecSecretNames).
		ReferencesViaField(&corev1.ConfigMap{}, computefields.MachineSpecConfigMapNames).
		ReferencesViaField(&storagev1alpha1.VolumeClaim{}, computefields.MachineSpecVolumeClaimNames).
		ReferencesViaField(&networkingv1alpha1.NetworkInterface{}, computefields.MachineSpecNetworkInterfaceNames).
		ReferencesTargetViaField(&corev1.Secret{}, computefields.MachineSpecSecretNames).
		ReferencesTargetViaField(&corev1.ConfigMap{}, computefields.MachineSpecConfigMapNames).
		ReferencesTargetViaField(&storagev1alpha1.VolumeClaim{}, computefields.MachineSpecVolumeClaimNames).
		ReferencesTargetViaField(&networkingv1alpha1.NetworkInterface{}, computefields.MachineSpecNetworkInterfaceNames).
		Complete(r)
}
