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
	computev1alpha1 "github.com/onmetal/onmetal-api/api/compute/v1alpha1"
	computeindexclient "github.com/onmetal/poollet/api/compute/client/index"
	computepredicate "github.com/onmetal/poollet/api/compute/predicate"
	"github.com/onmetal/poollet/broker"
	"github.com/onmetal/poollet/broker/builder"
	"github.com/onmetal/poollet/broker/domain"
	"github.com/onmetal/poollet/broker/predicate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type MachinePoolReconciler struct {
	client.Client

	TargetClient client.Client

	PoolName            string
	ProviderID          string
	InitPoolLabels      map[string]string
	InitPoolAnnotations map[string]string

	TargetPoolLabels map[string]string
	TargetPoolName   string

	ClusterName string
	Domain      domain.Domain
}

func (r *MachinePoolReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log = log.WithValues("namespace", "", "name", r.PoolName)

	machinePool := &computev1alpha1.MachinePool{}
	if err := r.Get(ctx, client.ObjectKey{Name: r.PoolName}, machinePool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, machinePool)
}

func (r *MachinePoolReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.PoolName)
}

func (r *MachinePoolReconciler) finalizer() string {
	return r.domain().Slash("machinepool")
}

func (r *MachinePoolReconciler) fieldOwner() client.FieldOwner {
	return client.FieldOwner(r.domain().Slash("machinepool"))
}

func (r *MachinePoolReconciler) reconcileExists(ctx context.Context, log logr.Logger, machinePool *computev1alpha1.MachinePool) (ctrl.Result, error) {
	if !machinePool.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, machinePool)
	}
	return r.reconcile(ctx, log, machinePool)
}

func (r *MachinePoolReconciler) delete(ctx context.Context, log logr.Logger, machinePool *computev1alpha1.MachinePool) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(machinePool, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Finalizer present, deleting machines assigned to pool")
	machines, err := computeindexclient.ListMachinesRunningInMachinePool(ctx, r.Client, r.PoolName)
	if err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Deleting machines on pool")
	for _, machine := range machines {
		if err := r.Delete(ctx, &machine); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Error deleting machine", "MachineKey", client.ObjectKeyFromObject(&machine))
		}
	}

	if len(machines) > 0 {
		log.V(1).Info("Machines are still present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("All machines are gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, machinePool, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Finalizer successfully removed")
	return ctrl.Result{}, nil
}

func (r *MachinePoolReconciler) targetPools(ctx context.Context) ([]computev1alpha1.MachinePool, error) {
	if r.TargetPoolName != "" {
		targetPool := &computev1alpha1.MachinePool{}
		targetPoolKey := client.ObjectKey{Name: r.TargetPoolName}
		if err := r.TargetClient.Get(ctx, targetPoolKey, targetPool); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("error getting target pool %s: %w", r.TargetPoolName, err)
			}
			return nil, nil
		}

		return []computev1alpha1.MachinePool{*targetPool}, nil
	}

	targetPoolList := &computev1alpha1.MachinePoolList{}
	if err := r.TargetClient.List(ctx, targetPoolList,
		client.MatchingLabels(r.TargetPoolLabels),
	); err != nil {
		return nil, fmt.Errorf("error listing target pools: %w", err)
	}

	return targetPoolList.Items, nil
}

func (r *MachinePoolReconciler) accumulatePools(ctx context.Context) ([]corev1.LocalObjectReference, error) {
	targetPools, err := r.targetPools(ctx)
	if err != nil {
		return nil, err
	}

	availableMachineClassNames := sets.NewString()
	for _, targetPool := range targetPools {
		for _, availableMachineClass := range targetPool.Status.AvailableMachineClasses {
			availableMachineClassNames.Insert(availableMachineClass.Name)
		}
	}

	res := make([]corev1.LocalObjectReference, 0, len(availableMachineClassNames))
	for _, name := range availableMachineClassNames.List() {
		res = append(res, corev1.LocalObjectReference{Name: name})
	}
	return res, nil
}

func (r *MachinePoolReconciler) patchStatus(
	ctx context.Context,
	machinePool *computev1alpha1.MachinePool,
	state computev1alpha1.MachinePoolState,
	availableMachineClasses []corev1.LocalObjectReference,
) error {
	base := machinePool.DeepCopy()
	machinePool.Status.State = state
	machinePool.Status.AvailableMachineClasses = availableMachineClasses
	if err := r.Status().Patch(ctx, machinePool, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching status: %w", err)
	}
	return nil
}

func (r *MachinePoolReconciler) reconcile(ctx context.Context, log logr.Logger, machinePool *computev1alpha1.MachinePool) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")
	availableMachineClasses, err := r.accumulatePools(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting available machine classes: %w", err)
	}

	if err := r.patchStatus(ctx, machinePool, computev1alpha1.MachinePoolStateReady, availableMachineClasses); err != nil {
		return ctrl.Result{}, fmt.Errorf("error patching status: %w", err)
	}

	log.V(1).Info("Successfully reconciled machine pool")
	return ctrl.Result{}, nil
}

func (r *MachinePoolReconciler) initialize(ctx context.Context, log logr.Logger) error {
	machinePool := &computev1alpha1.MachinePool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: computev1alpha1.SchemeGroupVersion.String(),
			Kind:       "MachinePool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.PoolName,
			Labels:      r.InitPoolLabels,
			Annotations: r.InitPoolAnnotations,
		},
		Spec: computev1alpha1.MachinePoolSpec{
			ProviderID: r.ProviderID,
		},
	}
	log.V(1).Info("Initializing machine pool")
	if err := r.Patch(ctx, machinePool, client.Apply, r.fieldOwner()); err != nil {
		return fmt.Errorf("error appyling machine pool: %w", err)
	}
	return nil
}

func (r *MachinePoolReconciler) SetupWithManager(mgr broker.Manager) error {
	log := ctrl.Log.WithName("machinepool").WithName("setup")
	ctx := ctrl.LoggerInto(context.TODO(), log)

	if err := r.initialize(ctx, log); err != nil {
		return fmt.Errorf("error initializing machine pool: %w", err)
	}

	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		For(&computev1alpha1.MachinePool{}).
		Watches(
			&source.Kind{Type: &computev1alpha1.Machine{}},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(
				computepredicate.MachineRunsInMachinePoolPredicate(r.PoolName),
			),
		).
		WatchesTarget(
			&source.Kind{Type: &computev1alpha1.MachinePool{}},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(
				predicate.NameLabelsPredicate(r.TargetPoolName, r.TargetPoolLabels),
			),
		).
		Complete(r)
}
