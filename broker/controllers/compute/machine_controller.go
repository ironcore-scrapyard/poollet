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
	"github.com/onmetal/onmetal-api/controllers/shared"
	computehelper "github.com/onmetal/poollet/api/compute/helper"
	computefields "github.com/onmetal/poollet/api/compute/index/fields"
	"github.com/onmetal/poollet/broker"
	brokerclient "github.com/onmetal/poollet/broker/client"
	"github.com/onmetal/poollet/broker/controllers/compute/events"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	"github.com/onmetal/poollet/broker/provider"
	"github.com/onmetal/poollet/broker/sync"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type MachineReconciler struct {
	record.EventRecorder
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
	return r.Domain.Subdomain(r.PoolName)
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
	target, partial, err := r.applyTarget(ctx, machine, targetNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		log.V(1).Info("Target dependencies are not ready", "Partial", partial)
		return ctrl.Result{Requeue: partial}, nil
	}
	log.V(1).Info("Patching status")
	if err := r.patchStatus(ctx, machine, target.Status.State); err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Successfully reconciled machine", "Partial", partial)
	return ctrl.Result{Requeue: partial}, nil
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
		if apierrors.IsNotFound(err) {
			r.Eventf(machine, corev1.EventTypeWarning, "ImagePullSecretNotFound", "Image pull secret %s not found", imagePullSecretKey.Name)
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
	targetIgnition := &corev1.Secret{}
	if err := r.Provider.Target(ctx, ignitionKey, targetIgnition); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting ignition %s target: %w", ignitionKey, err)
		}
		if apierrors.IsNotFound(err) {
			r.Eventf(machine, corev1.EventTypeWarning, events.FailedApplyingIgnition, "Ignition %s not found", ignitionKey.Name)
		}
		b.PartialSync = true
		return nil
	}

	b.Add(func() {
		target.Spec.IgnitionRef = &commonv1alpha1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: targetIgnition.Name},
			Key:                  ignitionRef.Key,
		}
	})
	return nil
}

func (r *MachineReconciler) findMachineVolumeStatus(machine *computev1alpha1.Machine, machineVolumeName string) (computev1alpha1.VolumeStatus, bool) {
	for _, status := range machine.Status.Volumes {
		if status.Name == machineVolumeName {
			return status, true
		}
	}
	return computev1alpha1.VolumeStatus{}, false
}

func (r *MachineReconciler) machineVolumeName(machine *computev1alpha1.Machine, machineVolume *computev1alpha1.Volume) string {
	if machineVolume.VolumeRef != nil {
		return machineVolume.VolumeRef.Name
	}

	return shared.MachineEphemeralVolumeName(machine.Name, machineVolume.Name)
}

func (r *MachineReconciler) targetVolumeSource(ctx context.Context, machine *computev1alpha1.Machine, machineVolume *computev1alpha1.Volume) (src *computev1alpha1.VolumeSource, ok bool, warning string, err error) {
	switch {
	case machineVolume.EmptyDisk != nil:
		return &computev1alpha1.VolumeSource{
			EmptyDisk: machineVolume.EmptyDisk.DeepCopy(),
		}, true, "", nil
	case machineVolume.VolumeRef != nil || machineVolume.Ephemeral != nil:
		status, ok := r.findMachineVolumeStatus(machine, machineVolume.Name)
		if !ok || status.Phase != computev1alpha1.VolumePhaseBound {
			return nil, false, "", nil
		}

		volumeKey := client.ObjectKey{Namespace: machine.Namespace, Name: r.machineVolumeName(machine, machineVolume)}
		targetVolume := &storagev1alpha1.Volume{}
		if err := r.Provider.Target(ctx, volumeKey, targetVolume); err != nil {
			if !brokererrors.IsNotSyncedOrNotFound(err) {
				return nil, false, "", fmt.Errorf("error getting volume %s target: %w", volumeKey, err)
			}
			if apierrors.IsNotFound(err) {
				return nil, false, fmt.Sprintf("volume %s not found", volumeKey.Name), nil
			}
			return nil, false, "", nil
		}

		return &computev1alpha1.VolumeSource{
			VolumeRef: &corev1.LocalObjectReference{
				Name: targetVolume.Name,
			},
		}, true, "", nil
	default:
		return nil, false, "", fmt.Errorf("unrecognized volume %#v", machineVolume)
	}
}

