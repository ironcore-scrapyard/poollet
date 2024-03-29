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
	"github.com/onmetal/controller-utils/metautils"
	commonv1alpha1 "github.com/onmetal/onmetal-api/apis/common/v1alpha1"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	brokerclient "github.com/onmetal/poollet/broker/client"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	"github.com/onmetal/poollet/broker/provider"
	"github.com/onmetal/poollet/broker/sync"
	poolletclient "github.com/onmetal/poollet/client"
	"github.com/onmetal/poollet/multicluster/controllerutil"
	mcmeta "github.com/onmetal/poollet/multicluster/meta"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VolumeApplier interface {
	ApplyTarget(ctx context.Context, volume *storagev1alpha1.Volume) (*storagev1alpha1.Volume, bool, error)
	GetTarget(ctx context.Context, volume *storagev1alpha1.Volume) (*storagev1alpha1.Volume, error)
	DeleteTarget(ctx context.Context, volume *storagev1alpha1.Volume) (done bool, err error)
}

type ProxyVolumeApplier struct {
	TargetClient client.Client
	ClusterName  string
	Scheme       *runtime.Scheme
}

var errNoBrokerController = fmt.Errorf("volume does not have broker controller set")

func (r *ProxyVolumeApplier) ApplyTarget(ctx context.Context, volume *storagev1alpha1.Volume) (*storagev1alpha1.Volume, bool, error) {
	brokerCtrl := mcmeta.GetControllerOf(volume)
	if brokerCtrl == nil {
		return nil, false, errNoBrokerController
	}

	target := &storagev1alpha1.Volume{}
	targetKey := client.ObjectKey{
		Namespace: brokerCtrl.Namespace,
		Name:      brokerCtrl.Name,
	}

	if err := r.TargetClient.Get(ctx, targetKey, target); err != nil {
		return nil, false, fmt.Errorf("error getting brokered target %s: %w", targetKey, err)
	}

	baseTarget := target.DeepCopy()
	if err := controllerutil.SetOwnerReference(
		r.ClusterName,
		volume,
		target,
		r.Scheme,
	); err != nil {
		return nil, false, fmt.Errorf("error setting target %s broker owner reference: %w", targetKey, err)
	}
	if err := r.TargetClient.Patch(ctx, target, client.MergeFrom(baseTarget)); err != nil {
		return nil, false, fmt.Errorf("error patching target %s: %w", targetKey, err)
	}

	return target, false, nil
}

func (r *ProxyVolumeApplier) GetTarget(ctx context.Context, volume *storagev1alpha1.Volume) (*storagev1alpha1.Volume, error) {
	brokerCtrl := mcmeta.GetControllerOf(volume)
	if brokerCtrl == nil {
		return nil, errNoBrokerController
	}

	target := &storagev1alpha1.Volume{}
	targetKey := client.ObjectKey{
		Namespace: brokerCtrl.Namespace,
		Name:      brokerCtrl.Name,
	}

	if err := r.TargetClient.Get(ctx, targetKey, target); err != nil {
		return nil, err
	}

	ok, err := controllerutil.HasOwnerReference(
		r.ClusterName,
		volume,
		target,
		r.Scheme,
	)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, brokererrors.NewNotSynced(storagev1alpha1.Resource("volumes"), volume.Name)
	}
	return target, nil
}

func (r *ProxyVolumeApplier) DeleteTarget(ctx context.Context, volume *storagev1alpha1.Volume) (done bool, err error) {
	brokerCtrl := mcmeta.GetControllerOf(volume)
	if brokerCtrl == nil {
		return false, errNoBrokerController
	}

	target := &storagev1alpha1.Volume{}
	targetKey := client.ObjectKey{
		Namespace: brokerCtrl.Namespace,
		Name:      brokerCtrl.Name,
	}
	if err := r.TargetClient.Get(ctx, targetKey, target); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error getting target %s: %w", targetKey, err)
		}
		return true, nil
	}

	baseTarget := target.DeepCopy()
	if err := controllerutil.RemoveOwnerReference(
		r.ClusterName,
		volume,
		target,
		r.Scheme,
	); err != nil {
		return false, fmt.Errorf("error removing target %s owner reference: %w", targetKey, err)
	}
	if err := r.TargetClient.Patch(ctx, target, client.MergeFrom(baseTarget)); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error patching target %s: %w", targetKey, err)
		}
		return true, nil
	}
	return true, nil
}

