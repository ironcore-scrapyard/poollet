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

package index

import (
	"context"
	"fmt"

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	"github.com/onmetal/poollet/api/compute/index/fields"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ListMachinesRunningInMachinePool(ctx context.Context, c client.Client, poolName string) ([]computev1alpha1.Machine, error) {
	machineList := &computev1alpha1.MachineList{}
	if err := c.List(ctx, machineList,
		client.MatchingFields{
			fields.MachineSpecMachinePoolRefName: poolName,
		},
	); err != nil {
		return nil, fmt.Errorf("error listing machines running in machine pool %s: %w", poolName, err)
	}

	return machineList.Items, nil
}

func ListMachinesReferencingVolumeKey(ctx context.Context, c client.Client, volumeKey client.ObjectKey) ([]computev1alpha1.Machine, error) {
	machineList := &computev1alpha1.MachineList{}
	if err := c.List(ctx, machineList,
		client.InNamespace(volumeKey.Namespace),
		client.MatchingFields{
			fields.MachineSpecVolumeNames: volumeKey.Name,
		},
	); err != nil {
		return nil, fmt.Errorf("error listing machines running in referencing volume %s: %w", volumeKey, err)
	}

	return machineList.Items, nil
}

func ListMachinesReferencingNetworkInterfaceKey(ctx context.Context, c client.Client, nicKey client.ObjectKey) ([]computev1alpha1.Machine, error) {
	machineList := &computev1alpha1.MachineList{}
	if err := c.List(ctx, machineList,
		client.InNamespace(nicKey.Namespace),
		client.MatchingFields{
			fields.MachineSpecNetworkInterfaceNames: nicKey.Name,
		},
	); err != nil {
		return nil, fmt.Errorf("error listing machines running in referencing network interface %s: %w", nicKey, err)
	}

	return machineList.Items, nil
}
