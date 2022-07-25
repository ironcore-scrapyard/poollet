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
	computehelper "github.com/onmetal/poollet/api/compute/helper"
	networkingctrl "github.com/onmetal/poollet/api/networking/controller"
	"github.com/onmetal/poollet/broker"
	brokerclient "github.com/onmetal/poollet/broker/client"
	"github.com/onmetal/poollet/broker/controllers/networking/events"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	brokermeta "github.com/onmetal/poollet/broker/meta"
	"github.com/onmetal/poollet/broker/provider"
	"github.com/onmetal/poollet/broker/sync"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type AliasPrefixReconciler struct {
	record.EventRecorder
	Provider provider.Provider

	client.Client
	APIReader    client.Reader
	TargetClient client.Client
	Scheme       *runtime.Scheme

	ClusterName     string
	MachinePoolName string
	Domain          domain.Domain
}

func (r *AliasPrefixReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.MachinePoolName)
}

func (r *AliasPrefixReconciler) finalizer() string {
	return r.domain().Slash("aliasprefix")
}

func (r *AliasPrefixReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	aliasPrefix := &networkingv1alpha1.AliasPrefix{}
	if err := r.Get(ctx, req.NamespacedName, aliasPrefix); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, aliasPrefix)
}

func (r *AliasPrefixReconciler) reconcileExists(ctx context.Context, log logr.Logger, aliasPrefix *networkingv1alpha1.AliasPrefix) (ctrl.Result, error) {
	if !aliasPrefix.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, aliasPrefix)
	}
	ok, err := networkingctrl.IsAliasPrefixUsedCachedOrLive(ctx, r.APIReader, r.Client, aliasPrefix, r.MachinePoolName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return r.delete(ctx, log, aliasPrefix)
	}
	return r.reconcile(ctx, log, aliasPrefix)
}

func (r *AliasPrefixReconciler) delete(ctx context.Context, log logr.Logger, aliasPrefix *networkingv1alpha1.AliasPrefix) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(aliasPrefix, r.finalizer()) {
		log.V(2).Info("No finalizer present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, aliasPrefix)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}

	log = log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Deleting target if exists")
	existed, err := clientutils.DeleteIfExists(ctx, r.TargetClient, &networkingv1alpha1.AliasPrefix{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      aliasPrefix.Name,
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
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, aliasPrefix, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Removed finalizer")
	return ctrl.Result{}, nil
}

func (r *AliasPrefixReconciler) registerDefaultMutation(ctx context.Context, aliasPrefix, target *networkingv1alpha1.AliasPrefix, b *sync.CompositeMutationBuilder) error {
	prefix := aliasPrefix.Status.Prefix
	if prefix == nil {
		b.PartialSync = true
		return nil
	}

	b.Add(func() {
		target.Spec.Prefix = networkingv1alpha1.PrefixSource{
			Value: prefix,
		}
		target.Spec.NetworkInterfaceSelector = nil
	})
	return nil
}

func (r *AliasPrefixReconciler) registerNetworkRefMutation(ctx context.Context, aliasPrefix, target *networkingv1alpha1.AliasPrefix, b *sync.CompositeMutationBuilder) error {
	networkKey := client.ObjectKey{Namespace: aliasPrefix.Namespace, Name: aliasPrefix.Spec.NetworkRef.Name}
	targetNetwork := &networkingv1alpha1.Network{}
	if err := r.Provider.Target(ctx, networkKey, targetNetwork); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting network %s target: %w", networkKey, err)
		}
		if apierrors.IsNotFound(err) {
			r.Eventf(aliasPrefix, corev1.EventTypeWarning, events.FailedApplyingAliasPrefix, "Network %s not found", networkKey.Name)
		}

		b.PartialSync = true
		return nil
	}

	b.Add(func() {
		target.Spec.NetworkRef = corev1.LocalObjectReference{Name: targetNetwork.Name}
	})
	return nil
}

func (r *AliasPrefixReconciler) reconcile(ctx context.Context, log logr.Logger, aliasPrefix *networkingv1alpha1.AliasPrefix) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, aliasPrefix)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}

	log = log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, aliasPrefix, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Applying target")
	target, partial, err := r.applyTarget(ctx, log, aliasPrefix, targetNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		log.V(1).Info("Target dependencies are not ready", "Partial", partial)
		return ctrl.Result{Requeue: partial}, nil
	}

	log.V(1).Info("Applying target routing")
	if err := r.applyTargetRouting(ctx, log, aliasPrefix, target); err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Reconciled", "Partial", partial)
	return ctrl.Result{Requeue: partial}, nil
}

func (r *AliasPrefixReconciler) applyTarget(ctx context.Context, log logr.Logger, aliasPrefix *networkingv1alpha1.AliasPrefix, targetNamespace string) (*networkingv1alpha1.AliasPrefix, bool, error) {
	var (
		target = &networkingv1alpha1.AliasPrefix{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace,
				Name:      aliasPrefix.Name,
			},
		}
		b sync.CompositeMutationBuilder
	)

	if err := r.registerDefaultMutation(ctx, aliasPrefix, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerNetworkRefMutation(ctx, aliasPrefix, target, &b); err != nil {
		return nil, false, err
	}

	log.V(1).Info("Applying target")
	if _, err := brokerclient.BrokerControlledCreateOrPatch(ctx, r.TargetClient, r.ClusterName, aliasPrefix, target,
		b.Mutate(target),
	); err != nil {
		return nil, b.PartialSync, sync.IgnorePartialCreate(err)
	}

	log.V(1).Info("Applied target")
	return aliasPrefix, b.PartialSync, nil
}

