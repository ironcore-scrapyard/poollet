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

package controller

import (
	"context"

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/controllers/shared"
	computeindexclient "github.com/onmetal/poollet/api/compute/client/index"
	computehelper "github.com/onmetal/poollet/api/compute/helper"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func IsVolumeUsedLive(ctx context.Context, r client.Reader, volume *storagev1alpha1.Volume, machinePoolName string) (bool, error) {
	claimRef := volume.Spec.ClaimRef
	if claimRef == nil {
		return false, nil
	}

	machineList := &computev1alpha1.MachineList{}
	if err := r.List(ctx, machineList,
		client.InNamespace(volume.Namespace),
	); err != nil {
		return false, err
	}

	matchingMachine := computehelper.FindMachine(machineList.Items,
		computehelper.ByMachineRunningInMachinePool(machinePoolName),
		computehelper.ByMachineSpecReferencingVolume(claimRef.Name),
	)
	return matchingMachine != nil, nil
}

func IsVolumeUsedCached(ctx context.Context, c client.Client, volume *storagev1alpha1.Volume, machinePoolName string) (bool, error) {
	claimRef := volume.Spec.ClaimRef
	if claimRef == nil {
		return false, nil
	}

	volumeKey := client.ObjectKey{Namespace: volume.Namespace, Name: claimRef.Name}
	machines, err := computeindexclient.ListMachinesReferencingVolumeKey(ctx, c, volumeKey)
	if err != nil {
		return false, err
	}

	matchingMachine := computehelper.FindMachine(machines,
		computehelper.ByMachineRunningInMachinePool(machinePoolName),
	)
	return matchingMachine != nil, nil
}

func IsVolumeUsedCachedOrLive(ctx context.Context, r client.Reader, c client.Client, volume *storagev1alpha1.Volume, machinePoolName string) (bool, error) {
	if ok, err := IsVolumeUsedCached(ctx, c, volume, machinePoolName); err != nil || ok {
		return ok, err
	}
	if ok, err := IsVolumeUsedLive(ctx, r, volume, machinePoolName); err != nil || ok {
		return ok, err
	}
	return false, nil
}

func VolumeReconcileRequestsFromMachine(machine *computev1alpha1.Machine) []reconcile.Request {
	volumeNames := shared.MachineSpecVolumeNames(machine)
	res := make([]reconcile.Request, 0, len(volumeNames))
	for volumeName := range volumeNames {
		res = append(res, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: machine.Namespace, Name: volumeName}})
	}
	return res
}
