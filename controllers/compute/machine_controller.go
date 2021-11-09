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

	partitionlethandler "github.com/onmetal/partitionlet/handler"

	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/conditionutils"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	partitionletcomputev1alpha1 "github.com/onmetal/partitionlet/apis/compute/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	machineFinalizer         = "partitionlet.onmetal.de/machine"
	machineFieldOwner        = client.FieldOwner("partitionlet.onmetal.de/machine")
	machineMachineClassField = ".spec.machineClass.name"
)

type MachineReconciler struct {
	client.Client
	ParentClient client.Client

	ParentCache        cache.Cache
	ParentFieldIndexer client.FieldIndexer

	Namespace                 string
	MachinePoolName           string
	SourceMachinePoolSelector map[string]string
}

//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machines/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machineclasses,verbs=get;list;watch

func (r *MachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	parentMachine := &computev1alpha1.Machine{}
	if err := r.ParentClient.Get(ctx, req.NamespacedName, parentMachine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, parentMachine)
}

func (r *MachineReconciler) reconcileExists(ctx context.Context, log logr.Logger, machine *computev1alpha1.Machine) (ctrl.Result, error) {
	if !machine.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, machine)
	}
	return r.reconcile(ctx, log, machine)
}

func (r *MachineReconciler) reconcile(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(parentMachine, machineFinalizer) {
		base := parentMachine.DeepCopy()
		controllerutil.AddFinalizer(parentMachine, machineFinalizer)
		if err := r.ParentClient.Patch(ctx, parentMachine, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("could not set finalizer: %w", err)
		}

		return ctrl.Result{}, nil
	}

	// TODO: check whether to compare parent machine class w/ partition machine class
	machineClass := &computev1alpha1.MachineClass{}
	machineClassKey := client.ObjectKey{Name: parentMachine.Spec.MachineClass.Name}
	log.V(1).Info("Getting machine class", "MachineClass", machineClassKey)
	if err := r.Get(ctx, machineClassKey, machineClass); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error getting machine class")
		}

		base := parentMachine.DeepCopy()
		conditionutils.MustUpdateSlice(&parentMachine.Status.Conditions, string(partitionletcomputev1alpha1.MachineSynced),
			conditionutils.UpdateStatus(corev1.ConditionFalse),
			conditionutils.UpdateReason("MachineClassNotFound"),
			conditionutils.UpdateMessage("The referenced machine class does not exist in this partition."),
			conditionutils.UpdateObserved(parentMachine),
		)
		if err := r.ParentClient.Status().Patch(ctx, parentMachine, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	machine := &computev1alpha1.Machine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: computev1alpha1.GroupVersion.String(),
			Kind:       "Machine",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.Namespace,
			Name:      partitionletcomputev1alpha1.MachineName(parentMachine.Namespace, parentMachine.Name),
			Annotations: map[string]string{
				partitionletcomputev1alpha1.MachineParentNamespaceAnnotation: parentMachine.Namespace,
				partitionletcomputev1alpha1.MachineParentNameAnnotation:      parentMachine.Name,
			},
		},
		Spec: computev1alpha1.MachineSpec{
			Hostname:            parentMachine.Spec.Hostname,
			MachineClass:        corev1.LocalObjectReference{Name: machineClass.Name},
			Image:               parentMachine.Spec.Image,
			Interfaces:          parentMachine.Spec.Interfaces,
			MachinePoolSelector: r.SourceMachinePoolSelector,
		},
	}
	log.V(1).Info("Applying machine", "Machine", machine.Name)
	if err := r.Patch(ctx, machine, client.Apply, machineFieldOwner); err != nil {
		base := parentMachine.DeepCopy()
		conditionutils.MustUpdateSlice(&parentMachine.Status.Conditions, string(partitionletcomputev1alpha1.MachineSynced),
			conditionutils.UpdateStatus(corev1.ConditionFalse),
			conditionutils.UpdateReason("ApplyFailed"),
			conditionutils.UpdateMessage(fmt.Sprintf("Could not apply the machine: %v", err)),
			conditionutils.UpdateObserved(parentMachine),
		)
		if err := r.ParentClient.Status().Patch(ctx, parentMachine, client.MergeFrom(base)); err != nil {
			log.Error(err, "Could not update parent status")
		}
		return ctrl.Result{}, fmt.Errorf("error applying machine: %w", err)
	}

	log.V(1).Info("Updating parent machine status")
	baseParentMachine := parentMachine.DeepCopy()
	parentMachine.Status.State = machine.Status.State
	conditionutils.MustUpdateSlice(&parentMachine.Status.Conditions, string(partitionletcomputev1alpha1.MachineSynced),
		conditionutils.UpdateStatus(corev1.ConditionTrue),
		conditionutils.UpdateReason("Applied"),
		conditionutils.UpdateMessage("Successfully applied machine"),
		conditionutils.UpdateObserved(parentMachine),
	)
	if err := r.ParentClient.Status().Patch(ctx, parentMachine, client.MergeFrom(baseParentMachine)); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not update parent status: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *MachineReconciler) delete(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(parentMachine, machineFinalizer) {
		return ctrl.Result{}, nil
	}

	machine := &computev1alpha1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.Namespace,
			Name:      partitionletcomputev1alpha1.MachineName(parentMachine.Namespace, parentMachine.Name),
		},
	}
	if err := r.Delete(ctx, machine); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("could not delete machine: %w", err)
		}

		base := parentMachine.DeepCopy()
		controllerutil.RemoveFinalizer(parentMachine, machineFinalizer)
		if err := r.ParentClient.Patch(ctx, parentMachine, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("could not remove finalizer: %w", err)
		}

		return ctrl.Result{}, nil
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *MachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	log := ctrl.Log.WithName("machine-reconciler")
	ctx := ctrl.LoggerInto(context.Background(), log)

	if err := r.ParentFieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, machineMachineClassField, func(obj client.Object) []string {
		machine := obj.(*computev1alpha1.Machine)
		return []string{machine.Spec.MachineClass.Name}
	}); err != nil {
		return fmt.Errorf("error setting up %s indexer: %w", machineMachineClassField, err)
	}

	c, err := controller.New("machine", mgr, controller.Options{
		Reconciler: r,
		Log:        mgr.GetLogger().WithName("machine"),
	})
	if err != nil {
		return fmt.Errorf("error creating controller: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&computev1alpha1.Machine{}, r.ParentCache),
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			machine := obj.(*computev1alpha1.Machine)
			return machine.Spec.MachinePool.Name == r.MachinePoolName
		}),
	); err != nil {
		return fmt.Errorf("error setting up parent machine watch: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&computev1alpha1.MachineClass{}, r.ParentCache),
		handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
			parentClass := obj.(*computev1alpha1.MachineClass)
			list := &computev1alpha1.MachineList{}
			if err := r.ParentClient.List(ctx, list, client.MatchingFields{machineMachineClassField: parentClass.Name}); err != nil {
				log.Error(err, "Error listing parent machines")
				return nil
			}

			res := make([]reconcile.Request, 0, len(list.Items))
			for _, item := range list.Items {
				res = append(res, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(&item),
				})
			}
			return res
		}),
		&predicate.GenerationChangedPredicate{},
	); err != nil {
		return fmt.Errorf("error setting up parent machine class watch: %w", err)
	}

	if err := c.Watch(
		&source.Kind{Type: &computev1alpha1.Machine{}},
		&partitionlethandler.EnqueueRequestForParentObject{
			ParentNamespaceAnnotation: partitionletcomputev1alpha1.MachineParentNamespaceAnnotation,
			ParentNameAnnotation:      partitionletcomputev1alpha1.MachineParentNameAnnotation,
		},
	); err != nil {
		return fmt.Errorf("error setting up machine watch: %w", err)
	}

	return nil
}
