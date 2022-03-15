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
	"github.com/onmetal/controller-utils/clientutils"
	"github.com/onmetal/controller-utils/metautils"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	"github.com/onmetal/onmetal-api/equality"
	"github.com/onmetal/partitionlet/controllers/shared"
	partitionletmeta "github.com/onmetal/partitionlet/meta"
	"github.com/onmetal/partitionlet/names"
	partitionletpredicate "github.com/onmetal/partitionlet/predicate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	partitionlethandler "github.com/onmetal/partitionlet/handler"
)

const (
	volumeFinalizer           = "partitionlet.onmetal.de/volume"
	volumePurposeLabel        = "partitionlet.onmetal.de/volume-purpose"
	volumePurposeAccessSecret = "access-secret"
)

type VolumeReconciler struct {
	client.Client
	FieldIndexer client.FieldIndexer
	Scheme       *runtime.Scheme

	ParentClient             client.Client
	ParentCache              cache.Cache
	ParentFieldIndexer       client.FieldIndexer
	SharedParentFieldIndexer *clientutils.SharedFieldIndexer

	NamesStrategy names.Strategy

	StoragePoolName string
	MachinePoolName string

	SourceStoragePoolName     string
	SourceStoragePoolSelector map[string]string
}

//+kubebuilder:rbac:groups=storage.onmetal.de,resources=volumes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.onmetal.de,resources=volumes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.onmetal.de,resources=volumes/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=storage.onmetal.de,resources=storagepools,verbs=get;list;watch
//+kubebuilder:rbac:groups=storage.onmetal.de,resources=storageclasses,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *VolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	volume := &storagev1alpha1.Volume{}
	if err := r.ParentClient.Get(ctx, req.NamespacedName, volume); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.reconcileExists(ctx, log, volume)
}

func (r *VolumeReconciler) reconcileExists(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume) (ctrl.Result, error) {
	volumeKey, err := r.NamesStrategy.Key(parentVolume)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error determining volume key: %w", err)
	}

	log = log.WithValues("VolumeKey", volumeKey)
	ctx = ctrl.LoggerInto(ctx, log)

	if !parentVolume.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, parentVolume, volumeKey)
	}

	used, err := r.isParentUsed(ctx, log, parentVolume)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error determining whether parent is used: %w", err)
	}

	if !used {
		log.V(1).Info("Parent is not used, continuing with deletion flow")
		return r.delete(ctx, log, parentVolume, volumeKey)
	}

	log.V(1).Info("Parent is used, continuing with reconciliation flow")
	return r.reconcile(ctx, log, parentVolume, volumeKey)
}

func (r *VolumeReconciler) isParentUsed(ctx context.Context, log logr.Logger, parent *storagev1alpha1.Volume) (bool, error) {
	// If we are the storage pool of a volume, sync it.
	if r.StoragePoolName != "" {
		used, err := r.isParentUsedViaStoragePool(ctx, parent)
		if err != nil {
			return false, err
		}

		log.V(2).Info("Parent used via storage pool", "Used", used)
		if used {
			return used, nil
		}
	}

	// If we started without a machine pool name, we are not monitoring for volumes scheduled
	// onto machines assigned to our machine pool.
	if r.MachinePoolName != "" {
		used, err := r.isParentUsedViaMachinePool(ctx, parent)
		if err != nil {
			return false, err
		}

		log.V(2).Info("Parent used via machine pool", "Used", used)
		if used {
			return used, nil
		}
	}

	return false, nil
}

func (r *VolumeReconciler) isParentUsedViaStoragePool(ctx context.Context, parent *storagev1alpha1.Volume) (bool, error) {
	return parent.Spec.StoragePool.Name == r.StoragePoolName, nil
}

func (r *VolumeReconciler) isParentUsedViaMachinePool(ctx context.Context, parent *storagev1alpha1.Volume) (bool, error) {
	// Check if the volume is on a machine that's scheduled onto us.
	claimName := parent.Spec.ClaimRef.Name
	if claimName == "" {
		return false, nil
	}

	machineList := &computev1alpha1.MachineList{}
	err := r.ParentClient.List(ctx, machineList,
		client.InNamespace(parent.Namespace),
		client.Limit(1),
		client.MatchingFields{
			shared.MachineSpecVolumeAttachmentsVolumeClaimRefNameField: claimName,
		},
	)
	if err != nil {
		return false, fmt.Errorf("error listing machines using claim using volume %s: %w", client.ObjectKeyFromObject(parent), err)
	}
	return len(machineList.Items) > 0, nil
}

