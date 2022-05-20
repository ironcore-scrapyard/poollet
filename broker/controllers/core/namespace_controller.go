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

package core

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	"github.com/onmetal/poollet/broker"
	"github.com/onmetal/poollet/broker/builder"
	brokerclient "github.com/onmetal/poollet/broker/client"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	poolletmeta "github.com/onmetal/poollet/meta"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type namespaceReferent struct {
	Type       client.Object
	Predicates []predicate.Predicate
}

type NamespaceReconciler struct {
	references []namespaceReferent

	client.Client
	APIReader client.Reader

	TargetClient    client.Client
	TargetAPIReader client.Reader
	Scheme          *runtime.Scheme

	NamespacePrefix string
	ClusterName     string
	Domain          domain.Domain

	ResyncPeriod time.Duration
}

func (r *NamespaceReconciler) Dependent(obj client.Object, prct ...predicate.Predicate) {
	r.references = append(r.references, namespaceReferent{Type: obj, Predicates: prct})
}

func (r *NamespaceReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.ClusterName)
}

func (r *NamespaceReconciler) finalizer() string {
	return r.domain().Slash("namespace")
}

func (r *NamespaceReconciler) sourceUIDLabel() string {
	return r.domain().Slash("namespace-source-uid")
}

func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).V(1)
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, ns)
}

func (r *NamespaceReconciler) isUsed(ctx context.Context, ns *corev1.Namespace) (bool, error) {
	if ok, err := r.isUsedCached(ctx, ns); err != nil || ok {
		return ok, err
	}
	return r.isUsedLive(ctx, ns)
}

func (r *NamespaceReconciler) isUsedLive(ctx context.Context, ns *corev1.Namespace) (bool, error) {
	for _, ref := range r.references {
		list, err := poolletmeta.NewListForObject(ref.Type, r.Scheme)
		if err != nil {
			return false, err
		}
		if err := r.APIReader.List(ctx, list, client.InNamespace(ns.Name), client.Limit(1)); err != nil {
			return false, err
		}

		if meta.LenList(list) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (r *NamespaceReconciler) isUsedCached(ctx context.Context, ns *corev1.Namespace) (bool, error) {
	for _, ref := range r.references {
		list, err := poolletmeta.NewListForObject(ref.Type, r.Scheme)
		if err != nil {
			return false, err
		}
		if err := r.List(ctx, list, client.InNamespace(ns.Name), client.Limit(1)); err != nil {
			return false, err
		}

		if meta.LenList(list) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (r *NamespaceReconciler) reconcileExists(ctx context.Context, log logr.Logger, ns *corev1.Namespace) (ctrl.Result, error) {
	if !ns.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, ns)
	}

	ok, err := r.isUsed(ctx, ns)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return r.delete(ctx, log, ns)
	}
	return r.reconcile(ctx, log, ns)
}

func (r *NamespaceReconciler) delete(ctx context.Context, log logr.Logger, ns *corev1.Namespace) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(ns, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	if err := brokerclient.BrokerControlledListSingleAndDelete(ctx, r.TargetAPIReader, r.Client, r.ClusterName, ns, &corev1.Namespace{},
		client.MatchingLabels{r.sourceUIDLabel(): string(ns.UID)},
	); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error deleting target: %w", err)
		}

		log.V(1).Info("Target does not exist, removing finalizer")
		return ctrl.Result{}, clientutils.PatchRemoveFinalizer(ctx, r.Client, ns, r.finalizer())
	}

	log.V(1).Info("Target deletion issued")
	return ctrl.Result{Requeue: true}, nil
}

func (r *NamespaceReconciler) reconcile(ctx context.Context, log logr.Logger, ns *corev1.Namespace) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, ns, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Applying target")
	targetNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: r.NamespacePrefix}}
	if _, err := brokerclient.BrokerControlledListSingleGenerateOrPatch(ctx, r.TargetAPIReader, r.TargetClient, r.ClusterName, ns, targetNS, func() error {
		poolletmeta.SetLabel(targetNS, r.sourceUIDLabel(), string(ns.UID))
		return nil
	}, client.MatchingLabels{r.sourceUIDLabel(): string(ns.UID)}); err != nil {
		return ctrl.Result{}, fmt.Errorf("error applying target: %w", err)
	}

	log.V(1).Info("Applied target")
	return ctrl.Result{RequeueAfter: r.ResyncPeriod}, nil
}

// Target implements provider.Provider.
func (r *NamespaceReconciler) Target(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	targetNS := obj.(*corev1.Namespace)

	ns := &corev1.Namespace{}
	if err := r.Get(ctx, key, ns); err != nil {
		return err
	}

	if err := brokerclient.BrokerControlledListSingle(ctx, r.TargetAPIReader, r.Scheme, r.ClusterName, ns, targetNS,
		client.MatchingLabels{r.sourceUIDLabel(): string(ns.UID)},
	); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		return brokererrors.NewNotSynced(corev1.Resource("namespaces"), ns.Name)
	}
	return nil
}

func (r *NamespaceReconciler) SetupWithManager(mgr broker.Manager) error {
	if r.ResyncPeriod == 0 {
		r.ResyncPeriod = 10 * time.Second
	}

	b := broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		For(&corev1.Namespace{}).
		OwnsTarget(&corev1.Namespace{})

	for _, ref := range r.references {
		b.Watches(
			&source.Kind{Type: ref.Type},
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: obj.GetNamespace()}}}
			}),
			builder.WithPredicates(ref.Predicates...),
		)
	}

	return b.Complete(r)
}
