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

package networking

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	commonv1alpha1 "github.com/onmetal/onmetal-api/apis/common/v1alpha1"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	networkingv1alpha1 "github.com/onmetal/onmetal-api/apis/networking/v1alpha1"
	computepredicate "github.com/onmetal/poollet/api/compute/predicate"
	networkingctrl "github.com/onmetal/poollet/api/networking/controller"
	"github.com/onmetal/poollet/api/networking/helper"
	networkingfields "github.com/onmetal/poollet/api/networking/index/fields"
	"github.com/onmetal/poollet/broker"
	"github.com/onmetal/poollet/broker/builder"
	brokerclient "github.com/onmetal/poollet/broker/client"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	"github.com/onmetal/poollet/broker/provider"
	"github.com/onmetal/poollet/broker/sync"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type NetworkInterfaceReconciler struct {
	Provider provider.Provider

	client.Client
	APIReader    client.Reader
	TargetClient client.Client

	ClusterName     string
	MachinePoolName string
	Domain          domain.Domain
}

func (r *NetworkInterfaceReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.ClusterName)
}

func (r *NetworkInterfaceReconciler) finalizer() string {
	return r.domain().Slash("networkinterface")
}

func (r *NetworkInterfaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	nic := &networkingv1alpha1.NetworkInterface{}
	if err := r.Get(ctx, req.NamespacedName, nic); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, nic)
}

func (r *NetworkInterfaceReconciler) reconcileExists(ctx context.Context, log logr.Logger, nic *networkingv1alpha1.NetworkInterface) (ctrl.Result, error) {
	if !nic.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, nic)
	}
	ok, err := networkingctrl.IsNetworkInterfaceUsedCachedOrLive(ctx, r.APIReader, r.Client, nic, r.MachinePoolName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return r.delete(ctx, log, nic)
	}
	return r.reconcile(ctx, log, nic)
}

func (r *NetworkInterfaceReconciler) delete(ctx context.Context, log logr.Logger, nic *networkingv1alpha1.NetworkInterface) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(nic, r.finalizer()) {
		log.V(2).Info("No finalizer present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, nic)
	if err != nil {
		return ctrl.Result{}, err
	}

	log = log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Deleting target if exists")
	existed, err := clientutils.DeleteIfExists(ctx, r.TargetClient, &networkingv1alpha1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      nic.Name,
		},
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting target: %w", err)
	}
	if existed {
		log.V(1).Info("Issued target deletion")
		return ctrl.Result{Requeue: true}, nil
	}
	log.V(1).Info("Target already gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, nic, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Removed finalizer")
	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceReconciler) registerNetworkMutation(ctx context.Context, nic, target *networkingv1alpha1.NetworkInterface, b *sync.CompositeMutationBuilder) error {
	networkKey := client.ObjectKey{Namespace: nic.Namespace, Name: nic.Spec.NetworkRef.Name}
	targetNetwork := &networkingv1alpha1.Network{}
	if err := r.Provider.Target(ctx, networkKey, targetNetwork); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting network %s target key: %w", networkKey, err)
		}
		b.PartialSync = true
		return nil
	}

	b.Add(func() {
		target.Spec.NetworkRef.Name = targetNetwork.Name
	})
	return nil
}

func (r *NetworkInterfaceReconciler) registerMachineMutation(ctx context.Context, nic, target *networkingv1alpha1.NetworkInterface, b *sync.CompositeMutationBuilder) error {
	if nic.Spec.MachineRef == nil {
		b.Add(func() {
			target.Spec.MachineRef = nil
		})
		return nil
	}

	machineKey := client.ObjectKey{Namespace: nic.Namespace, Name: nic.Spec.MachineRef.Name}
	targetMachine := &computev1alpha1.Machine{}
	if err := r.Provider.Target(ctx, machineKey, targetMachine); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting machine %s target key: %w", machineKey, err)
		}
		// Since we don't depend on a machine to be synced yet, we just set it to empty.
		b.Add(func() {
			target.Spec.MachineRef = nil
		})
		return nil
	}

	b.Add(func() {
		target.Spec.MachineRef = &commonv1alpha1.LocalUIDReference{
			Name: targetMachine.Name,
			UID:  targetMachine.UID,
		}
	})
	return nil
}

