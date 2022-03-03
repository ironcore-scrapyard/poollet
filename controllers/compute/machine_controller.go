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

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	"github.com/onmetal/controller-utils/metautils"
	commonv1alpha1 "github.com/onmetal/onmetal-api/apis/common/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	partitionletclient "github.com/onmetal/partitionlet/client"
	"github.com/onmetal/partitionlet/controllers/shared"
	partitionlethandler "github.com/onmetal/partitionlet/handler"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/onmetal/controller-utils/conditionutils"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	"github.com/onmetal/onmetal-api/equality"
)

const (
	machineFinalizer           = "partitionlet.onmetal.de/machine"
	machinePurposeLabel        = "partitionlet.onmetal.de/machine-purpose"
	machinePurposeAttachDetach = "attach-detach"
)

func MachineIgnitionNameFromMachineName(machineName string) string {
	return fmt.Sprintf("ignition-%s", machineName)
}

type MachineReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	ParentClient             client.Client
	ParentCache              cache.Cache
	ParentFieldIndexer       client.FieldIndexer
	SharedParentFieldIndexer *clientutils.SharedFieldIndexer

	NamesStrategy             names.Strategy
	MachinePoolName           string
	SourceMachinePoolName     string
	SourceMachinePoolSelector map[string]string
}

//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machines/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=compute.onmetal.de,resources=machineclasses,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *MachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	parentMachine := &computev1alpha1.Machine{}
	if err := r.ParentClient.Get(ctx, req.NamespacedName, parentMachine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, parentMachine)
}

func (r *MachineReconciler) reconcileExists(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine) (ctrl.Result, error) {
	machineKey, err := r.NamesStrategy.Key(parentMachine)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error determining machine key: %w", err)
	}

	log = log.WithValues("MachineKey", machineKey)
	ctx = ctrl.LoggerInto(ctx, log)

	if !parentMachine.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, parentMachine, machineKey)
	}
	return r.reconcile(ctx, log, parentMachine, machineKey)
}

func (r *MachineReconciler) applyIgnition(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine, machineKey client.ObjectKey) (*commonv1alpha1.ConfigMapKeySelector, error) {
	ignitionName := MachineIgnitionNameFromMachineName(machineKey.Name)

	parentIgnitionRef := parentMachine.Spec.Ignition
	if parentIgnitionRef == nil {
		if _, err := clientutils.DeleteIfExists(ctx, r.Client, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: machineKey.Namespace,
				Name:      ignitionName,
			},
		}); err != nil {
			return nil, fmt.Errorf("error deleting ignition: %w", err)
		}
		return nil, nil
	}

	parentIgnition := &corev1.ConfigMap{}
	parentIgnitionKey := client.ObjectKey{Namespace: parentMachine.Namespace, Name: parentIgnitionRef.Name}
	log.V(1).Info("Getting parent ignition", "ParentIgnitionKey", parentIgnitionKey)
	if err := r.ParentClient.Get(ctx, parentIgnitionKey, parentIgnition); err != nil {
		return nil, fmt.Errorf("error getting parent ignition %s: %w", parentIgnitionKey, err)
	}

	log.V(1).Info("Applying ignition")
	ignition := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineKey.Namespace,
			Name:      ignitionName,
		},
	}
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, ignition, func() error {
		ignitionDataKey := computev1alpha1.DefaultIgnitionKey
		if parentIgnitionRef.Key != "" {
			ignitionDataKey = parentIgnitionRef.Key
		}

		ignition.Data = map[string]string{
			ignitionDataKey: parentIgnition.Data[ignitionDataKey],
		}
		return partitionletmeta.SetParentControllerReference(parentMachine, ignition, r.Scheme)
	}); err != nil {
		return nil, fmt.Errorf("error applying ignition: %w", err)
	}

	log.V(1).Info("Successfully applied ignition")
	return &commonv1alpha1.ConfigMapKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: ignitionName},
		Key:                  parentIgnitionRef.Key,
	}, nil

}

