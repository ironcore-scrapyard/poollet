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

package helper

import (
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	"github.com/onmetal/onmetal-api/controllers/shared"
	"k8s.io/apimachinery/pkg/util/sets"
)

func MachineRunsInMachinePool(machine *computev1alpha1.Machine, poolName string) bool {
	machinePoolRef := machine.Spec.MachinePoolRef
	if machinePoolRef == nil {
		return false
	}
	return machinePoolRef.Name == poolName
}

type MachinePredicate func(machine *computev1alpha1.Machine) bool

func AndMachinePredicate(predicates ...MachinePredicate) MachinePredicate {
	return func(machine *computev1alpha1.Machine) bool {
		for _, predicate := range predicates {
			if !predicate(machine) {
				return false
			}
		}
		return true
	}
}

func MachineSpecVolumeNames(machine *computev1alpha1.Machine) sets.String {
	return shared.MachineSpecVolumeNames(machine)
}

func MachineSpecNetworkInterfaceNames(machine *computev1alpha1.Machine) sets.String {
	return shared.MachineSpecNetworkInterfaceNames(machine)
}

func MachineSpecReferencesNetworkInterfaceName(machine *computev1alpha1.Machine, nicName string) bool {
	return MachineSpecNetworkInterfaceNames(machine).Has(nicName)
}

func MachineSpecSecretNames(machine *computev1alpha1.Machine) sets.String {
	names := sets.NewString()

	if imagePullSecretRef := machine.Spec.ImagePullSecretRef; imagePullSecretRef != nil {
		names.Insert(imagePullSecretRef.Name)
	}

	if ignitionRef := machine.Spec.IgnitionRef; ignitionRef != nil {
		names.Insert(ignitionRef.Name)
	}

	return names
}

func MachineSpecConfigMapNames(machine *computev1alpha1.Machine) sets.String {
	names := sets.NewString()

	return names
}
