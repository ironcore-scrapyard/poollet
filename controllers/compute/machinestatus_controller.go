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

	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	partitionletcomputev1alpha1 "github.com/onmetal/partitionlet/apis/compute/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type MachineStatusReconciler struct {
	client.Client
	ParentClient client.Client
	ParentCache  cache.Cache
	Namespace    string
}

//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machines,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machines/status,verbs=get;update;patch

func (r *MachineStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	parentMachine := &computev1alpha1.Machine{}
	if err := r.ParentClient.Get(ctx, req.NamespacedName, parentMachine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, parentMachine)
}

func (r *MachineStatusReconciler) reconcileExists(ctx context.Context, log logr.Logger, machine *computev1alpha1.Machine) (ctrl.Result, error) {
	if !machine.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, machine)
	}
	return r.reconcile(ctx, log, machine)
}

func (r *MachineStatusReconciler) reconcile(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine) (ctrl.Result, error) {
	machine := &computev1alpha1.Machine{}
	machineKey := client.ObjectKey{Namespace: r.Namespace, Name: partitionletcomputev1alpha1.MachineName(parentMachine.Namespace, parentMachine.Name)}
	if err := r.Get(ctx, machineKey, machine); err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting machine: %w", err)
	}

	log.V(1).Info("Patching machine status", "Machine", machine.Name)
	base := machine.DeepCopy()
	machine.Status.Interfaces = parentMachine.Status.Interfaces
	machine.Status.VolumeClaims = parentMachine.Status.VolumeClaims
	if err := r.Status().Patch(ctx, machine, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not patch machine status: %w", err)
	}

	log.V(1).Info("Successfully updated machine status", "Machine", machine.Name)
	return ctrl.Result{}, nil
}

func (r *MachineStatusReconciler) delete(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *MachineStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := controller.New("machine-status", mgr, controller.Options{
		Reconciler: r,
		Log:        mgr.GetLogger().WithName("machine-status"),
	})
	if err != nil {
		return fmt.Errorf("error creating controller: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&computev1alpha1.Machine{}, r.ParentCache),
		&handler.EnqueueRequestForObject{},
	); err != nil {
		return fmt.Errorf("error setting up parent machine watch: %w", err)
	}

	if err := c.Watch(
		&source.Kind{Type: &computev1alpha1.Machine{}},
		handler.EnqueueRequestsFromMapFunc(func(object client.Object) []reconcile.Request {
			annotations := object.GetAnnotations()
			parentNamespace, ok := annotations[partitionletcomputev1alpha1.MachineParentNamespaceAnnotation]
			if !ok {
				return nil
			}
			parentName, ok := annotations[partitionletcomputev1alpha1.MachineParentNameAnnotation]
			if !ok {
				return nil
			}

			return []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Namespace: parentNamespace,
						Name:      parentName,
					},
				},
			}
		}),
	); err != nil {
		return fmt.Errorf("error setting up machine watch: %w", err)
	}

	return nil
}
