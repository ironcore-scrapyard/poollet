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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/onmetal/controller-utils/conditionutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	partitionletstoragev1alpha1 "github.com/onmetal/partitionlet/apis/storage/v1alpha1"
	partitionlethandler "github.com/onmetal/partitionlet/handler"
)

const (
	volumeFinalizer        = "partitionlet.onmetal.de/volume"
	volumeFieldOwner       = client.FieldOwner("partitionlet.onmetal.de/volume")
	volumeStoragePoolField = ".spec.storagePool.name"
)

type VolumeReconciler struct {
	client.Client
	ParentClient              client.Client
	ParentCache               cache.Cache
	Namespace                 string
	StoragePoolName           string
	ParentFieldIndexer        client.FieldIndexer
	SourceStoragePoolSelector map[string]string
}

//+kubebuilder:rbac:groups=storage.onmetal.de,resources=volumes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.onmetal.de,resources=volumes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.onmetal.de,resources=volumes/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=storage.onmetal.de,resources=storagepools,verbs=get;list;watch
//+kubebuilder:rbac:groups=storage.onmetal.de,resources=storageclasses,verbs=get;list;watch

func (r *VolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	volume := &storagev1alpha1.Volume{}
	if err := r.ParentClient.Get(ctx, req.NamespacedName, volume); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.reconcileExists(ctx, log, volume)
}

func (r *VolumeReconciler) reconcileExists(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !volume.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, volume)
	}
	return r.reconcile(ctx, log, volume)
}