type SyncVolumeApplier struct {
	Provider         provider.Provider
	TargetPoolName   string
	TargetPoolLabels map[string]string
	ClusterName      string

	Unclaimable bool

	TargetClient client.Client
}

func (r *SyncVolumeApplier) registerImagePullSecretMutation(ctx context.Context, volume, target *storagev1alpha1.Volume, b *sync.CompositeMutationBuilder) error {
	imagePullSecretRef := volume.Spec.ImagePullSecretRef
	if imagePullSecretRef == nil {
		b.Add(func() {
			target.Spec.ImagePullSecretRef = nil
		})
		return nil
	}

	targetImagePullSecret := &corev1.Secret{}
	if err := r.Provider.Target(ctx, client.ObjectKey{Namespace: volume.Namespace, Name: imagePullSecretRef.Name}, targetImagePullSecret); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting target image pull secret key: %w", err)
		}
		b.PartialSync = true
		return nil
	}

	b.Add(func() {
		target.Spec.ImagePullSecretRef = &corev1.LocalObjectReference{Name: targetImagePullSecret.Name}
	})
	return nil
}

func (r *SyncVolumeApplier) registerClaimMutation(ctx context.Context, volume, target *storagev1alpha1.Volume, b *sync.CompositeMutationBuilder) error {
	if r.Unclaimable {
		b.Add(func() {
			target.Spec.ClaimRef = nil
			target.Spec.Unclaimable = true
		})
		return nil
	}

	claimRef := volume.Spec.ClaimRef
	if claimRef == nil {
		b.Add(func() {
			target.Spec.ClaimRef = nil
		})
		return nil
	}

	machineKey := client.ObjectKey{Namespace: volume.Namespace, Name: claimRef.Name}
	targetMachine := &computev1alpha1.Machine{}
	if err := r.Provider.Target(ctx, machineKey, targetMachine); err != nil {
		if !brokererrors.IsNotSyncedOrNotFound(err) {
			return fmt.Errorf("error getting machine %s target: %w", machineKey, err)
		}
		// Since we don't depend on a machine to be synced yet, we just set it to empty.
		b.Add(func() {
			target.Spec.ClaimRef = nil
		})
		return nil
	}

	b.Add(func() {
		target.Spec.ClaimRef = &commonv1alpha1.LocalUIDReference{
			Name: targetMachine.Name,
			UID:  targetMachine.UID,
		}
	})
	return nil
}

func (r *SyncVolumeApplier) registerDefaultMutation(ctx context.Context, volume, target *storagev1alpha1.Volume, b *sync.CompositeMutationBuilder) error {
	b.Add(func() {
		target.Spec.VolumeClassRef = volume.Spec.VolumeClassRef
		target.Spec.VolumePoolSelector = r.TargetPoolLabels
		if r.TargetPoolName != "" && target.Spec.VolumePoolRef == nil {
			target.Spec.VolumePoolRef = &corev1.LocalObjectReference{Name: r.TargetPoolName}
		}
		target.Spec.Resources = volume.Spec.Resources
		target.Spec.Image = volume.Spec.Image
	})
	return nil
}

func (r *SyncVolumeApplier) ApplyTarget(ctx context.Context, volume *storagev1alpha1.Volume) (*storagev1alpha1.Volume, bool, error) {
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, volume)
	if err != nil {
		return nil, false, err
	}

	var (
		target = &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace,
				Name:      volume.Name,
			},
		}
		b sync.CompositeMutationBuilder
	)
	if err := r.registerImagePullSecretMutation(ctx, volume, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerDefaultMutation(ctx, volume, target, &b); err != nil {
		return nil, false, err
	}
	if err := r.registerClaimMutation(ctx, volume, target, &b); err != nil {
		return nil, false, err
	}

	if _, err := brokerclient.BrokerControlledCreateOrPatch(
		ctx,
		r.TargetClient,
		r.ClusterName,
		volume,
		target,
		b.Mutate(target),
	); err != nil {
		return nil, b.PartialSync, sync.IgnorePartialCreate(err)
	}
	return target, b.PartialSync, nil
}