func (r *MachineReconciler) reconcile(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine, machineKey client.ObjectKey) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.ParentClient, parentMachine, machineFinalizer)
	if err != nil || modified {
		return ctrl.Result{}, err
	}
	log.V(1).Info("Successfully ensured finalizer")

	ok, err := r.validateMachineClassExists(ctx, log, parentMachine)
	if err != nil || !ok {
		return r.patchParentStatusGetMachineClassIssue(ctx, log, parentMachine, err)
	}

	// We sync the ignition as part of the machine controller instead of as a separate controller for several
	// reasons:
	// * If a machine initially gets created and a separate controller is responsible for syncing the ignition,
	//   the ignition might end up later in the cluster than the machine itself, causing any actual machine
	//   reconciler to error with 'ignition not found'. We want to avoid that case.
	// * Semantically speaking, the ignition is a 'weak entity' of the machine itself. We could also dump the
	//   ignition right into the machine YAML, however, that's not consumer-friendly. As such, we moved it into
	//   a tightly coupled ConfigMap.
	// * Eventually, when we have something like a 'virtual-machinepoollet`, it will directly get the ignition content
	//   as part of its interface and the ignition syncing has to be done within the same step as creating the machine.
	ignitionRef, err := r.applyIgnition(ctx, log, parentMachine, machineKey)
	if err != nil {
		return r.patchParentStatusIgnitionApplyIssue(ctx, log, parentMachine, err)
	}

	attachments, attachmentStates, err := r.applyAttachDetach(ctx, log, parentMachine, machineKey)
	if err != nil {
		return r.patchParentStatusAttachDetachIssue(ctx, log, parentMachine, err)
	}

	machine, err := r.applyMachine(ctx, log, parentMachine, machineKey, ignitionRef, attachments)
	if err != nil {
		return r.patchParentStatusChildApplyIssue(ctx, log, parentMachine, err)
	}

	if err := r.patchMachineStatus(ctx, parentMachine, machine); err != nil {
		return r.patchParentStatusPatchChildStatusIssue(ctx, log, parentMachine, machine, err)
	}

	return r.patchParentStatusSuccessfulSync(ctx, parentMachine, machine, attachmentStates)
}

func (r *MachineReconciler) patchMachineStatus(ctx context.Context, parentMachine *computev1alpha1.Machine, machine *computev1alpha1.Machine) error {
	baseMachine := machine.DeepCopy()
	machine.Status.Interfaces = parentMachine.Status.Interfaces
	if err := r.Status().Patch(ctx, machine, client.MergeFrom(baseMachine)); err != nil {
		return err
	}
	return nil
}

func (r *MachineReconciler) applyMachine(
	ctx context.Context,
	log logr.Logger,
	parentMachine *computev1alpha1.Machine,
	machineKey client.ObjectKey,
	ignitionRef *commonv1alpha1.ConfigMapKeySelector,
	attachments []computev1alpha1.VolumeAttachment,
) (*computev1alpha1.Machine, error) {
	log.V(1).Info("Applying machine")

	machine := &computev1alpha1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineKey.Namespace,
			Name:      machineKey.Name,
		},
	}
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, machine, func() error {
		machine.Spec.Hostname = parentMachine.Spec.Hostname
		machine.Spec.MachineClass = parentMachine.Spec.MachineClass
		machine.Spec.Image = parentMachine.Spec.Image
		machine.Spec.Interfaces = parentMachine.Spec.Interfaces
		machine.Spec.MachinePoolSelector = r.SourceMachinePoolSelector
		machine.Spec.MachinePool = corev1.LocalObjectReference{Name: r.SourceMachinePoolName}
		machine.Spec.Ignition = ignitionRef
		machine.Spec.VolumeAttachments = attachments
		return partitionletmeta.SetParentControllerReference(parentMachine, machine, r.Scheme)
	}); err != nil {
		return nil, fmt.Errorf("error applying machine: %w", err)
	}

	log.V(1).Info("Successfully applied machine")
	return machine, nil
}

