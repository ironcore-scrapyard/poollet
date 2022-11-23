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
	networkingv1alpha1 "github.com/onmetal/onmetal-api/api/networking/v1alpha1"
	networkingctrl "github.com/onmetal/poollet/api/networking/controller"
	"github.com/onmetal/poollet/broker"
	brokerclient "github.com/onmetal/poollet/broker/client"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	"github.com/onmetal/poollet/broker/provider"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type NetworkReconciler struct {
	Provider provider.Provider

	client.Client
	APIReader    client.Reader
	TargetClient client.Client

	ClusterName     string
	MachinePoolName string
	Domain          domain.Domain
}

func (r *NetworkReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.MachinePoolName)
}

func (r *NetworkReconciler) finalizer() string {
	return r.domain().Slash("network")
}

func (r *NetworkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	network := &networkingv1alpha1.Network{}
	if err := r.Get(ctx, req.NamespacedName, network); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, network)
}

func (r *NetworkReconciler) reconcileExists(ctx context.Context, log logr.Logger, network *networkingv1alpha1.Network) (ctrl.Result, error) {
	if !network.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, network)
	}
	ok, err := networkingctrl.IsNetworkUsedCachedOrLive(ctx, r.APIReader, r.Client, network, r.MachinePoolName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return r.delete(ctx, log, network)
	}
	return r.reconcile(ctx, log, network)
}

func (r *NetworkReconciler) delete(ctx context.Context, log logr.Logger, network *networkingv1alpha1.Network) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(network, r.finalizer()) {
		log.V(2).Info("No finalizer present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Determining target namespace")
	targetNamespace := &corev1.Namespace{}
	if err := r.Provider.Target(ctx, client.ObjectKey{Name: network.Namespace}, targetNamespace); err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}

	log = log.WithValues("TargetNamespace", targetNamespace.Name)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Deleting target if exists")
	existed, err := clientutils.DeleteIfExists(ctx, r.TargetClient, &networkingv1alpha1.Network{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace.Name,
			Name:      network.Name,
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
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, network, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Removed finalizer")
	return ctrl.Result{}, nil
}

func (r *NetworkReconciler) reconcile(ctx context.Context, log logr.Logger, network *networkingv1alpha1.Network) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, network)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}

	log = log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, network, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	target := &networkingv1alpha1.Network{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      network.Name,
		},
	}

	log.V(1).Info("Applying target")
	if _, err := brokerclient.BrokerControlledCreateOrPatch(
		ctx,
		r.TargetClient,
		r.ClusterName,
		network,
		target,
		func() error {
			return nil
		},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("error applying target: %w", err)
	}

	log.V(1).Info("Applied target")
	return ctrl.Result{}, nil
}

func (r *NetworkReconciler) Target(ctx context.Context, key client.ObjectKey, targetObj client.Object) error {
	targetNetwork := targetObj.(*networkingv1alpha1.Network)

	network := &networkingv1alpha1.Network{}
	if err := r.Get(ctx, key, network); err != nil {
		return err
	}

	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, network)
	if err != nil {
		return err
	}

	if err := r.TargetClient.Get(ctx, client.ObjectKey{Namespace: targetNamespace, Name: network.Name}, targetNetwork); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return brokererrors.NewNotSynced(networkingv1alpha1.Resource("networks"), network.Name)
	}
	return nil
}

func (r *NetworkReconciler) SetupWithManager(mgr broker.Manager) error {
	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&networkingv1alpha1.Network{}).
		OwnsTarget(&networkingv1alpha1.Network{}).
		Watches(
			&source.Kind{Type: &networkingv1alpha1.NetworkInterface{}},
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
				nic := obj.(*networkingv1alpha1.NetworkInterface)
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: nic.Namespace, Name: nic.Spec.NetworkRef.Name}}}
			}),
		).
		Complete(r)
}
