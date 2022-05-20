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
	"fmt"

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	computeindexclient "github.com/onmetal/poollet/api/compute/client/index"
	computehelper "github.com/onmetal/poollet/api/compute/helper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
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
		computehelper.ByMachineSpecReferencingVolumeClaim(claimRef.Name),
	)
	return matchingMachine != nil, nil
}

func IsVolumeUsedCached(ctx context.Context, c client.Client, volume *storagev1alpha1.Volume, machinePoolName string) (bool, error) {
	claimRef := volume.Spec.ClaimRef
	if claimRef == nil {
		return false, nil
	}

	volumeClaimKey := client.ObjectKey{Namespace: volume.Namespace, Name: claimRef.Name}
	machines, err := computeindexclient.ListMachinesReferencingVolumeClaimKey(ctx, c, volumeClaimKey)
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

func IsVolumeClaimUsedCached(ctx context.Context, c client.Client, volumeClaim *storagev1alpha1.VolumeClaim, machinePoolName string) (bool, error) {
	if volumeClaim.Spec.VolumeRef == nil || volumeClaim.Status.Phase != storagev1alpha1.VolumeClaimBound {
		return false, nil
	}

	volumeClaimKey := client.ObjectKeyFromObject(volumeClaim)
	machines, err := computeindexclient.ListMachinesReferencingVolumeClaimKey(ctx, c, volumeClaimKey)
	if err != nil {
		return false, err
	}

	matchingMachine := computehelper.FindMachine(machines,
		computehelper.ByMachineRunningInMachinePool(machinePoolName),
	)
	return matchingMachine != nil, nil
}

func GetVolumeClaimVolumeReconcileRequests(ctx context.Context, c client.Client, volumeClaim *storagev1alpha1.VolumeClaim, machinePoolName string) ([]reconcile.Request, error) {
	ok, err := IsVolumeClaimUsedCached(ctx, c, volumeClaim, machinePoolName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: volumeClaim.Namespace, Name: volumeClaim.Spec.VolumeRef.Name}}}, nil
}

func GetBoundMachineVolumeClaims(ctx context.Context, c client.Client, machine *computev1alpha1.Machine) ([]*storagev1alpha1.VolumeClaim, error) {
	var res []*storagev1alpha1.VolumeClaim
	for _, volumeClaimName := range computehelper.MachineSpecVolumeClaimNames(machine) {
		volumeClaim := &storagev1alpha1.VolumeClaim{}
		volumeClaimKey := client.ObjectKey{Namespace: machine.Namespace, Name: volumeClaimName}
		if err := c.Get(ctx, volumeClaimKey, volumeClaim); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("error getting machine volume claim %s: %w", volumeClaimKey, err)
			}
			continue
		}

		if volumeClaim.Spec.VolumeRef == nil || volumeClaim.Status.Phase != storagev1alpha1.VolumeClaimBound {
			continue
		}

		res = append(res, volumeClaim)
	}
	return res, nil
}

func GetMachineVolumeReconcileRequests(ctx context.Context, c client.Client, machine *computev1alpha1.Machine) ([]reconcile.Request, error) {
	volumeClaims, err := GetBoundMachineVolumeClaims(ctx, c, machine)
	if err != nil {
		return nil, err
	}

	res := make([]reconcile.Request, 0, len(volumeClaims))
	for _, volumeClaim := range volumeClaims {
		res = append(res, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: machine.Namespace, Name: volumeClaim.Spec.VolumeRef.Name}})
	}
	return res, nil
}
