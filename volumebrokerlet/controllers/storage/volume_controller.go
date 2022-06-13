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
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	storagehelper "github.com/onmetal/poollet/api/storage/helper"
	storagefields "github.com/onmetal/poollet/api/storage/index/fields"
	"github.com/onmetal/poollet/broker"
	"github.com/onmetal/poollet/broker/controllers/storage"
	"github.com/onmetal/poollet/broker/domain"
	"github.com/onmetal/poollet/broker/provider"
	volumebrokerletcontrollerscommon "github.com/onmetal/poollet/volumebrokerlet/controllers/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type VolumeReconciler struct {
	Provider provider.Provider

	client.Client
	TargetClient client.Client
	APIReader    client.Reader
	Scheme       *runtime.Scheme

	PoolName string

	TargetPoolLabels map[string]string
	TargetPoolName   string

	ClusterName string
}

func (r *VolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	volume := &storagev1alpha1.Volume{}
	if err := r.Get(ctx, req.NamespacedName, volume); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, volume)
}

func (r *VolumeReconciler) domain() domain.Domain {
	return volumebrokerletcontrollerscommon.Domain.Subdomain(r.PoolName)
}

func (r *VolumeReconciler) finalizer() string {
	return r.domain().Slash("volume")
}

func (r *VolumeReconciler) reconcileExists(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !storagehelper.VolumeRunsInVolumePool(volume, r.PoolName) {
		return ctrl.Result{}, nil
	}

	if !volume.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, volume)
	}

	return r.reconcile(ctx, log, volume)
}

func (r *VolumeReconciler) delete(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(volume, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Deleting target")
	done, err := r.applier().DeleteTarget(ctx, volume)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !done {
		log.V(1).Info("Issued target deletion")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Target is gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, volume, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}
	log.V(1).Info("Successfully removed finalizer")
	return ctrl.Result{}, nil
}

func (r *VolumeReconciler) applier() *storage.SyncVolumeApplier {
	return &storage.SyncVolumeApplier{
		Unclaimable:      true,
		Provider:         r.Provider,
		TargetPoolName:   r.TargetPoolName,
		TargetPoolLabels: r.TargetPoolLabels,
		ClusterName:      r.ClusterName,
		TargetClient:     r.TargetClient,
	}
}

func (r *VolumeReconciler) accessApplier() *storage.AccessApplier {
	return &storage.AccessApplier{
		Domain:       r.domain(),
		APIReader:    r.APIReader,
		Client:       r.Client,
		TargetClient: r.TargetClient,
	}
}

func (r *VolumeReconciler) reconcile(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, volume, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Applying target volume")
	target, partial, err := r.applier().ApplyTarget(ctx, volume)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		log.V(1).Info("Target dependencies are not ready", "Partial", partial)
		return ctrl.Result{Requeue: partial}, nil
	}

	log.V(1).Info("Applying access")
	access, err := r.accessApplier().ApplyAccess(ctx, log, volume, target)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error applying volume access: %w", err)
	}

	log.V(1).Info("Patching status")
	if err := storage.PatchVolumeStatus(ctx, r.Client, volume, target.Status.State, access); err != nil {
		return ctrl.Result{}, fmt.Errorf("error patching volume status: %w", err)
	}

	log.V(1).Info("Successfully reconciled volume", "Partial", partial)
	return ctrl.Result{Requeue: partial}, nil
}

func (r *VolumeReconciler) Target(ctx context.Context, key client.ObjectKey, obj client.Object) error {
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

func (r *VolumeReconciler) SetupWithManager(mgr broker.Manager) error {
	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&storagev1alpha1.Volume{}).
		OwnsTarget(&storagev1alpha1.Volume{}).
		Owns(&corev1.Secret{}).
		ReferencesViaField(&corev1.Secret{}, storagefields.VolumeSpecSecretNamesField).
		ReferencesTargetViaField(&corev1.Secret{}, storagefields.VolumeStatusSecretNamesField).
		Complete(r)
}