func (r *MachineReconciler) listParentControlledAttachDetachVolumeClaims(ctx context.Context, parentMachine *computev1alpha1.Machine, machineKey client.ObjectKey) ([]storagev1alpha1.VolumeClaim, error) {
	volumeClaimList := &storagev1alpha1.VolumeClaimList{}
	err := partitionletclient.ListAndFilterParentControlledBy(ctx, r.Client, parentMachine, volumeClaimList,
		client.InNamespace(machineKey.Namespace),
		client.MatchingLabels{
			machinePurposeLabel: machinePurposeAttachDetach,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error listing attach-detach volume claims for machine %s: %w", machineKey, err)
	}

	return volumeClaimList.Items, nil
}

func (r *MachineReconciler) applyAttachDetach(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine, machineKey client.ObjectKey) ([]computev1alpha1.VolumeAttachment, []computev1alpha1.VolumeAttachmentStatus, error) {
	volumeClaims, err := r.listParentControlledAttachDetachVolumeClaims(ctx, parentMachine, machineKey)
	if err != nil {
		return nil, nil, err
	}

	volumeClaimObjects, err := metautils.ExtractObjectSlice(volumeClaims)
	if err != nil {
		return nil, nil, fmt.Errorf("error extracting object slice: %w", err)
	}

	var (
		attachments      []computev1alpha1.VolumeAttachment
		attachmentStates []computev1alpha1.VolumeAttachmentStatus
	)
	for _, parentAttachment := range parentMachine.Spec.VolumeAttachments {
		if parentAttachment.VolumeClaim == nil {
			continue
		}

		parentVolumeClaim := &storagev1alpha1.VolumeClaim{}
		parentVolumeClaimKey := client.ObjectKey{Namespace: parentMachine.Namespace, Name: parentAttachment.VolumeClaim.Ref.Name}
		log := log.WithValues("ParentVolumeClaimKey", parentVolumeClaimKey)
		log.V(1).Info("Getting parent volume claim")
		if err := r.ParentClient.Get(ctx, parentVolumeClaimKey, parentVolumeClaim); err != nil {
			return nil, nil, fmt.Errorf("error getting parent volume claim %s: %w", parentVolumeClaimKey, err)
		}

		if parentVolumeClaim.Status.Phase != storagev1alpha1.VolumeClaimBound {
			log.V(1).Info("Parent volume claim is not yet bound")
			continue
		}
		log.V(1).Info("Parent volume claim is bound")

		volumeClaim, newVolumeClaims, err := r.createOrUseAndPatchVolumeClaim(ctx, log, parentMachine, parentVolumeClaim, parentVolumeClaimKey, machineKey, volumeClaimObjects, parentAttachment)
		if err != nil {
			return nil, nil, err
		}

		volumeClaimObjects = newVolumeClaims
		attachments = append(attachments, computev1alpha1.VolumeAttachment{
			Name:     parentAttachment.Name,
			Priority: parentAttachment.Priority,
			VolumeAttachmentSource: computev1alpha1.VolumeAttachmentSource{
				VolumeClaim: &computev1alpha1.VolumeClaimAttachmentSource{
					Ref: corev1.LocalObjectReference{Name: volumeClaim.Name},
				},
			},
		})
		attachmentStates = append(attachmentStates, computev1alpha1.VolumeAttachmentStatus{
			Name:     parentAttachment.Name,
			Priority: parentAttachment.Priority,
		})
	}

	for _, outdatedVolumeClaim := range volumeClaimObjects {
		if err := r.Delete(ctx, outdatedVolumeClaim); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Error deleting outdated volume claim")
		}
	}
	return attachments, attachmentStates, nil
}

func (r *MachineReconciler) createOrUseAndPatchVolumeClaim(
	ctx context.Context,
	log logr.Logger,
	parentMachine *computev1alpha1.Machine,
	parentVolumeClaim *storagev1alpha1.VolumeClaim,
	parentVolumeClaimKey client.ObjectKey,
	machineKey client.ObjectKey,
	volumeClaimObjects []client.Object,
	parentAttachment computev1alpha1.VolumeAttachment,
) (*storagev1alpha1.VolumeClaim, []client.Object, error) {
	parentVolume := &storagev1alpha1.Volume{}
	parentVolumeKey := client.ObjectKey{Namespace: parentMachine.Namespace, Name: parentVolumeClaim.Spec.VolumeRef.Name}
	log.V(1).Info("Getting target parent volume", "ParentVolumeKey", parentVolumeKey)
	if err := r.ParentClient.Get(ctx, parentVolumeKey, parentVolume); err != nil {
		return nil, nil, fmt.Errorf("error getting parent volume %s: %w", parentVolumeKey, err)
	}

	log.V(1).Info("Determining target volume key from parent volume", "ParentVolumeKey", parentVolumeKey)
	volumeKey, err := r.NamesStrategy.Key(parentVolume)
	if err != nil {
		return nil, nil, fmt.Errorf("error determining target volume key for parent volume claim %s volume name %s: %w",
			parentVolumeClaimKey,
			parentVolumeClaim.Spec.VolumeRef.Name,
			err,
		)
	}
	log = log.WithValues("VolumeKey", volumeKey)

	desiredVolumeClaimSpec := storagev1alpha1.VolumeClaimSpec{
		VolumeRef:       corev1.LocalObjectReference{Name: volumeKey.Name},
		Resources:       parentVolumeClaim.Spec.Resources,
		StorageClassRef: parentVolumeClaim.Spec.StorageClassRef,
	}
	volumeClaim := &storagev1alpha1.VolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    machineKey.Namespace,
			GenerateName: fmt.Sprintf("machine-%s", machineKey.Name),
			Labels: map[string]string{
				machinePurposeLabel: machinePurposeAttachDetach,
			},
		},
		Spec: desiredVolumeClaimSpec,
	}
	if err := partitionletmeta.SetParentControllerReference(parentMachine, volumeClaim, r.Scheme); err != nil {
		return nil, nil, fmt.Errorf("error setting parent controller reference: %w", err)
	}
	_, newVolumeClaims, err := clientutils.CreateOrUseAndPatch(ctx, r.Client, volumeClaimObjects, volumeClaim,
		func() (bool, error) {
			return equality.Semantic.DeepEqual(volumeClaim.Spec, desiredVolumeClaimSpec), nil
		},
		clientutils.IsOlderThan(volumeClaim),
		nil,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating machine %s volume attachment %s: %w", machineKey, parentAttachment.Name, err)
	}
	return volumeClaim, newVolumeClaims, nil
}

