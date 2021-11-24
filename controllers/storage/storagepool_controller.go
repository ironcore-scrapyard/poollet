// Copyright 2021 OnMetal authors
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

package storage

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
)

const storagePoolFieldOwner = client.FieldOwner("partitionlet.onmetal.de/storagepool")

type StoragePoolReconciler struct { //nolint
	client.Client
	ParentClient client.Client
	ParentCache  cache.Cache

	StoragePoolName           string
	ProviderID                string
	SourceStoragePoolSelector map[string]string
}

func (r *StoragePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	storagePool := &storagev1alpha1.StoragePool{}
	if err := r.ParentClient.Get(ctx, req.NamespacedName, storagePool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, storagePool)
}

func (r *StoragePoolReconciler) reconcileExists(ctx context.Context, log logr.Logger, pool *storagev1alpha1.StoragePool) (ctrl.Result, error) {
	if !pool.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, pool)
	}
	return r.reconcile(ctx, log, pool)
}

func (r *StoragePoolReconciler) delete(ctx context.Context, log logr.Logger, pool *storagev1alpha1.StoragePool) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *StoragePoolReconciler) reconcile(ctx context.Context, log logr.Logger, pool *storagev1alpha1.StoragePool) (ctrl.Result, error) {
	log.Info("Reconciling pool")
	sourcePoolList := &storagev1alpha1.StoragePoolList{}
	if err := r.List(ctx, sourcePoolList, client.MatchingLabels(r.SourceStoragePoolSelector)); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not list source pools: %w", err)
	}

	availableClassSet := map[corev1.LocalObjectReference]struct{}{}
	for _, pool := range sourcePoolList.Items {
		for _, availableStorageClass := range pool.Status.AvailableStorageClasses {
			availableClassSet[availableStorageClass] = struct{}{}
		}
	}

	availableClasses := make([]corev1.LocalObjectReference, 0, len(availableClassSet))
	for availableClass := range availableClassSet {
		availableClasses = append(availableClasses, availableClass)
	}

	base := pool.DeepCopy()
	pool.Status.AvailableStorageClasses = availableClasses
	log.Info("Updating available classes")
	if err := r.ParentClient.Status().Patch(ctx, pool, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("error updating storage classes: %w", err)
	}
	log.Info("Successfully synced pool")
	return ctrl.Result{}, nil
}

func (r *StoragePoolReconciler) birthCry(ctx context.Context) error {
	storagePool := &storagev1alpha1.StoragePool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: storagev1alpha1.GroupVersion.String(),
			Kind:       "StoragePool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: r.StoragePoolName,
		},
		Spec: storagev1alpha1.StoragePoolSpec{
			ProviderID: r.ProviderID,
		},
	}
	if err := r.ParentClient.Patch(ctx, storagePool, client.Apply, storagePoolFieldOwner); err != nil {
		return fmt.Errorf("error appylying storagepool in parent cluster: %w", err)
	}
	return nil
}

func (r *StoragePoolReconciler) SetupWithManager(mgr manager.Manager) error {
	ctx := context.Background()

	var sourcePoolPredicates []predicate.Predicate
	if r.SourceStoragePoolSelector != nil {
		prct, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{MatchLabels: r.SourceStoragePoolSelector})
		if err != nil {
			return fmt.Errorf("could not instantiate label selector predicate: %w", err)
		}

		sourcePoolPredicates = append(sourcePoolPredicates, prct)
	}

	if err := r.birthCry(ctx); err != nil {
		return fmt.Errorf("error initializing storage pool: %w", err)
	}

	c, err := controller.New("storage-pool", mgr, controller.Options{
		Reconciler: r,
		Log:        ctrl.Log.WithName("storage-pool"),
	})
	if err != nil {
		return fmt.Errorf("error creating storage pool controller: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&storagev1alpha1.StoragePool{}, r.ParentCache),
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(object client.Object) bool {
			pool := object.(*storagev1alpha1.StoragePool)
			return pool.Name == r.StoragePoolName && pool.Spec.ProviderID == r.ProviderID
		}),
	); err != nil {
		return fmt.Errorf("error setting up parent storage pool watch")
	}

	if err := c.Watch(
		&source.Kind{Type: &storagev1alpha1.StoragePool{}},
		handler.EnqueueRequestsFromMapFunc(func(object client.Object) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: r.StoragePoolName}}}
		}),
		sourcePoolPredicates...,
	); err != nil {
		return fmt.Errorf("error setting up source storage pool watch")
	}

	return nil
}
