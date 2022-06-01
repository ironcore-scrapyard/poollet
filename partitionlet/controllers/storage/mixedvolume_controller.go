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
	storagehelper "github.com/onmetal/poollet/api/storage/helper"
	storagefields "github.com/onmetal/poollet/api/storage/index/fields"
	"github.com/onmetal/poollet/broker"
	"github.com/onmetal/poollet/broker/builder"
	"github.com/onmetal/poollet/broker/controllers/storage"
	"github.com/onmetal/poollet/broker/domain"
	"github.com/onmetal/poollet/broker/provider"
	partitionletcontrollerscommon "github.com/onmetal/poollet/partitionlet/controllers/common"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type MixedVolumeReconciler struct {
	Provider provider.Provider

	client.Client
	APIReader    client.Reader
	TargetClient client.Client

	PoolName        string
	MachinePoolName string

	TargetPoolName   string
	TargetPoolLabels map[string]string

	FallbackPoolName   string
	FallbackPoolLabels map[string]string

	ClusterName string
}

func (r *MixedVolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	volume := &storagev1alpha1.Volume{}
	if err := r.Get(ctx, req.NamespacedName, volume); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, volume)
}

func (r *MixedVolumeReconciler) domain() domain.Domain {
	return partitionletcontrollerscommon.Domain.Subdomain(r.ClusterName)
}

func (r *MixedVolumeReconciler) finalizer() string {
	return r.domain().Slash("volume")
}

func (r *MixedVolumeReconciler) reconcileExists(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !volume.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, volume)
	}
	if storagehelper.VolumeRunsInVolumePool(volume, r.PoolName) {
		return r.reconcile(ctx, log, volume)
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

func (r *MixedVolumeReconciler) delete(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(volume, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Deleting target")
	applier, _, err := r.applierFor(volume)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting applier for volume: %w", err)
	}

	log.V(1).Info("Issuing target deletion")
	done, err := applier.DeleteTarget(ctx, volume)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting target: %w", err)
	}
	if !done {
		log.V(1).Info("Issued target deletion")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Target is gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, volume, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *MixedVolumeReconciler) applierFor(volume *storagev1alpha1.Volume) (applier storage.VolumeApplier, managed bool, err error) {
	if volume.Spec.VolumePoolRef != nil && volume.Spec.VolumePoolRef.Name == r.PoolName {
		return &storage.SyncVolumeApplier{
			Provider:         r.Provider,
			TargetPoolName:   r.TargetPoolName,
			TargetPoolLabels: r.TargetPoolLabels,
			ClusterName:      r.ClusterName,
			TargetClient:     r.TargetClient,
		}, true, nil
	}

	if r.FallbackPoolName == "" && len(r.FallbackPoolLabels) == 0 {
		return nil, false, fmt.Errorf("volume is not managed by mixed volume reconciler and no fallback given")
	}
	return &storage.SyncVolumeApplier{
		Provider:         r.Provider,
		TargetPoolName:   r.FallbackPoolName,
		TargetPoolLabels: r.FallbackPoolLabels,
		ClusterName:      r.ClusterName,
		TargetClient:     r.TargetClient,
	}, false, nil
}

func (r *MixedVolumeReconciler) accessApplier() *storage.AccessApplier {
	return &storage.AccessApplier{
		Domain:       r.domain(),
		APIReader:    r.APIReader,
		Client:       r.Client,
		TargetClient: r.TargetClient,
	}
}

func (r *MixedVolumeReconciler) reconcile(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, volume, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	applier, managed, err := r.applierFor(volume)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting applier for volume: %w", err)
	}

	log.V(1).Info("Applying target")
	target, partial, err := applier.ApplyTarget(ctx, volume)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		log.V(1).Info("Target dependencies are not yet ready", "Partial", partial)
		return ctrl.Result{Requeue: partial}, nil
	}

	if managed {
		log.V(1).Info("Applying access")
		access, err := r.accessApplier().ApplyAccess(ctx, log, volume, target)
		if err != nil {
			return ctrl.Result{}, err
		}

		log.V(1).Info("Patching status")
		if err := storage.PatchVolumeStatus(ctx, r.Client, volume, target.Status.State, access); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.V(1).Info("Reconciled", "Partial", partial)
	return ctrl.Result{Requeue: partial}, nil
}

func (r *MixedVolumeReconciler) Target(ctx context.Context, key client.ObjectKey, targetObj client.Object) error {
	target := targetObj.(*storagev1alpha1.Volume)

	volume := &storagev1alpha1.Volume{}
	if err := r.Get(ctx, key, volume); err != nil {
		return err
	}

	applier, _, err := r.applierFor(volume)
	if err != nil {
		return err
	}

	res, err := applier.GetTarget(ctx, volume)
	if err != nil {
		return err
	}

	*target = *res
	return nil
}

func (r *MixedVolumeReconciler) SetupWithManager(mgr broker.Manager) error {
	log := ctrl.Log.WithName("volume").WithName("setup")
	ctx := ctrl.LoggerInto(context.TODO(), log)

	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&storagev1alpha1.Volume{}).
		OwnsTarget(&storagev1alpha1.Volume{}).
		Watches(
			&source.Kind{Type: &storagev1alpha1.VolumeClaim{}},
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
				volumeClaim := obj.(*storagev1alpha1.VolumeClaim)
				reqs, err := storagectrl.GetVolumeClaimVolumeReconcileRequests(ctx, r.Client, volumeClaim, r.MachinePoolName)
				if err != nil {
					log.Error(err, "Error getting volume claim volume reconcile requests")
					return nil
				}
				return reqs
			}),
		).
		Watches(
			&source.Kind{Type: &computev1alpha1.Machine{}},
			handler.EnqueueRequestsFromMapFunc(func(object client.Object) []ctrl.Request {
				machine := object.(*computev1alpha1.Machine)
				reqs, err := storagectrl.GetMachineVolumeReconcileRequests(ctx, r.Client, machine)
				if err != nil {
					log.Error(err, "Error getting machine volume reconcile requests")
					return nil
				}
				return reqs
			}),
			builder.WithPredicates(computepredicate.MachineRunsInMachinePoolPredicate(r.MachinePoolName)),
		).
		ReferencesViaField(&corev1.Secret{}, storagefields.VolumeSpecSecretNamesField).
		ReferencesTargetViaField(&corev1.Secret{}, storagefields.VolumeStatusSecretNamesField).
		Complete(r)
}