func (r *SyncVolumeApplier) GetTarget(ctx context.Context, volume *storagev1alpha1.Volume) (*storagev1alpha1.Volume, error) {
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, volume)
	if err != nil {
		return nil, err
	}

	target := &storagev1alpha1.Volume{}
	targetKey := client.ObjectKey{Namespace: targetNamespace, Name: volume.Name}
	if err := r.TargetClient.Get(ctx, targetKey, target); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting target volume %s: %w", targetKey, err)
		}
		return nil, brokererrors.NewNotSynced(storagev1alpha1.Resource("volumes"), volume.Name)
	}
	return target, nil
}

func (r *SyncVolumeApplier) DeleteTarget(ctx context.Context, volume *storagev1alpha1.Volume) (done bool, err error) {
	log := ctrl.LoggerFrom(ctx)

	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, volume)
	if err != nil {
		return true, brokererrors.IgnoreNotSynced(err)
	}

	log.V(1).Info("Deleting target if exists")
	existed, err := clientutils.DeleteIfExists(ctx, r.TargetClient, &storagev1alpha1.Volume{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      volume.Name,
		},
	})
	if err != nil {
		return false, fmt.Errorf("error deleting volume: %w", err)
	}
	return !existed, nil
}

type AccessApplier struct {
	Domain domain.Domain

	APIReader    client.Reader
	Client       client.Client
	TargetClient client.Client
}

func (r *AccessApplier) accessVolumeUIDLabel() string {
	return r.Domain.Slash("access-volume-uid")
}

func (r *AccessApplier) ApplyAccess(ctx context.Context, log logr.Logger, volume, targetVolume *storagev1alpha1.Volume) (*storagev1alpha1.VolumeAccess, error) {
	access := targetVolume.Status.Access

	if access == nil || access.SecretRef == nil {
		if err := poolletclient.ControlledListSingleAndDelete(ctx, r.APIReader, r.Client, volume, &corev1.Secret{},
			client.InNamespace(volume.Namespace),
			client.MatchingLabels{
				r.accessVolumeUIDLabel(): string(volume.UID),
			},
		); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Error pruning access secret")
		}
		if access == nil {
			return nil, nil
		}
		return &storagev1alpha1.VolumeAccess{
			Driver:           access.Driver,
			VolumeAttributes: access.VolumeAttributes,
		}, nil
	}

	targetAccessSecret := &corev1.Secret{}
	targetAccessSecretKey := client.ObjectKey{Namespace: targetVolume.Namespace, Name: access.SecretRef.Name}
	if err := r.TargetClient.Get(ctx, targetAccessSecretKey, targetAccessSecret); err != nil {
		return nil, fmt.Errorf("error getting target access secret %s: %w", targetAccessSecretKey, err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    volume.Namespace,
			GenerateName: fmt.Sprintf("%s-access-", volume.Name),
		},
	}
	if _, err := poolletclient.ControlledListSingleGenerateOrPatch(ctx, r.APIReader, r.Client, volume, secret, func() error {
		metautils.SetLabel(secret, r.accessVolumeUIDLabel(), string(volume.UID))
		secret.Data = targetAccessSecret.Data
		return nil
	}, client.MatchingLabels{
		r.accessVolumeUIDLabel(): string(volume.UID),
	}); err != nil {
		return nil, fmt.Errorf("error generating / patching access secret: %w", err)
	}

	return &storagev1alpha1.VolumeAccess{
		SecretRef:        &corev1.LocalObjectReference{Name: secret.Name},
		Driver:           access.Driver,
		VolumeAttributes: access.VolumeAttributes,
	}, nil
}

func PatchVolumeStatus(ctx context.Context, c client.Client, volume *storagev1alpha1.Volume, state storagev1alpha1.VolumeState, access *storagev1alpha1.VolumeAccess) error {
	base := volume.DeepCopy()
	now := metav1.Now()
	if volume.Status.State != state {
		volume.Status.LastStateTransitionTime = &now
	}
	volume.Status.State = state
	volume.Status.Access = access
	if err := c.Status().Patch(ctx, volume, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching volume status: %w", err)
	}
	return nil
}