func (r *VolumeReconciler) delete(ctx context.Context, log logr.Logger, parent *storagev1alpha1.Volume, volumeKey client.ObjectKey) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(parent, volumeFinalizer) {
		log.V(1).Info("Volume contains no finalizer, no deletion needs to be done")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Deleting object")
	existed, err := clientutils.DeleteIfExists(ctx, r.Client, &storagev1alpha1.Volume{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: volumeKey.Namespace,
			Name:      volumeKey.Name,
		},
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting %s: %w", volumeKey, err)
	}

	if existed {
		log.V(1).Info("Object is not yet gone, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Object is gone, removing finalizer on parent object")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.ParentClient, parent, volumeFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Successfully removed finalizer")
	return ctrl.Result{}, nil
}

func (r *VolumeReconciler) patchParentStatus(
	ctx context.Context,
	parentVolume *storagev1alpha1.Volume,
	parentAccess *storagev1alpha1.VolumeAccess,
	state storagev1alpha1.VolumeState,
	syncCondition storagev1alpha1.VolumeCondition,
) error {
	if r.StoragePoolName == "" || parentVolume.Spec.StoragePool.Name != r.StoragePoolName {
		return nil
	}
	baseParent := parentVolume.DeepCopy()
	parentVolume.Status.Access = parentAccess
	parentVolume.Status.State = state
	conditionutils.MustUpdateSlice(&parentVolume.Status.Conditions, string(storagev1alpha1.VolumeSynced),
		conditionutils.UpdateStatus(syncCondition.Status),
		conditionutils.UpdateReason(syncCondition.Reason),
		conditionutils.UpdateMessage(syncCondition.Message),
		conditionutils.UpdateObserved(parentVolume),
	)
	if err := r.ParentClient.Status().Patch(ctx, parentVolume, client.MergeFrom(baseParent)); err != nil {
		return fmt.Errorf("error patching parent status: %w", err)
	}
	return nil
}

func (r *VolumeReconciler) reconcile(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume, volumeKey client.ObjectKey) (ctrl.Result, error) {
	log.V(1).Info("Reconciling")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.ParentClient, parentVolume, volumeFinalizer)
	if err != nil || modified {
		return ctrl.Result{}, err
	}

	ok, err := r.getStorageClass(ctx, log, parentVolume)
	if err != nil || !ok {
		return r.patchParentStatusStorageClassIssue(ctx, log, parentVolume, err)
	}

	volume, err := r.applyVolume(ctx, log, parentVolume, volumeKey)
	if err != nil {
		return r.patchParentStatusApplyIssue(ctx, log, parentVolume, err)
	}

	parentAccess, err := r.applyParentAccessSecret(ctx, log, parentVolume, volume, volumeKey)
	if err != nil {
		return r.patchParentStatusAccessSecretApplyIssue(ctx, log, parentVolume, err)
	}

	return r.patchParentStatusSuccessfulSync(ctx, log, parentVolume, volume, parentAccess)
}

func (r *VolumeReconciler) patchParentStatusSuccessfulSync(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume, volume *storagev1alpha1.Volume, parentAccess *storagev1alpha1.VolumeAccess) (ctrl.Result, error) {
	if err := r.patchParentStatus(
		ctx,
		parentVolume,
		parentAccess,
		volume.Status.State,
		storagev1alpha1.VolumeCondition{
			Status:  corev1.ConditionTrue,
			Reason:  "Synced",
			Message: "Successfully synced volume.",
		},
	); err != nil {
		log.Error(err, "Error patching parent status")
		return ctrl.Result{}, fmt.Errorf("error patching parent status")
	}
	return ctrl.Result{}, nil
}

func (r *VolumeReconciler) patchParentStatusApplyIssue(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume, err error) (ctrl.Result, error) {
	if err := r.patchParentStatus(
		ctx,
		parentVolume,
		parentVolume.Status.Access,
		storagev1alpha1.VolumeStateError,
		storagev1alpha1.VolumeCondition{
			Status:  corev1.ConditionFalse,
			Reason:  "ApplyFailed",
			Message: fmt.Sprintf("Applying the volume resulted in an error: %v", err),
		},
	); err != nil {
		log.Error(err, "Error patching parent status")
	}
	return ctrl.Result{}, fmt.Errorf("error applying volume: %w", err)
}