func (r *VolumeReconciler) delete(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(parentVolume, volumeFinalizer) {
		log.V(1).Info("Volume contains no finalizer, no deletion needs to be done")
		return ctrl.Result{}, nil
	}

	objectsToDelete := []client.Object{
		&storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: r.Namespace,
				Name:      partitionletstoragev1alpha1.VolumeName(parentVolume.Namespace, parentVolume.Name),
			},
		},
	}

	var goneCt int
	log.V(1).Info("Deleting dependent objects")
	for _, objectToDelete := range objectsToDelete {
		log.V(1).Info("Deleting dependent object", "Type", fmt.Sprintf("%T", objectToDelete), "DependentKey", client.ObjectKeyFromObject(objectToDelete))
		if err := r.Delete(ctx, objectToDelete); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("error deleting %T %s: %w", objectToDelete, client.ObjectKeyFromObject(objectToDelete), err)
			}
			goneCt++
		}
	}

	if goneCt != len(objectsToDelete) {
		log.V(1).Info("Not all dependent objects are gone", "Expected", len(objectsToDelete), "Actual", goneCt)
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("All dependent objects are gone, removing finalizer to allow deletion")
	base := parentVolume.DeepCopy()
	controllerutil.RemoveFinalizer(parentVolume, volumeFinalizer)
	if err := r.ParentClient.Patch(ctx, parentVolume, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Successfully removed finalizer")
	return ctrl.Result{}, nil
}

func (r *VolumeReconciler) reconcile(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume) (ctrl.Result, error) {
	log.Info("Reconciling volume")
	if !controllerutil.ContainsFinalizer(parentVolume, volumeFinalizer) {
		base := parentVolume.DeepCopy()
		controllerutil.AddFinalizer(parentVolume, volumeFinalizer)
		if err := r.ParentClient.Patch(ctx, parentVolume, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("could not set finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	storageClass := &storagev1alpha1.StorageClass{}
	storageClassKey := client.ObjectKey{Name: parentVolume.Spec.StorageClassRef.Name}
	log.V(1).Info("Getting storage class", "StorageClass", storageClassKey)
	if err := r.Get(ctx, storageClassKey, storageClass); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error getting storage class: %w", err)
		}

		base := parentVolume.DeepCopy()
		conditionutils.MustUpdateSlice(&parentVolume.Status.Conditions, string(storagev1alpha1.VolumeSynced),
			conditionutils.UpdateStatus(corev1.ConditionFalse),
			conditionutils.UpdateReason("StorageClassNotFound"),
			conditionutils.UpdateMessage("The referenced storage class does not exist in this partition."),
			conditionutils.UpdateObserved(parentVolume),
		)
		if err := r.ParentClient.Status().Patch(ctx, parentVolume, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	volume := &storagev1alpha1.Volume{
		TypeMeta: metav1.TypeMeta{
			APIVersion: storagev1alpha1.GroupVersion.String(),
			Kind:       "Volume",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.Namespace,
			Name:      partitionletstoragev1alpha1.VolumeName(parentVolume.Namespace, parentVolume.Name),
			Annotations: map[string]string{
				partitionletstoragev1alpha1.VolumeParentNamespaceAnnotation: parentVolume.Namespace,
				partitionletstoragev1alpha1.VolumeParentNameAnnotation:      parentVolume.Name,
			},
		},
		Spec: storagev1alpha1.VolumeSpec{
			StoragePoolSelector: r.SourceStoragePoolSelector,
			StorageClassRef:     corev1.LocalObjectReference{Name: storageClass.Name},
			Resources:           parentVolume.Spec.Resources,
		},
	}
	log.V(1).Info("Applying volume", "Volume", volume.Name)
	if err := r.Patch(ctx, volume, client.Apply, volumeFieldOwner); err != nil {
		base := parentVolume.DeepCopy()
		conditionutils.MustUpdateSlice(&parentVolume.Status.Conditions, string(storagev1alpha1.VolumeSynced),
			conditionutils.UpdateStatus(corev1.ConditionFalse),
			conditionutils.UpdateReason("ApplyFailed"),
			conditionutils.UpdateMessage(fmt.Sprintf("Could not apply the volume: %v", err)),
			conditionutils.UpdateObserved(parentVolume),
		)
		if err := r.ParentClient.Status().Patch(ctx, parentVolume, client.MergeFrom(base)); err != nil {
			log.Error(err, "Could not update parent status")
		}
		return ctrl.Result{}, fmt.Errorf("error applying volume: %w", err)
	}

	log.V(1).Info("Updating parent volume status")
	baseParentVolume := parentVolume.DeepCopy()
	parentVolume.Status.State = volume.Status.State
	conditionutils.MustUpdateSlice(&parentVolume.Status.Conditions, string(storagev1alpha1.VolumeSynced),
		conditionutils.UpdateStatus(corev1.ConditionTrue),
		conditionutils.UpdateReason("Applied"),
		conditionutils.UpdateMessage("Successfully applied volume"),
		conditionutils.UpdateObserved(parentVolume),
	)
	if err := r.ParentClient.Status().Patch(ctx, parentVolume, client.MergeFrom(baseParentVolume)); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not update parent status: %w", err)
	}
	log.Info("Successfully synced volume")
	return ctrl.Result{}, nil
}

func (r *VolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	log := ctrl.Log.WithName("volume-reconciler")
	ctx := ctrl.LoggerInto(context.Background(), log)

	if err := r.ParentFieldIndexer.IndexField(ctx, &storagev1alpha1.Volume{}, volumeStoragePoolField, func(obj client.Object) []string {
		volume := obj.(*storagev1alpha1.Volume)
		return []string{volume.Spec.StoragePool.Name}
	}); err != nil {
		return fmt.Errorf("error setting up %s indexer: %w", volumeStoragePoolField, err)
	}

	c, err := controller.New("volume", mgr, controller.Options{
		Reconciler: r,
		Log:        mgr.GetLogger().WithName("volume"),
	})
	if err != nil {
		return fmt.Errorf("error creating volume controller: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&storagev1alpha1.Volume{}, r.ParentCache),
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			volume := obj.(*storagev1alpha1.Volume)
			return volume.Spec.StoragePool.Name == r.StoragePoolName
		}),
	); err != nil {
		return fmt.Errorf("error setting up parent volume watch: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&storagev1alpha1.StoragePool{}, r.ParentCache),
		handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
			storagePool := obj.(*storagev1alpha1.StoragePool)
			list := &storagev1alpha1.VolumeList{}
			if err := r.ParentClient.List(ctx, list, client.MatchingFields{volumeStoragePoolField: storagePool.Name}); err != nil {
				log.Error(err, "Error listing parent storage pools")
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
		return fmt.Errorf("error setting up parent storage pool watch: %w", err)
	}

	if err := c.Watch(
		&source.Kind{Type: &storagev1alpha1.Volume{}},
		&partitionlethandler.EnqueueRequestForParentObject{
			ParentNamespaceAnnotation: partitionletstoragev1alpha1.VolumeParentNamespaceAnnotation,
			ParentNameAnnotation:      partitionletstoragev1alpha1.VolumeParentNameAnnotation,
		},
	); err != nil {
		return fmt.Errorf("error setting up volume watch: %w", err)
	}
	return nil
}