func (r *MachineReconciler) validateMachineClassExists(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine) (bool, error) {
	machineClass := &computev1alpha1.MachineClass{}
	machineClassKey := client.ObjectKey{Name: parentMachine.Spec.MachineClass.Name}
	log.V(1).Info("Getting machine class", "MachineClass", machineClassKey)
	if err := r.Get(ctx, machineClassKey, machineClass); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error getting machine class")
		}
		return false, nil
	}

	log.V(1).Info("Machine class is valid")
	return true, nil
}

func (r *MachineReconciler) patchParentStatus(
	ctx context.Context,
	parentMachine *computev1alpha1.Machine,
	state computev1alpha1.MachineState,
	attachments []computev1alpha1.VolumeAttachmentStatus,
	syncStatus corev1.ConditionStatus,
	syncReason, syncMessage string,
) error {
	base := parentMachine.DeepCopy()
	parentMachine.Status.State = state
	parentMachine.Status.VolumeAttachments = attachments
	conditionutils.MustUpdateSlice(&parentMachine.Status.Conditions, string(computev1alpha1.MachineSynced),
		conditionutils.UpdateStatus(syncStatus),
		conditionutils.UpdateReason(syncReason),
		conditionutils.UpdateMessage(syncMessage),
		conditionutils.UpdateObserved(parentMachine),
	)
	if err := r.ParentClient.Status().Patch(ctx, parentMachine, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error updating parent machine status: %w", err)
	}
	return nil
}

func (r *MachineReconciler) delete(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine, machineKey client.ObjectKey) (ctrl.Result, error) {
	log.V(1).Info("Delete")

	if !controllerutil.ContainsFinalizer(parentMachine, machineFinalizer) {
		log.V(1).Info("Finalizer not present, no cleanup necessary")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Deleting any attach-detach volume claims")
	volumeClaims, err := r.listParentControlledAttachDetachVolumeClaims(ctx, parentMachine, machineKey)
	if err != nil {
		return ctrl.Result{}, err
	}

	volumeClaimObjects, err := metautils.ExtractObjectSlice(volumeClaims)
	if err != nil {
		return ctrl.Result{}, err
	}

	existedVolumeClaims, err := clientutils.DeleteMultipleIfExist(ctx, r.Client, volumeClaimObjects)
	if err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Successfully deleted any attach-detach volume claim", "Existed", existedVolumeClaims)

	log.V(1).Info("Deleting ignition if exists")
	ignitionExisted, err := clientutils.DeleteIfExists(ctx, r.Client, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineKey.Namespace,
			Name:      MachineIgnitionNameFromMachineName(machineKey.Name),
		},
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting machine %s ignition: %w", machineKey, err)
	}

	log.V(1).Info("Successfully deleted ignition (if it existed)", "Existed", ignitionExisted)

	log.V(1).Info("Deleting machine if exists")
	machineExisted, err := clientutils.DeleteIfExists(ctx, r.Client, &computev1alpha1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineKey.Namespace,
			Name:      machineKey.Name,
		},
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting %s: %w", machineKey, err)
	}

	log.V(1).Info("Successfully deleted machine (if it existed)", "Existed", machineExisted)

	if len(existedVolumeClaims) > 0 || ignitionExisted || machineExisted {
		log.V(1).Info("Attach-Detach Volume Claim, Ignition or Machine existed, requeueing",
			"VolumeClaimsExisted", len(existedVolumeClaims) > 0,
			"MachineExisted", machineExisted,
			"IgnitionExisted", ignitionExisted,
		)
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Volume claims, Ignition and Machine gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, parentMachine, machineFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Successfully released finalizer")
	return ctrl.Result{}, nil
}

