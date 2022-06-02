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

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	"github.com/onmetal/poollet/api/compute/helper"
	"github.com/onmetal/poollet/api/compute/index/fields"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func MachineSpecMachinePoolRefNameField(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, fields.MachineSpecMachinePoolRefName, func(obj client.Object) []string {
		machine := obj.(*computev1alpha1.Machine)
		machinePoolRef := machine.Spec.MachinePoolRef
		if machinePoolRef == nil {
			return []string{""}
		}
		return []string{machinePoolRef.Name}
	})
}

func MachineVolumeNamesField(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, fields.MachineSpecVolumeNames, func(obj client.Object) []string {
		machine := obj.(*computev1alpha1.Machine)

		if names := helper.MachineSpecVolumeNames(machine); len(names) > 0 {
			return names.UnsortedList()
		}
		return []string{""}
	})
}

func MachineNetworkInterfaceNamesField(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, fields.MachineSpecNetworkInterfaceNames, func(obj client.Object) []string {
		machine := obj.(*computev1alpha1.Machine)
		if names := helper.MachineSpecNetworkInterfaceNames(machine); len(names) > 0 {
			return names.UnsortedList()
		}
		return []string{""}
	})
}

func MachineSecretNamesField(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, fields.MachineSpecSecretNames, func(obj client.Object) []string {
		machine := obj.(*computev1alpha1.Machine)
		if names := helper.MachineSpecSecretNames(machine); len(names) > 0 {
			return names.UnsortedList()
		}
		return []string{""}
	})
}

func MachineConfigMapNamesField(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(ctx, &computev1alpha1.Machine{}, fields.MachineSpecConfigMapNames, func(obj client.Object) []string {
		machine := obj.(*computev1alpha1.Machine)
		if names := helper.MachineSpecConfigMapNames(machine); len(names) > 0 {
			return names.UnsortedList()
		}
		return []string{""}
	})
}