func (r *VolumeReconciler) applyVolume(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume, volumeKey client.ObjectKey) (*storagev1alpha1.Volume, error) {
	log.V(1).Info("Applying volume")
	volume := &storagev1alpha1.Volume{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: volumeKey.Namespace,
			Name:      volumeKey.Name,
		},
		Spec: storagev1alpha1.VolumeSpec{
			StorageClassRef:     parentVolume.Spec.StorageClassRef,
			StoragePoolSelector: r.SourceStoragePoolSelector,
			StoragePool: corev1.LocalObjectReference{
				Name: r.SourceStoragePoolName,
			},
			Resources:   parentVolume.Spec.Resources,
			Tolerations: nil, // TODO: Translate tolerations
		},
	}
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, volume, func() error {
		// TODO: Translate tolerations
		volume.Spec.StorageClassRef = parentVolume.Spec.StorageClassRef
		volume.Spec.StoragePoolSelector = r.SourceStoragePoolSelector
		volume.Spec.Resources = parentVolume.Spec.Resources
		if volume.Spec.StoragePool.Name == "" {
			volume.Spec.StoragePool = corev1.LocalObjectReference{
				Name: r.SourceStoragePoolName,
			}
		}
		return partitionletmeta.SetParentControllerReference(parentVolume, volume, r.Scheme)
	}); err != nil {
		return nil, fmt.Errorf("error applying volume %s: %w", volumeKey, err)
	}

	log.V(1).Info("Successfully applied volume")
	return volume, nil
}

func (r *VolumeReconciler) patchParentStatusStorageClassIssue(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume, err error) (ctrl.Result, error) {
	if err != nil {
		if err := r.patchParentStatus(
			ctx,
			parentVolume,
			parentVolume.Status.Access,
			storagev1alpha1.VolumeStateError,
			storagev1alpha1.VolumeCondition{
				Status:  corev1.ConditionFalse,
				Reason:  "StorageClassError",
				Message: fmt.Sprintf("Error validating storage class: %v", err),
			},
		); err != nil {
			log.Error(err, "Error patching parent status")
		}
		return ctrl.Result{}, err
	}
	if err := r.patchParentStatus(
		ctx,
		parentVolume,
		parentVolume.Status.Access,
		storagev1alpha1.VolumeStateError,
		storagev1alpha1.VolumeCondition{
			Status:  corev1.ConditionFalse,
			Message: "UnsupportedStorageClass",
			Reason:  fmt.Sprintf("Storage class %q is not supported", parentVolume.Spec.StorageClassRef.Name),
		},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("error patching parent status: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *VolumeReconciler) getStorageClass(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume) (bool, error) {
	storageClass := &storagev1alpha1.StorageClass{}
	storageClassKey := client.ObjectKey{Name: parentVolume.Spec.StorageClassRef.Name}
	log = log.WithValues("StorageClass", storageClassKey)
	log.V(1).Info("Getting storage class")
	if err := r.Get(ctx, storageClassKey, storageClass); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error getting storage class %s: %w", storageClassKey, err)
		}
		return false, nil
	}
	log.V(1).Info("Successfully got storage class")
	return true, nil
}