func (r *MachineReconciler) registerVolumesMutation(ctx context.Context, machine, target *computev1alpha1.Machine, b *sync.CompositeMutationBuilder) error {
	var (
		// unhandledNames serves for tracking volumes that we could not sync yet.
		// Reasons are if e.g. the corresponding claim / hasn't been synced.
		// In these cases, we want to keep any value that is there.
		unhandledNames = sets.NewString()
		warnings       []string
		targetVolumes  []computev1alpha1.Volume
	)

	for _, machineVolume := range machine.Spec.Volumes {
		src, ok, warning, err := r.targetVolumeSource(ctx, machine, &machineVolume)
		if err != nil {
			return fmt.Errorf("[volume %s] %w", machineVolume.Name, err)
		}
		if !ok {
			b.PartialSync = true
			unhandledNames.Insert(machineVolume.Name)
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
		if src != nil {
			targetVolumes = append(targetVolumes, computev1alpha1.Volume{
				Name:         machineVolume.Name,
				Device:       machineVolume.Device,
				VolumeSource: *src,
			})
		}
	}

	if len(warnings) > 0 {
		r.Eventf(machine, corev1.EventTypeWarning, events.FailedApplyingVolumes, "Failed applying volume(s): %v", warnings)
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

func (r *MachineReconciler) findMachineNetworkInterfaceStatus(machine *computev1alpha1.Machine, machineNicName string) (computev1alpha1.NetworkInterfaceStatus, bool) {
	for _, status := range machine.Status.NetworkInterfaces {
		if status.Name == machineNicName {
			return status, true
		}
	}
	return computev1alpha1.NetworkInterfaceStatus{}, false
}

func (r *MachineReconciler) machineNetworkInterfaceName(machine *computev1alpha1.Machine, machineNic *computev1alpha1.NetworkInterface) string {
	if machineNic.NetworkInterfaceRef != nil {
		return machineNic.NetworkInterfaceRef.Name
	}

	return shared.MachineEphemeralNetworkInterfaceName(machine.Name, machineNic.Name)
}

func (r *MachineReconciler) targetNetworkInterfaceSource(ctx context.Context, machine, target *computev1alpha1.Machine, machineNic *computev1alpha1.NetworkInterface) (src *computev1alpha1.NetworkInterfaceSource, ok bool, warning string, err error) {
	switch {
	case machineNic.NetworkInterfaceRef != nil || machineNic.Ephemeral != nil:
		status, ok := r.findMachineNetworkInterfaceStatus(machine, machineNic.Name)
		if !ok || status.Phase != computev1alpha1.NetworkInterfacePhaseBound {
			return nil, false, "", nil
		}

		nicKey := client.ObjectKey{Namespace: machine.Namespace, Name: r.machineNetworkInterfaceName(machine, machineNic)}
		targetNic := &networkingv1alpha1.NetworkInterface{}
		if err := r.Provider.Target(ctx, nicKey, targetNic); err != nil {
			if !brokererrors.IsNotSyncedOrNotFound(err) {
				return nil, false, "", fmt.Errorf("error getting network interface %s target: %w", nicKey, err)
			}
			if apierrors.IsNotFound(err) {
				return nil, false, fmt.Sprintf("network interface %s not found", nicKey.Name), nil
			}
			return nil, false, "", nil
		}
		return &computev1alpha1.NetworkInterfaceSource{
			NetworkInterfaceRef: &corev1.LocalObjectReference{
				Name: targetNic.Name,
			},
		}, true, "", nil
	default:
		return nil, false, "", fmt.Errorf("unrecognized network interface %#v", machineNic)
	}
}

func (r *MachineReconciler) registerNetworkInterfacesMutation(ctx context.Context, machine, target *computev1alpha1.Machine, b *sync.CompositeMutationBuilder) error {
	var (
		// unhandledNames serves for tracking volumes that we could not sync yet.
		// Reasons are if e.g. the corresponding nic hasn't been synced.
		// In these cases, we want to keep any value that is there.
		unhandledNames = sets.NewString()
		targetNics     []computev1alpha1.NetworkInterface
		warnings       []string
	)
	for _, machineNic := range machine.Spec.NetworkInterfaces {
		src, ok, warning, err := r.targetNetworkInterfaceSource(ctx, machine, target, &machineNic)
		if err != nil {
			return fmt.Errorf("[network interface %s] %w", machineNic.Name, err)
		}
		if !ok {
			b.PartialSync = true
			unhandledNames.Insert(machineNic.Name)
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}

		if src != nil {
			targetNics = append(targetNics, computev1alpha1.NetworkInterface{Name: machineNic.Name, NetworkInterfaceSource: *src})
		}
	}

	if len(warnings) > 0 {
		r.Eventf(machine, corev1.EventTypeWarning, events.FailedApplyingNetworkInterfaces, "Failed applying network interface(s): %v", warnings)
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

func (r *MachineReconciler) applyTarget(ctx context.Context, machine *computev1alpha1.Machine, targetNamespace string) (*computev1alpha1.Machine, bool, error) {
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
		return nil, false, err
	}
	if err := r.registerIgnitionMutation(ctx, machine, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerVolumesMutation(ctx, machine, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerNetworkInterfacesMutation(ctx, machine, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerDefaultMutation(ctx, machine, target, &b); err != nil {
		return nil, false, err
	}

	if _, err := brokerclient.BrokerControlledCreateOrPatch(ctx, r.TargetClient, r.ClusterName, machine, target,
		b.Mutate(target),
	); err != nil {
		return nil, b.PartialSync, sync.IgnorePartialCreate(err)
	}
	return target, b.PartialSync, nil
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
		WatchTargetNamespaceCreated().
		For(&computev1alpha1.Machine{}).
		OwnsTarget(&computev1alpha1.Machine{}).
		ReferencesViaField(&corev1.Secret{}, computefields.MachineSpecSecretNames).
		ReferencesViaField(&corev1.ConfigMap{}, computefields.MachineSpecConfigMapNames).
		ReferencesViaField(&networkingv1alpha1.NetworkInterface{}, computefields.MachineSpecNetworkInterfaceNames).
		ReferencesTargetViaField(&corev1.Secret{}, computefields.MachineSpecSecretNames).
		ReferencesTargetViaField(&corev1.ConfigMap{}, computefields.MachineSpecConfigMapNames).
		ReferencesTargetViaField(&networkingv1alpha1.NetworkInterface{}, computefields.MachineSpecNetworkInterfaceNames).
		Complete(r)
}
