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
	brokermeta "github.com/onmetal/poollet/broker/meta"
	"github.com/onmetal/poollet/broker/provider"
	proxyvolumebrokerletcontrollerscommon "github.com/onmetal/poollet/proxyvolumebrokerlet/controllers/common"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type ProxyVolumeReconciler struct {
	Provider provider.Provider

	client.Client
	APIReader    client.Reader
	TargetClient client.Client

	PoolName string

	TargetPoolName   string
	TargetPoolLabels map[string]string

	ClusterName string
}

func (r *ProxyVolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	volume := &storagev1alpha1.Volume{}
	if err := r.Get(ctx, req.NamespacedName, volume); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, volume)
}

func (r *ProxyVolumeReconciler) domain() domain.Domain {
	return proxyvolumebrokerletcontrollerscommon.Domain.Subdomain(r.ClusterName)
}

func (r *ProxyVolumeReconciler) finalizer() string {
	return r.domain().Slash("volume")
}

func (r *ProxyVolumeReconciler) reconcileExists(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !storagehelper.VolumeRunsInVolumePool(volume, r.PoolName) {
		return ctrl.Result{}, nil
	}

	if !volume.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, volume)
	}
	return r.reconcile(ctx, log, volume)
}

func (r *ProxyVolumeReconciler) delete(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(volume, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Deleting target")
	done, err := r.applierFor(volume).DeleteTarget(ctx, volume)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting target: %w", err)
	}
	if !done {
		log.V(1).Info("Issued target deletion")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("No requeue, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, volume, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *ProxyVolumeReconciler) applierFor(volume *storagev1alpha1.Volume) storage.VolumeApplier {
	brokerCtrl := brokermeta.GetBrokerControllerOf(volume)
	if brokerCtrl == nil {
		return &storage.SyncVolumeApplier{
			Provider:         r.Provider,
			TargetPoolName:   r.TargetPoolName,
			TargetPoolLabels: r.TargetPoolLabels,
			ClusterName:      r.ClusterName,
			TargetClient:     r.TargetClient,
		}
	}
	return &storage.ProxyVolumeApplier{
		TargetClient: r.TargetClient,
		ClusterName:  r.ClusterName,
		Scheme:       r.Scheme(),
	}
}

func (r *ProxyVolumeReconciler) accessApplier() *storage.AccessApplier {
	return &storage.AccessApplier{
		Domain:       r.domain(),
		APIReader:    r.APIReader,
		Client:       r.Client,
		TargetClient: r.TargetClient,
	}
}

func (r *ProxyVolumeReconciler) reconcile(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, volume, r.finalizer())
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Applying target")
	applier := r.applierFor(volume)
	target, partial, err := applier.ApplyTarget(ctx, volume)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		log.V(1).Info("Target dependencies are not yet ready", "Partial", partial)
		return ctrl.Result{Requeue: partial}, nil
	}

	log.V(1).Info("Applying access")
	access, err := r.accessApplier().ApplyAccess(ctx, log, volume, target)
	if err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Patching status")
	if err := storage.PatchVolumeStatus(ctx, r.Client, volume, target.Status.State, access); err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Reconciled", "Partial", partial)
	return ctrl.Result{Requeue: partial}, nil
}

func (r *ProxyVolumeReconciler) Target(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	target := obj.(*storagev1alpha1.Volume)

	volume := &storagev1alpha1.Volume{}
	if err := r.Get(ctx, key, volume); err != nil {
		return err
	}

	res, err := r.applierFor(volume).GetTarget(ctx, volume)
	if err != nil {
		return err
	}

	*target = *res
	return nil
}

func (r *ProxyVolumeReconciler) SetupWithManager(mgr broker.Manager) error {
	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&storagev1alpha1.Volume{}).
		OwnsTarget(&storagev1alpha1.Volume{}).
		ReferencesViaField(&corev1.Secret{}, storagefields.VolumeSpecSecretNamesField).
		ReferencesTargetViaField(&corev1.Secret{}, storagefields.VolumeStatusSecretNamesField).
		Complete(r)
}