func (r *VolumeReconciler) applyParentAccessSecret(ctx context.Context, log logr.Logger, parentVolume, volume *storagev1alpha1.Volume, volumeKey client.ObjectKey) (*storagev1alpha1.VolumeAccess, error) {
	volumeAccess := volume.Status.Access
	if volumeAccess == nil {
		log.V(1).Info("Volume does not provide any access yet.")
		return nil, nil
	}
	log.V(1).Info("Volume has access information")

	accessSecretKey := client.ObjectKey{Namespace: volumeKey.Namespace, Name: volumeAccess.SecretRef.Name}
	accessSecret := &corev1.Secret{}
	log.V(1).Info("Getting access secret", "AccessSecretKey", accessSecretKey)
	if err := r.Get(ctx, accessSecretKey, accessSecret); err != nil {
		return nil, fmt.Errorf("error getting access secret %s: %w", accessSecretKey, err)
	}

	log.V(1).Info("Listing managed secrets in parent cluster.")
	secretList := &corev1.SecretList{}
	err := clientutils.ListAndFilterControlledBy(ctx, r.ParentClient, parentVolume, secretList,
		client.InNamespace(parentVolume.Namespace),
		client.MatchingLabels{
			volumePurposeLabel: volumePurposeAccessSecret,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error listing parent volume access secrets: %w", err)
	}

	secretObjects, err := metautils.ExtractObjectSlice(secretList.Items)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Applying access secret in parent cluster")
	parentAccessSecret := &corev1.Secret{}
	_, deprecatedAccessSecrets, err := clientutils.CreateOrUseAndPatch(ctx, r.ParentClient, secretObjects, parentAccessSecret,
		func() (bool, error) {
			return equality.Semantic.DeepEqual(accessSecret.Data, parentAccessSecret.Data), nil
		},
		clientutils.IsOlderThan(parentAccessSecret),
		func() error {
			if parentAccessSecret.ResourceVersion != "" {
				return nil
			}
			parentAccessSecret.ObjectMeta = metav1.ObjectMeta{
				Namespace:    parentVolume.Namespace,
				GenerateName: fmt.Sprintf("%s-access-", parentVolume.Name),
				Labels: map[string]string{
					volumePurposeLabel: volumePurposeAccessSecret,
				},
			}
			parentAccessSecret.Data = accessSecret.Data
			return controllerutil.SetControllerReference(parentVolume, parentAccessSecret, r.Scheme)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error applying access secret: %w", err)
	}
	for _, deprecatedAccessSecret := range deprecatedAccessSecrets {
		if err := r.ParentClient.Delete(ctx, deprecatedAccessSecret); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Error deleting deprecated parent access secret",
				"DeprecatedAccessSecretKey", client.ObjectKeyFromObject(deprecatedAccessSecret),
			)
		}
	}
	return &storagev1alpha1.VolumeAccess{
		SecretRef:        corev1.LocalObjectReference{Name: parentAccessSecret.Name},
		Driver:           volumeAccess.Driver,
		VolumeAttributes: volumeAccess.VolumeAttributes,
	}, nil
}

func (r *VolumeReconciler) patchParentStatusAccessSecretApplyIssue(ctx context.Context, log logr.Logger, parentVolume *storagev1alpha1.Volume, err error) (ctrl.Result, error) {
	if err := r.patchParentStatus(
		ctx,
		parentVolume,
		parentVolume.Status.Access,
		storagev1alpha1.VolumeStateError,
		storagev1alpha1.VolumeCondition{
			Status:  corev1.ConditionFalse,
			Reason:  "ErrorApplyingAccessSecret",
			Message: fmt.Sprintf("Error applying access secret: %v", err),
		},
	); err != nil {
		log.Error(err, "Error patching parent status")
	}
	return ctrl.Result{}, err
}

const (
	volumeSpecStoragePoolNameField  = ".spec.storagePool.name"
	volumeSpecClaimRefNameField     = ".spec.claimRef.name"
	volumeStatusAccessSecretRefName = ".status.access.secretRef.name"
)