func (r *AliasPrefixReconciler) registerAliasPrefixRoutingMutation(ctx context.Context, log logr.Logger, aliasPrefix *networkingv1alpha1.AliasPrefix, target *networkingv1alpha1.AliasPrefixRouting, b *sync.CompositeMutationBuilder) error {
	aliasPrefixRouting := &networkingv1alpha1.AliasPrefixRouting{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(aliasPrefix), aliasPrefixRouting); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("error getting alias prefix routing: %w", err)
		}

		b.PartialSync = true
		return nil
	}

	var targetDestinations []commonv1alpha1.LocalUIDReference
	for _, destination := range aliasPrefixRouting.Destinations {
		nic := &networkingv1alpha1.NetworkInterface{}
		nicKey := client.ObjectKey{Namespace: aliasPrefix.Namespace, Name: destination.Name}
		if err := r.Get(ctx, nicKey, nic); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("error getting network interface %s: %w", nicKey, err)
			}
			continue
		}

		machineRef := nic.Spec.MachineRef
		if destination.UID != nic.UID || machineRef == nil {
			continue
		}

		machine := &computev1alpha1.Machine{}
		machineKey := client.ObjectKey{Namespace: aliasPrefix.Namespace, Name: machineRef.Name}
		if err := r.Get(ctx, machineKey, machine); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("error getting network interface %s machine %s: %w", nicKey, machineKey, err)
			}
			continue
		}

		if machineRef.UID != destination.UID || computehelper.MachineRunsInMachinePool(machine, r.MachinePoolName) {
			continue
		}

		targetNic := &networkingv1alpha1.NetworkInterface{}
		if err := r.Provider.Target(ctx, nicKey, targetNic); err != nil {
			if !brokererrors.IsNotSynced(err) {
				return fmt.Errorf("error getting network interface %s target: %w", nicKey, err)
			}
			continue
		}

		targetDestinations = append(targetDestinations, commonv1alpha1.LocalUIDReference{
			Name: targetNic.Name,
			UID:  targetNic.UID,
		})
	}

	b.Add(func() {
		target.Destinations = targetDestinations
	})
	return nil
}

func (r *AliasPrefixReconciler) applyTargetRouting(ctx context.Context, log logr.Logger, aliasPrefix, target *networkingv1alpha1.AliasPrefix) error {
	var (
		targetAliasPrefixRouting = &networkingv1alpha1.AliasPrefixRouting{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: target.Namespace,
				Name:      target.Name,
			},
		}
		b sync.CompositeMutationBuilder
	)

	if err := r.registerAliasPrefixRoutingMutation(ctx, log, aliasPrefix, targetAliasPrefixRouting, &b); err != nil {
		return err
	}

	if _, err := brokerclient.BrokerControlledCreateOrPatch(ctx, r.TargetClient, r.ClusterName, aliasPrefix, targetAliasPrefixRouting,
		b.Mutate(targetAliasPrefixRouting),
	); err != nil {
		return sync.IgnorePartialCreate(err)
	}
	return nil
}

func (r *AliasPrefixReconciler) Target(ctx context.Context, key client.ObjectKey, targetObj client.Object) error {
	targetAliasPrefix := targetObj.(*networkingv1alpha1.AliasPrefix)

	aliasPrefix := &networkingv1alpha1.AliasPrefix{}
	if err := r.Get(ctx, key, aliasPrefix); err != nil {
		return err
	}

	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, aliasPrefix)
	if err != nil {
		return err
	}

	if err := r.TargetClient.Get(ctx, client.ObjectKey{Namespace: targetNamespace, Name: aliasPrefix.Name}, targetAliasPrefix); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return brokererrors.NewNotSynced(networkingv1alpha1.Resource("aliasprefixes"), aliasPrefix.Name)
	}
	return nil
}

func (r *AliasPrefixReconciler) SetupWithManager(mgr broker.Manager) error {
	log := logf.Log.WithName("aliasprefix").WithName("setup")
	ctx := ctrl.LoggerInto(context.TODO(), log)

	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&networkingv1alpha1.AliasPrefix{}).
		OwnsTarget(&networkingv1alpha1.AliasPrefix{}).
		Watches(
			&source.Kind{Type: &networkingv1alpha1.AliasPrefixRouting{}},
			&handler.EnqueueRequestForObject{},
		).
		WatchesTarget(
			&source.Kind{Type: &networkingv1alpha1.AliasPrefixRouting{}},
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
				aliasPrefixRouting := obj.(*networkingv1alpha1.AliasPrefixRouting)

				aliasPrefix := &networkingv1alpha1.AliasPrefix{}
				aliasPrefixKey := client.ObjectKeyFromObject(aliasPrefixRouting)
				if err := r.TargetClient.Get(ctx, aliasPrefixKey, aliasPrefix); err != nil {
					log.Error(err, "Error getting alias prefix for routing")
					return nil
				}

				brokerCtrl := brokermeta.GetBrokerControllerOf(aliasPrefix)
				if brokerCtrl == nil {
					return nil
				}

				ok, err := brokermeta.RefersToClusterAndType(r.ClusterName, &networkingv1alpha1.AliasPrefix{}, *brokerCtrl, r.Scheme)
				if err != nil {
					log.Error(err, "Error determining whether broker networkingctrl refers to alias prefix")
					return nil
				}
				if !ok {
					return nil
				}
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: brokerCtrl.Namespace, Name: brokerCtrl.Name}}}
			}),
		).
		Complete(r)
}