const (
	machineSpecMachineClassNameField = ".spec.machineClass.name"
	machineSpecIgnitionNameField     = ".spec.ignition.name"
)

func (r *MachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	log := ctrl.Log.WithName("machine-reconciler")
	ctx := ctrl.LoggerInto(context.Background(), log)

	if err := r.ParentFieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, machineSpecMachineClassNameField, func(obj client.Object) []string {
		machine := obj.(*computev1alpha1.Machine)
		return []string{machine.Spec.MachineClass.Name}
	}); err != nil {
		return err
	}

	if err := r.ParentFieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, machineSpecIgnitionNameField, func(object client.Object) []string {
		machine := object.(*computev1alpha1.Machine)
		ignitionRef := machine.Spec.Ignition
		if ignitionRef == nil {
			return nil
		}

		return []string{ignitionRef.Name}
	}); err != nil {
		return err
	}

	c, err := controller.New("machine", mgr, controller.Options{
		Reconciler: r,
		Log:        mgr.GetLogger().WithName("machine"),
	})
	if err != nil {
		return err
	}

	if err := c.Watch(
		source.NewKindWithCache(&computev1alpha1.Machine{}, r.ParentCache),
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			machine := obj.(*computev1alpha1.Machine)
			return machine.Spec.MachinePool.Name == r.MachinePoolName
		}),
		&partitionletpredicate.VolatileConditionFieldsPredicate{},
	); err != nil {
		return err
	}

	if err := c.Watch(
		source.NewKindWithCache(&computev1alpha1.MachineClass{}, r.ParentCache),
		handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
			parentClass := obj.(*computev1alpha1.MachineClass)
			list := &computev1alpha1.MachineList{}
			if err := r.ParentClient.List(ctx, list, client.MatchingFields{machineSpecMachineClassNameField: parentClass.Name}); err != nil {
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
		return err
	}

	if err := c.Watch(source.NewKindWithCache(&corev1.ConfigMap{}, r.ParentCache), handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
		configMap := obj.(*corev1.ConfigMap)
		machineList := &computev1alpha1.MachineList{}
		if err := r.ParentClient.List(ctx, machineList, client.InNamespace(configMap.Namespace), client.MatchingFields{
			machineSpecIgnitionNameField: configMap.Name,
		}); err != nil {
			log.Error(err, "Error listing machines using config map", "ConfigMapKey", client.ObjectKeyFromObject(configMap))
			return nil
		}

		requests := make([]reconcile.Request, 0, len(machineList.Items))
		for _, machine := range machineList.Items {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&machine)})
		}
		return requests
	}), &predicate.GenerationChangedPredicate{}); err != nil {
		return err
	}

	if err := c.Watch(source.NewKindWithCache(&storagev1alpha1.VolumeClaim{}, r.ParentCache), handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
		volumeClaim := &storagev1alpha1.VolumeClaim{}
		if volumeClaim.Status.Phase != storagev1alpha1.VolumeClaimBound {
			return nil
		}

		machineList := &computev1alpha1.MachineList{}
		if err := r.ParentClient.List(ctx, machineList, client.InNamespace(volumeClaim.Namespace), client.MatchingFields{
			shared.MachineSpecVolumeAttachmentsVolumeClaimRefNameField: volumeClaim.Name,
		}); err != nil {
			log.Error(err, "Error listing machines using claim")
			return nil
		}

		reqs := make([]reconcile.Request, 0, len(machineList.Items))
		for _, machine := range machineList.Items {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&machine)})
		}
		return reqs
	})); err != nil {
		return err
	}

	if err := c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &partitionlethandler.EnqueueRequestForParentController{
		OwnerType: &computev1alpha1.Machine{},
	}, &predicate.GenerationChangedPredicate{}); err != nil {
		return err
	}

	if err := c.Watch(&source.Kind{Type: &storagev1alpha1.VolumeClaim{}}, &partitionlethandler.EnqueueRequestForParentController{
		OwnerType: &computev1alpha1.Machine{},
	}, predicate.Funcs{
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			oldVolumeClaim, newVolumeClaim := updateEvent.ObjectOld.(*storagev1alpha1.VolumeClaim), updateEvent.ObjectNew.(*storagev1alpha1.VolumeClaim)
			return !equality.Semantic.DeepEqual(oldVolumeClaim.Spec, newVolumeClaim.Spec) || oldVolumeClaim.Status.Phase != newVolumeClaim.Status.Phase
		},
	}); err != nil {
		return err
	}

	return nil
}