func (r *VolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	log := ctrl.Log.WithName("volume-reconciler")
	ctx := ctrl.LoggerInto(context.Background(), log)

	if r.StoragePoolName != "" {
		if err := r.ParentFieldIndexer.IndexField(ctx, &storagev1alpha1.Volume{}, volumeSpecStoragePoolNameField, func(obj client.Object) []string {
			volume := obj.(*storagev1alpha1.Volume)
			return []string{volume.Spec.StoragePool.Name}
		}); err != nil {
			return fmt.Errorf("error setting up %s indexer: %w", volumeSpecStoragePoolNameField, err)
		}
	}

	if r.MachinePoolName != "" {
		if err := r.SharedParentFieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, shared.MachineSpecVolumeAttachmentsVolumeClaimRefNameField); err != nil {
			return err
		}

		if err := r.ParentFieldIndexer.IndexField(ctx, &storagev1alpha1.Volume{}, volumeSpecClaimRefNameField, func(object client.Object) []string {
			volume := object.(*storagev1alpha1.Volume)
			return []string{volume.Spec.ClaimRef.Name}
		}); err != nil {
			return err
		}
	}

	if err := r.FieldIndexer.IndexField(ctx, &storagev1alpha1.Volume{}, volumeStatusAccessSecretRefName, func(obj client.Object) []string {
		volume := obj.(*storagev1alpha1.Volume)
		access := volume.Status.Access
		if access == nil || access.SecretRef.Name == "" {
			return nil
		}

		return []string{access.SecretRef.Name}
	}); err != nil {
		return err
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
		&partitionletpredicate.VolatileConditionFieldsPredicate{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			parentVolume := obj.(*storagev1alpha1.Volume)

			key, err := r.NamesStrategy.Key(parentVolume)
			if err != nil {
				log.Error(err, "Error constructing target key")
				return false
			}

			err = r.Get(ctx, key, &storagev1alpha1.Volume{})
			if err != nil && !apierrors.IsNotFound(err) {
				log.Error(err, "Error getting target object")
				return false
			}
			if err == nil {
				log.V(1).Info("Target object exists, reconciliation required.")
				return true
			}

			used, err := r.isParentUsed(ctx, log, parentVolume)
			if err != nil {
				log.Error(err, "Error determining whether parent is used")
				return false
			}

			log.V(1).Info("Used", "Used", used)
			return used
		}),
	); err != nil {
		return fmt.Errorf("error setting up parent volume watch: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&corev1.Secret{}, r.ParentCache),
		&handler.EnqueueRequestForOwner{OwnerType: &storagev1alpha1.Volume{}},
		&predicate.GenerationChangedPredicate{},
	); err != nil {
		return err
	}

	if r.StoragePoolName != "" {
		if err := c.Watch(
			source.NewKindWithCache(&storagev1alpha1.StoragePool{}, r.ParentCache),
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
				storagePool := obj.(*storagev1alpha1.StoragePool)
				list := &storagev1alpha1.VolumeList{}
				if err := r.ParentClient.List(ctx, list, client.MatchingFields{volumeSpecStoragePoolNameField: storagePool.Name}); err != nil {
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
	}

	if r.MachinePoolName != "" {
		if err := c.Watch(
			source.NewKindWithCache(&computev1alpha1.Machine{}, r.ParentCache),
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
				machine := obj.(*computev1alpha1.Machine)
				if machine.Spec.MachinePool.Name != r.MachinePoolName {
					return nil
				}

				keys := make(map[client.ObjectKey]struct{})
				for _, attachment := range machine.Spec.VolumeAttachments {
					if volumeClaim := attachment.VolumeClaim; volumeClaim != nil {
						volumeList := &storagev1alpha1.VolumeList{}
						if err := r.ParentClient.List(ctx, volumeList,
							client.InNamespace(machine.Namespace),
							client.MatchingFields{
								volumeSpecClaimRefNameField: attachment.VolumeClaim.Ref.Name,
							},
						); err != nil {
							log.Error(err, "Error listing volumes for claim", "Claim", attachment.VolumeClaim.Ref.Name)
							return nil
						}

						for _, volume := range volumeList.Items {
							keys[client.ObjectKeyFromObject(&volume)] = struct{}{}
						}
					}
				}

				res := make([]reconcile.Request, 0, len(keys))
				for key := range keys {
					res = append(res, reconcile.Request{NamespacedName: key})
				}
				return res
			}),
			&partitionletpredicate.VolatileConditionFieldsPredicate{},
		); err != nil {
			return fmt.Errorf("error setting up parent machine watch: %w", err)
		}
	}

	if err := c.Watch(
		&source.Kind{Type: &storagev1alpha1.Volume{}},
		&partitionlethandler.EnqueueRequestForParentController{
			OwnerType: &storagev1alpha1.Volume{},
		},
		&partitionletpredicate.VolatileConditionFieldsPredicate{},
	); err != nil {
		return fmt.Errorf("error setting up volume watch: %w", err)
	}

	if err := c.Watch(
		&source.Kind{Type: &corev1.Secret{}},
		handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
			secret := obj.(*corev1.Secret)
			log := log.WithValues("SecretKey", client.ObjectKeyFromObject(secret))
			volumeList := &storagev1alpha1.VolumeList{}
			if err := r.List(ctx, volumeList,
				client.InNamespace(secret.Namespace),
				client.MatchingFields{
					volumeStatusAccessSecretRefName: secret.Name,
				},
			); err != nil {
				log.Error(err, "Error listing volumes using secret")
				return nil
			}

			reqs := make([]reconcile.Request, 0, len(volumeList.Items))
			for _, volume := range volumeList.Items {
				reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&volume)})
			}
			return reqs
		}),
		&predicate.GenerationChangedPredicate{},
	); err != nil {
		return err
	}

	return nil
}
