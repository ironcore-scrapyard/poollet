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

package dependent

import (
	"context"

	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/poollet/api/storage/controller"
	storagehelper "github.com/onmetal/poollet/api/storage/helper"
	storageindex "github.com/onmetal/poollet/api/storage/index"
	storagefields "github.com/onmetal/poollet/api/storage/index/fields"
	"github.com/onmetal/poollet/broker/dependents"
	dependentbuilder "github.com/onmetal/poollet/broker/dependents/builder"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VolumeUsedByMachineInMachinePoolCachedPredicate struct {
	MachinePoolName string

	client client.Client
}

func (p *VolumeUsedByMachineInMachinePoolCachedPredicate) InjectClient(c client.Client) error {
	p.client = c
	return nil
}

func (p *VolumeUsedByMachineInMachinePoolCachedPredicate) Matches(ctx context.Context, referenced, obj client.Object) (bool, error) {
	volume := obj.(*storagev1alpha1.Volume)
	return controller.IsVolumeUsedByMachineInMachinePoolCached(ctx, p.client, volume, p.MachinePoolName)
}

type VolumeUsedByMachineInMachinePoolLivePredicate struct {
	MachinePoolName string

	apiReader client.Reader
}

func (p *VolumeUsedByMachineInMachinePoolLivePredicate) InjectAPIReader(apiReader client.Reader) error {
	p.apiReader = apiReader
	return nil
}

func (p *VolumeUsedByMachineInMachinePoolLivePredicate) Matches(ctx context.Context, referenced, obj client.Object) (bool, error) {
	volume := obj.(*storagev1alpha1.Volume)
	return controller.IsVolumeUsedByMachineInMachinePoolLive(ctx, p.apiReader, volume, p.MachinePoolName)
}

type VolumeRunsInVolumePoolPredicate struct {
	VolumePoolName string
}

func (p *VolumeRunsInVolumePoolPredicate) Matches(ctx context.Context, referenced, obj client.Object) (bool, error) {
	volume := obj.(*storagev1alpha1.Volume)
	return storagehelper.VolumeRunsInVolumePool(volume, p.VolumePoolName), nil
}

func SetupVolumeToSecret(secretDependents dependents.Dependents, volumePoolName string) error {
	return dependentbuilder.NewDependentFor(secretDependents).
		Referent(&storagev1alpha1.Volume{}).
		Referenced(&corev1.Secret{}).
		FieldReference(storagefields.VolumeSpecSecretNamesField, storageindex.ExtractVolumeSpecSecretNames,
			dependentbuilder.WithCachedClientListOptions(&client.MatchingFields{storagefields.VolumeSpecVolumePoolRefName: volumePoolName}),
			dependentbuilder.WithLiveListPredicates(&VolumeRunsInVolumePoolPredicate{VolumePoolName: volumePoolName}),
		).
		Complete()
}

func SetupVolumeToNamespace(namespaceDependents dependents.Dependents, volumePoolName string) error {
	return dependentbuilder.NewDependentFor(namespaceDependents).
		Referent(&storagev1alpha1.Volume{}).
		NamespaceReference(
			dependentbuilder.WithCachedClientListOptions(&client.MatchingFields{storagefields.VolumeSpecVolumePoolRefName: volumePoolName}),
			dependentbuilder.WithLiveListPredicates(&VolumeRunsInVolumePoolPredicate{VolumePoolName: volumePoolName}),
		).
		Complete()
}

func SetupMachineVolumeToSecret(secretDependents dependents.Dependents, machinePoolName string) error {
	return dependentbuilder.NewDependentFor(secretDependents).
		Referent(&storagev1alpha1.Volume{}).
		Referenced(&corev1.Secret{}).
		FieldReference(storagefields.VolumeSpecSecretNamesField, storageindex.ExtractVolumeSpecSecretNames,
			dependentbuilder.WithCachedListPredicates(&VolumeUsedByMachineInMachinePoolCachedPredicate{MachinePoolName: machinePoolName}),
			dependentbuilder.WithLiveListPredicates(&VolumeUsedByMachineInMachinePoolLivePredicate{MachinePoolName: machinePoolName}),
		).
		Complete()
}