func (r *MachineReconciler) patchParentStatusGetMachineClassIssue(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine, err error) (ctrl.Result, error) {
	if err != nil {
		if err := r.patchParentStatus(ctx, parentMachine, computev1alpha1.MachineStateError, parentMachine.Status.VolumeAttachments, corev1.ConditionFalse, "ErrorGettingMachineClass", fmt.Sprintf("Error getting machine class: %v", err)); err != nil {
			log.Error(err, "Error patching parent machine status")
		}
		return ctrl.Result{}, err
	}
	if err := r.patchParentStatus(ctx, parentMachine, computev1alpha1.MachineStateError, parentMachine.Status.VolumeAttachments, corev1.ConditionFalse, "UnknownMachineClass", fmt.Sprintf("Unknown machine class %s", parentMachine.Spec.MachineClass.Name)); err != nil {
		return ctrl.Result{}, fmt.Errorf("error patching parent status: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *MachineReconciler) patchParentStatusChildApplyIssue(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine, err error) (ctrl.Result, error) {
	if err := r.patchParentStatus(ctx, parentMachine, computev1alpha1.MachineStateError, parentMachine.Status.VolumeAttachments, corev1.ConditionFalse, "ErrorApplying", fmt.Sprintf("Applying resulted in an unexpected error: %v", err)); err != nil {
		log.Error(err, "Could not patch parent status")
	}
	return ctrl.Result{}, err
}

func (r *MachineReconciler) patchParentStatusPatchChildStatusIssue(ctx context.Context, log logr.Logger, parentMachine, machine *computev1alpha1.Machine, err error) (ctrl.Result, error) {
	if err := r.patchParentStatus(ctx, parentMachine, machine.Status.State, parentMachine.Status.VolumeAttachments, corev1.ConditionFalse, "ErrorPatchingStatus", fmt.Sprintf("Patching machine status resulted in an unexpected error: %v", err)); err != nil {
		log.Error(err, "Could not patch parent status")
	}
	return ctrl.Result{}, err
}

func (r *MachineReconciler) patchParentStatusSuccessfulSync(ctx context.Context, parentMachine, machine *computev1alpha1.Machine, attachmentStates []computev1alpha1.VolumeAttachmentStatus) (ctrl.Result, error) {
	if err := r.patchParentStatus(ctx, parentMachine, machine.Status.State, attachmentStates, corev1.ConditionTrue, "Synced", "The machine has been successfully synced."); err != nil {
		return ctrl.Result{}, fmt.Errorf("error patching parent status: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *MachineReconciler) patchParentStatusIgnitionApplyIssue(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine, err error) (ctrl.Result, error) {
	if err := r.patchParentStatus(ctx, parentMachine, computev1alpha1.MachineStateError, parentMachine.Status.VolumeAttachments, corev1.ConditionFalse, "ErrorApplyingIgnition", fmt.Sprintf("Applying the ignition resulted in an error: %v", err)); err != nil {
		log.Error(err, "Could not patch parent status")
	}
	return ctrl.Result{}, err
}

func (r *MachineReconciler) patchParentStatusAttachDetachIssue(ctx context.Context, log logr.Logger, parentMachine *computev1alpha1.Machine, err error) (ctrl.Result, error) {
	if err := r.patchParentStatus(ctx, parentMachine, computev1alpha1.MachineStateError, parentMachine.Status.VolumeAttachments, corev1.ConditionFalse, "AttachDetachError", fmt.Sprintf("Attach / Detach resulted in an error: %v", err)); err != nil {
		log.Error(err, "Could not patch parent status")
	}
	return ctrl.Result{}, err
}