func (r *NetworkInterfaceReconciler) registerVirtualIPMutation(ctx context.Context, nic, target *networkingv1alpha1.NetworkInterface, b *sync.CompositeMutationBuilder) error {
	if nic.Spec.VirtualIP == nil {
		b.Add(func() {
			target.Spec.VirtualIP = nil
		})
		return nil
	}

	virtualIPKey := client.ObjectKey{Namespace: nic.Namespace, Name: helper.NetworkInterfaceVirtualIPName(nic)}
	targetVirtualIP := &networkingv1alpha1.VirtualIP{}
	if err := r.Provider.Target(ctx, virtualIPKey, targetVirtualIP); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting virtual ip %s target key: %w", virtualIPKey, err)
		}
		b.PartialSync = true
		return nil
	}

	b.Add(func() {
		target.Spec.VirtualIP = &networkingv1alpha1.VirtualIPSource{
			VirtualIPRef: &corev1.LocalObjectReference{Name: targetVirtualIP.Name},
		}
	})
	return nil
}

func (r *NetworkInterfaceReconciler) registerDefaultMutation(ctx context.Context, nic, target *networkingv1alpha1.NetworkInterface, b *sync.CompositeMutationBuilder) error {
	if len(nic.Status.IPs) != len(nic.Spec.IPFamilies) {
		b.PartialSync = true
		return nil
	}

	var (
		ipFamilies = make([]corev1.IPFamily, 0, len(nic.Status.IPs))
		ips        = make([]networkingv1alpha1.IPSource, 0, len(nic.Status.IPs))
	)
	for _, ip := range nic.Status.IPs {
		ip := ip
		ipFamilies = append(ipFamilies, ip.Family())
		ips = append(ips, networkingv1alpha1.IPSource{Value: &ip})
	}

	b.Add(func() {
		target.Spec.IPFamilies = ipFamilies
		target.Spec.IPs = ips
	})
	return nil
}

func (r *NetworkInterfaceReconciler) reconcile(ctx context.Context, log logr.Logger, nic *networkingv1alpha1.NetworkInterface) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Determining target namespace")
	namespaceKey := client.ObjectKey{Name: nic.Namespace}
	targetNamespace := &corev1.Namespace{}
	if err := r.Provider.Target(ctx, namespaceKey, targetNamespace); err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}

	log = log.WithValues("TargetNamespace", targetNamespace.Name)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, nic, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	target, partial, err := r.applyTarget(ctx, log, nic, targetNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		log.V(1).Info("Target dependencies are not ready", "Partial", partial)
		return ctrl.Result{Requeue: partial}, nil
	}

	log.V(1).Info("Applied target", "Partial", partial)
	return ctrl.Result{Requeue: partial}, nil
}

func (r *NetworkInterfaceReconciler) applyTarget(ctx context.Context, log logr.Logger, nic *networkingv1alpha1.NetworkInterface, targetNamespace *corev1.Namespace) (*networkingv1alpha1.NetworkInterface, bool, error) {
	var (
		target = &networkingv1alpha1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace.Name,
				Name:      nic.Name,
			},
		}
		b sync.CompositeMutationBuilder
	)

	if err := r.registerDefaultMutation(ctx, nic, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerNetworkMutation(ctx, nic, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerMachineMutation(ctx, nic, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerVirtualIPMutation(ctx, nic, target, &b); err != nil {
		return nil, false, err
	}

	log.V(1).Info("Applying target")
	if _, err := brokerclient.BrokerControlledCreateOrPatch(ctx, r.TargetClient, r.ClusterName, nic, target,
		b.Mutate(target),
	); err != nil {
		return nil, b.PartialSync, sync.IgnorePartialCreate(err)
	}
	return target, b.PartialSync, nil
}

func (r *NetworkInterfaceReconciler) Target(ctx context.Context, key client.ObjectKey, targetObj client.Object) error {
	targetNic := targetObj.(*networkingv1alpha1.NetworkInterface)

	nic := &networkingv1alpha1.NetworkInterface{}
	if err := r.Get(ctx, key, nic); err != nil {
		return err
	}

	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, nic)
	if err != nil {
		return err
	}

	if err := r.TargetClient.Get(ctx, client.ObjectKey{Namespace: targetNamespace, Name: nic.Name}, targetNic); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return brokererrors.NewNotSynced(networkingv1alpha1.Resource("networkinterfaces"), nic.Name)
	}
	return nil
}

func (r *NetworkInterfaceReconciler) SetupWithManager(mgr broker.Manager) error {
	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		WatchTargetNamespaceCreated().
		FilterNoTargetNamespace().
		For(&networkingv1alpha1.NetworkInterface{}).
		OwnsTarget(&networkingv1alpha1.NetworkInterface{}).
		ReferencesViaField(
			&computev1alpha1.Machine{},
			networkingfields.NetworkInterfaceMachineNames,
			builder.WithPredicates(computepredicate.MachineRunsInMachinePoolPredicate(r.MachinePoolName)),
		).
		Complete(r)
}
