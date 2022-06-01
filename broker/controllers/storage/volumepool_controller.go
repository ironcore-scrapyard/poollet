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
	storageindexclient "github.com/onmetal/poollet/api/storage/client/index"
	storagepredicate "github.com/onmetal/poollet/api/storage/predicate"
	"github.com/onmetal/poollet/broker"
	"github.com/onmetal/poollet/broker/builder"
	"github.com/onmetal/poollet/broker/domain"
	brokerpredicate "github.com/onmetal/poollet/broker/predicate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type VolumePoolReconciler struct {
	client.Client

	Target client.Client

	PoolName            string
	ProviderID          string
	InitPoolLabels      map[string]string
	InitPoolAnnotations map[string]string

	TargetPoolLabels map[string]string
	TargetPoolName   string

	ClusterName string
	Domain      domain.Domain
}

func (r *VolumePoolReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log = log.WithValues("namespace", "", "name", r.PoolName)

	volumePool := &storagev1alpha1.VolumePool{}
	if err := r.Get(ctx, client.ObjectKey{Name: r.PoolName}, volumePool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, volumePool)
}

func (r *VolumePoolReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.ClusterName)
}

func (r *VolumePoolReconciler) finalizer() string {
	return r.domain().Slash("volumepool")
}

func (r *VolumePoolReconciler) fieldOwner() client.FieldOwner {
	return client.FieldOwner(r.domain().Slash("volumepool"))
}

func (r *VolumePoolReconciler) reconcileExists(ctx context.Context, log logr.Logger, volumePool *storagev1alpha1.VolumePool) (ctrl.Result, error) {
	if !volumePool.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, volumePool)
	}
	return r.reconcile(ctx, log, volumePool)
}

func (r *VolumePoolReconciler) delete(ctx context.Context, log logr.Logger, volumePool *storagev1alpha1.VolumePool) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(volumePool, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Finalizer present, deleting volumes assigned to pool")
	volumes, err := storageindexclient.ListVolumesRunningOnVolumePool(ctx, r.Client, r.PoolName)
	if err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Deleting volumes on pool")
	for _, volume := range volumes {
		if err := r.Delete(ctx, &volume); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Error deleting volume", "VolumeKey", client.ObjectKeyFromObject(&volume))
		}
	}

	if len(volumes) > 0 {
		log.V(1).Info("Volumes are still present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("All volumes are gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, volumePool, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Finalizer successfully removed")
	return ctrl.Result{}, nil
}

func (r *VolumePoolReconciler) targetPools(ctx context.Context) ([]storagev1alpha1.VolumePool, error) {
	if r.TargetPoolName != "" {
		targetPool := &storagev1alpha1.VolumePool{}
		targetPoolKey := client.ObjectKey{Name: r.TargetPoolName}
		if err := r.Target.Get(ctx, targetPoolKey, targetPool); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("error getting target pool %s: %w", r.TargetPoolName, err)
			}
			return nil, nil
		}

		return []storagev1alpha1.VolumePool{*targetPool}, nil
	}

	targetPoolList := &storagev1alpha1.VolumePoolList{}
	if err := r.Target.List(ctx, targetPoolList,
		client.MatchingLabels(r.TargetPoolLabels),
	); err != nil {
		return nil, fmt.Errorf("error listing target pools: %w", err)
	}

	return targetPoolList.Items, nil
}

func (r *VolumePoolReconciler) accumulatePools(ctx context.Context) (availableVolumeClasses []corev1.LocalObjectReference, available, used corev1.ResourceList, err error) {
	pools, err := r.targetPools(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	availableVolumeClassNames := sets.NewString()

	for _, targetPool := range pools {
		for _, ref := range targetPool.Status.AvailableVolumeClasses {
			availableVolumeClassNames.Insert(ref.Name)
		}

		available = quotav1.Add(available, targetPool.Status.Available)
		used = quotav1.Add(used, targetPool.Status.Used)
	}

	availableVolumeClasses = make([]corev1.LocalObjectReference, 0, len(availableVolumeClassNames))
	for _, name := range availableVolumeClassNames.List() {
		availableVolumeClasses = append(availableVolumeClasses, corev1.LocalObjectReference{Name: name})
	}
	return availableVolumeClasses, available, used, nil
}

func (r *VolumePoolReconciler) patchStatus(
	ctx context.Context,
	volumePool *storagev1alpha1.VolumePool,
	state storagev1alpha1.VolumePoolState,
	availableVolumeClasses []corev1.LocalObjectReference,
	available, used corev1.ResourceList,
) error {
	base := volumePool.DeepCopy()
	volumePool.Status.State = state
	volumePool.Status.AvailableVolumeClasses = availableVolumeClasses
	volumePool.Status.Available = available
	volumePool.Status.Used = used
	if err := r.Status().Patch(ctx, volumePool, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching status: %w", err)
	}
	return nil
}

func (r *VolumePoolReconciler) reconcile(ctx context.Context, log logr.Logger, volumePool *storagev1alpha1.VolumePool) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")
	availableVolumeClasses, available, used, err := r.accumulatePools(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting available volume classes: %w", err)
	}

	if err := r.patchStatus(ctx, volumePool, storagev1alpha1.VolumePoolStateAvailable, availableVolumeClasses, available, used); err != nil {
		return ctrl.Result{}, fmt.Errorf("error patching status: %w", err)
	}

	log.V(1).Info("Successfully reconciled volume pool")
	return ctrl.Result{}, nil
}

func (r *VolumePoolReconciler) initialize(ctx context.Context, log logr.Logger) error {
	volumePool := &storagev1alpha1.VolumePool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: storagev1alpha1.SchemeGroupVersion.String(),
			Kind:       "VolumePool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.PoolName,
			Labels:      r.InitPoolLabels,
			Annotations: r.InitPoolAnnotations,
		},
		Spec: storagev1alpha1.VolumePoolSpec{
			ProviderID: r.ProviderID,
		},
	}
	log.V(1).Info("Initializing volume pool")
	if err := r.Patch(ctx, volumePool, client.Apply, r.fieldOwner()); err != nil {
		return fmt.Errorf("error appyling volume pool: %w", err)
	}
	return nil
}

func (r *VolumePoolReconciler) SetupWithManager(mgr broker.Manager) error {
	log := ctrl.Log.WithName("volumepool").WithName("setup")
	ctx := ctrl.LoggerInto(context.TODO(), log)

	if err := r.initialize(ctx, log); err != nil {
		return fmt.Errorf("error initializing volume pool")
	}

	return broker.NewControllerManagedBy(mgr, r.ClusterName).
		For(&storagev1alpha1.VolumePool{}).
		Watches(
			&source.Kind{Type: &storagev1alpha1.Volume{}},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(
				storagepredicate.VolumeRunsInVolumePoolPredicate(r.PoolName),
			),
		).
		WatchesTarget(
			&source.Kind{Type: &storagev1alpha1.VolumePool{}},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(
				brokerpredicate.NameLabelsPredicate(r.TargetPoolName, r.TargetPoolLabels),
			),
		).
		Complete(r)
}
