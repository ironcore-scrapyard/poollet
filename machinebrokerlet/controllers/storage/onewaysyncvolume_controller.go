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

package storage

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	computepredicate "github.com/onmetal/poollet/api/compute/predicate"
	storagectrl "github.com/onmetal/poollet/api/storage/controller"
	storagefields "github.com/onmetal/poollet/api/storage/index/fields"
	"github.com/onmetal/poollet/broker"
	"github.com/onmetal/poollet/broker/builder"
	"github.com/onmetal/poollet/broker/controllers/storage"
	"github.com/onmetal/poollet/broker/domain"
	"github.com/onmetal/poollet/broker/provider"
	machinebrokerletcontrollerscommon "github.com/onmetal/poollet/machinebrokerlet/controllers/common"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type OneWaySyncVolumeReconciler struct {
	Provider provider.Provider

	client.Client
	APIReader    client.Reader
	TargetClient client.Client

	TargetPoolName   string
	TargetPoolLabels map[string]string

	MachinePoolName string
	ClusterName     string
}

func (r *OneWaySyncVolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	volume := &storagev1alpha1.Volume{}
	if err := r.Get(ctx, req.NamespacedName, volume); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, volume)
}

func (r *OneWaySyncVolumeReconciler) domain() domain.Domain {
	return machinebrokerletcontrollerscommon.Domain.Subdomain(r.ClusterName)
}

func (r *OneWaySyncVolumeReconciler) finalizer() string {
	return r.domain().Slash("volume")
}

func (r *OneWaySyncVolumeReconciler) applier() *storage.SyncVolumeApplier {
	return &storage.SyncVolumeApplier{
		Provider:         r.Provider,
		TargetPoolName:   r.TargetPoolName,
		TargetPoolLabels: r.TargetPoolLabels,
		ClusterName:      r.ClusterName,
		TargetClient:     r.TargetClient,
	}
}

func (r *OneWaySyncVolumeReconciler) reconcileExists(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !volume.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, volume)
	}
	used, err := storagectrl.IsVolumeUsedCachedOrLive(ctx, r.APIReader, r.Client, volume, r.MachinePoolName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !used {
		return r.delete(ctx, log, volume)
	}
	return r.reconcile(ctx, log, volume)
}

func (r *OneWaySyncVolumeReconciler) delete(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(volume, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Deleting target")
	ok, err := r.applier().DeleteTarget(ctx, volume)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting target: %w", err)
	}
	if !ok {
		log.V(1).Info("Issued target deletion")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Target is gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, volume, r.finalizer()); err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *OneWaySyncVolumeReconciler) reconcile(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, volume, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Applying target")
	target, partial, err := r.applier().ApplyTarget(ctx, volume)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		log.V(1).Info("Target dependencies are not yet ready", "Partial", partial)
		return ctrl.Result{Requeue: partial}, nil
	}

	log.V(1).Info("Reconciled", "Partial", partial)
	return ctrl.Result{Requeue: partial}, nil
}

func (r *OneWaySyncVolumeReconciler) Target(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	target := obj.(*storagev1alpha1.Volume)

	volume := &storagev1alpha1.Volume{}
	if err := r.Get(ctx, key, volume); err != nil {
		return err
	}

	res, err := r.applier().GetTarget(ctx, volume)
	if err != nil {
		return err
	}

	*target = *res
	return nil
}

func (r *OneWaySyncVolumeReconciler) SetupWithManager(mgr broker.Manager) error {
	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&storagev1alpha1.Volume{}).
		OwnsTarget(&storagev1alpha1.Volume{}).
		Watches(
			&source.Kind{Type: &computev1alpha1.Machine{}},
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
				machine := obj.(*computev1alpha1.Machine)
				return storagectrl.VolumeReconcileRequestsFromMachine(machine)
			}),
			builder.WithPredicates(computepredicate.MachineRunsInMachinePoolPredicate(r.MachinePoolName)),
		).
		ReferencesViaField(&corev1.Secret{}, storagefields.VolumeSpecSecretNamesField).
		ReferencesTargetViaField(&corev1.Secret{}, storagefields.VolumeStatusSecretNamesField).
		Complete(r)
}
