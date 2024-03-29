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
	networkingv1alpha1 "github.com/onmetal/onmetal-api/apis/networking/v1alpha1"
	networkingctrl "github.com/onmetal/poollet/api/networking/controller"
	networkingfields "github.com/onmetal/poollet/api/networking/index/fields"
	"github.com/onmetal/poollet/broker"
	brokerclient "github.com/onmetal/poollet/broker/client"
	"github.com/onmetal/poollet/broker/controllers/networking/events"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	"github.com/onmetal/poollet/broker/provider"
	"github.com/onmetal/poollet/broker/sync"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type VirtualIPReconciler struct {
	record.EventRecorder
	Provider provider.Provider

	client.Client
	APIReader    client.Reader
	TargetClient client.Client

	ClusterName     string
	MachinePoolName string
	Domain          domain.Domain
}

func (r *VirtualIPReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.MachinePoolName)
}

func (r *VirtualIPReconciler) finalizer() string {
	return r.domain().Slash("virtualip")
}

func (r *VirtualIPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	virtualIP := &networkingv1alpha1.VirtualIP{}
	if err := r.Get(ctx, req.NamespacedName, virtualIP); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, virtualIP)
}

func (r *VirtualIPReconciler) reconcileExists(ctx context.Context, log logr.Logger, virtualIP *networkingv1alpha1.VirtualIP) (ctrl.Result, error) {
	if !virtualIP.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, virtualIP)
	}
	ok, err := networkingctrl.IsVirtualIPUsedCachedOrLive(ctx, r.APIReader, r.Client, virtualIP, r.MachinePoolName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return r.delete(ctx, log, virtualIP)
	}
	return r.reconcile(ctx, log, virtualIP)
}

func (r *VirtualIPReconciler) delete(ctx context.Context, log logr.Logger, virtualIP *networkingv1alpha1.VirtualIP) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(virtualIP, r.finalizer()) {
		log.V(2).Info("No finalizer present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Determining target namespace")
	targetNamespace := &corev1.Namespace{}
	if err := r.Provider.Target(ctx, client.ObjectKey{Name: virtualIP.Namespace}, targetNamespace); err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}

	log = log.WithValues("TargetNamespace", targetNamespace.Name)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Deleting target if exists")
	existed, err := clientutils.DeleteIfExists(ctx, r.TargetClient, &networkingv1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace.Name,
			Name:      virtualIP.Name,
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
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, virtualIP, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Removed finalizer")
	return ctrl.Result{}, nil
}

func (r *VirtualIPReconciler) registerTargetRefMutation(ctx context.Context, virtualIP, target *networkingv1alpha1.VirtualIP, b *sync.CompositeMutationBuilder) error {
	targetRef := virtualIP.Spec.TargetRef
	if targetRef == nil {
		b.Add(func() {
			target.Spec.TargetRef = nil
		})
		return nil
	}

	nicKey := client.ObjectKey{Namespace: virtualIP.Namespace, Name: targetRef.Name}
	targetNic := &networkingv1alpha1.NetworkInterface{}
	if err := r.Provider.Target(ctx, nicKey, targetNic); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting network interface %s target: %w", nicKey, err)
		}
		if apierrors.IsNotFound(err) {
			r.Eventf(virtualIP, corev1.EventTypeNormal, events.FailedSyncingVirtualIPTarget, "Target network interface %s not found", nicKey.Name)
		}
		// Since we don't depend on a network interface to be synced, we just set it to empty.
		b.Add(func() {
			target.Spec.TargetRef = nil
		})
		return nil
	}

	b.Add(func() {
		target.Spec.TargetRef = &commonv1alpha1.LocalUIDReference{
			Name: targetNic.Name,
			UID:  targetNic.UID,
		}
	})
	return nil
}

func (r *VirtualIPReconciler) registerDefaultMutation(ctx context.Context, virtualIP, target *networkingv1alpha1.VirtualIP, b *sync.CompositeMutationBuilder) error {
	b.Add(func() {
		target.Spec.Type = virtualIP.Spec.Type
		target.Spec.IPFamily = virtualIP.Spec.IPFamily
	})
	return nil
}

func (r *VirtualIPReconciler) reconcile(ctx context.Context, log logr.Logger, virtualIP *networkingv1alpha1.VirtualIP) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, virtualIP)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}

	log = log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, virtualIP, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Applying target")
	target, partial, err := r.applyTarget(ctx, log, virtualIP, targetNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		log.V(1).Info("Target dependencies are not yet ready", "Partial", partial)
		return ctrl.Result{Requeue: partial}, nil
	}

	log.V(1).Info("Applied target", "Partial", partial)
	return ctrl.Result{Requeue: partial}, nil
}

func (r *VirtualIPReconciler) applyTarget(ctx context.Context, log logr.Logger, virtualIP *networkingv1alpha1.VirtualIP, targetNamespace string) (*networkingv1alpha1.VirtualIP, bool, error) {
	var (
		target = &networkingv1alpha1.VirtualIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace,
				Name:      virtualIP.Name,
			},
		}
		b sync.CompositeMutationBuilder
	)

	if err := r.registerDefaultMutation(ctx, virtualIP, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerTargetRefMutation(ctx, virtualIP, target, &b); err != nil {
		return nil, false, err
	}

	log.V(1).Info("Applying target")
	if _, err := brokerclient.BrokerControlledCreateOrPatch(
		ctx,
		r.TargetClient,
		r.ClusterName,
		virtualIP,
		target,
		b.Mutate(target),
	); err != nil {
		return nil, b.PartialSync, sync.IgnorePartialCreate(err)
	}
	return target, b.PartialSync, nil
}

func (r *VirtualIPReconciler) Target(ctx context.Context, key client.ObjectKey, targetObj client.Object) error {
	targetVirtualIP := targetObj.(*networkingv1alpha1.VirtualIP)

	virtualIP := &networkingv1alpha1.VirtualIP{}
	if err := r.Get(ctx, key, virtualIP); err != nil {
		return err
	}

	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, virtualIP)
	if err != nil {
		return err
	}

	if err := r.TargetClient.Get(ctx, client.ObjectKey{Namespace: targetNamespace, Name: virtualIP.Name}, targetVirtualIP); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return brokererrors.NewNotSynced(networkingv1alpha1.Resource("virtualips"), virtualIP.Name)
	}
	return nil
}

func (r *VirtualIPReconciler) SetupWithManager(mgr broker.Manager) error {
	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&networkingv1alpha1.VirtualIP{}).
		OwnsTarget(&networkingv1alpha1.VirtualIP{}).
		ReferencesViaField(&networkingv1alpha1.NetworkInterface{}, networkingfields.VirtualIPSpecTargetRefName).
		Complete(r)
}
