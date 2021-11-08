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

package compute

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const machinePoolFieldOwner = client.FieldOwner("partitionlet.onmetal.de/machinepool")

type MachinePoolReconciler struct {
	client.Client
	ParentClient client.Client
	ParentCache  cache.Cache

	MachinePoolName           string
	ProviderID                string
	SourceMachinePoolSelector map[string]string
}

func (r *MachinePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	machinePool := &computev1alpha1.MachinePool{}
	if err := r.ParentClient.Get(ctx, req.NamespacedName, machinePool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, machinePool)
}

func (r *MachinePoolReconciler) reconcileExists(ctx context.Context, log logr.Logger, pool *computev1alpha1.MachinePool) (ctrl.Result, error) {
	if !pool.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, pool)
	}
	return r.reconcile(ctx, log, pool)
}

func (r *MachinePoolReconciler) delete(ctx context.Context, log logr.Logger, pool *computev1alpha1.MachinePool) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *MachinePoolReconciler) reconcile(ctx context.Context, log logr.Logger, pool *computev1alpha1.MachinePool) (ctrl.Result, error) {
	log.Info("Reconciling pool")
	sourcePoolList := &computev1alpha1.MachinePoolList{}
	if err := r.List(ctx, sourcePoolList, client.MatchingLabels(r.SourceMachinePoolSelector)); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not list source pools: %w", err)
	}

	availableClassSet := map[corev1.LocalObjectReference]struct{}{}
	for _, pool := range sourcePoolList.Items {
		for _, availableMachineClass := range pool.Status.AvailableMachineClasses {
			availableClassSet[availableMachineClass] = struct{}{}
		}
	}

	availableClasses := make([]corev1.LocalObjectReference, 0, len(availableClassSet))
	for availableClass := range availableClassSet {
		availableClasses = append(availableClasses, availableClass)
	}

	base := pool.DeepCopy()
	pool.Status.AvailableMachineClasses = availableClasses
	log.Info("Updating available classes")
	if err := r.ParentClient.Status().Patch(ctx, pool, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("error updating machine classes: %w", err)
	}
	log.Info("Successfully synced pool")
	return ctrl.Result{}, nil
}

func (r *MachinePoolReconciler) birthCry(ctx context.Context) error {
	machinePool := &computev1alpha1.MachinePool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: computev1alpha1.GroupVersion.String(),
			Kind:       "MachinePool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: r.MachinePoolName,
		},
		Spec: computev1alpha1.MachinePoolSpec{
			ProviderID: r.ProviderID,
		},
	}
	if err := r.ParentClient.Patch(ctx, machinePool, client.Apply, machinePoolFieldOwner); err != nil {
		return fmt.Errorf("error appylying machinepool in parent cluster: %w", err)
	}
	return nil
}

func (r *MachinePoolReconciler) SetupWithManager(mgr manager.Manager) error {
	ctx := context.Background()

	var sourcePoolPredicates []predicate.Predicate
	if r.SourceMachinePoolSelector != nil {
		prct, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{MatchLabels: r.SourceMachinePoolSelector})
		if err != nil {
			return fmt.Errorf("could not instantiate label selector predicate: %w", err)
		}

		sourcePoolPredicates = append(sourcePoolPredicates, prct)
	}

	if err := r.birthCry(ctx); err != nil {
		return fmt.Errorf("error initializing machine pool: %w", err)
	}

	c, err := controller.New("machine-pool", mgr, controller.Options{
		Reconciler: r,
		Log:        ctrl.Log.WithName("machine-pool"),
	})
	if err != nil {
		return fmt.Errorf("error creating machine pool controller: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&computev1alpha1.MachinePool{}, r.ParentCache),
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(object client.Object) bool {
			machinePool := object.(*computev1alpha1.MachinePool)
			return machinePool.Name == r.MachinePoolName && machinePool.Spec.ProviderID == r.ProviderID
		}),
	); err != nil {
		return fmt.Errorf("error setting up parent machine pool watch")
	}

	if err := c.Watch(
		&source.Kind{Type: &computev1alpha1.MachinePool{}},
		handler.EnqueueRequestsFromMapFunc(func(object client.Object) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: r.MachinePoolName}}}
		}),
		sourcePoolPredicates...,
	); err != nil {
		return fmt.Errorf("error setting up source machine pool watch")
	}

	return nil
}
