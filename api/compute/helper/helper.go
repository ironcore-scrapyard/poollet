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

func OrMachinePredicate(predicates ...MachinePredicate) MachinePredicate {
	return func(machine *computev1alpha1.Machine) bool {
		for _, predicate := range predicates {
			if predicate(machine) {
				return true
			}
		}
		return false
	}
}

func FilterMachines(machines []computev1alpha1.Machine, predicates ...MachinePredicate) []computev1alpha1.Machine {
	var res []computev1alpha1.Machine
	for _, machine := range machines {
		if AndMachinePredicate(predicates...)(&machine) {
			res = append(res, machine)
		}
	}
	return res
}

func FindMachine(machines []computev1alpha1.Machine, predicates ...MachinePredicate) *computev1alpha1.Machine {
	for i := range machines {
		machine := &machines[i]
		if AndMachinePredicate(predicates...)(machine) {
			return machine
		}
	}
	return nil
}

func ByMachineRunningInMachinePool(poolName string) MachinePredicate {
	return func(machine *computev1alpha1.Machine) bool {
		return MachineRunsInMachinePool(machine, poolName)
	}
}

func MachineSpecVolumeClaimNames(machine *computev1alpha1.Machine) []string {
	var names []string
	for _, volume := range machine.Spec.Volumes {
		switch {
		case volume.VolumeClaimRef != nil:
			names = append(names, volume.VolumeClaimRef.Name)
		}
	}
	return names
}

func MachineSpecReferencesVolumeClaimName(machine *computev1alpha1.Machine, volumeClaimName string) bool {
	for _, name := range MachineSpecVolumeClaimNames(machine) {
		if name == volumeClaimName {
			return true
		}
	}
	return false
}

func ByMachineSpecReferencingVolumeClaim(volumeClaimName string) MachinePredicate {
	return func(machine *computev1alpha1.Machine) bool {
		return MachineSpecReferencesVolumeClaimName(machine, volumeClaimName)
	}
}

func MachineSpecNetworkInterfaceNames(machine *computev1alpha1.Machine) []string {
	var names []string
	for _, nic := range machine.Spec.NetworkInterfaces {
		switch {
		case nic.NetworkInterfaceRef != nil:
			names = append(names, nic.NetworkInterfaceRef.Name)
		case nic.Ephemeral != nil:
			names = append(names, shared.MachineEphemeralNetworkInterfaceName(machine.Name, nic.Name))
		}
	}
	return names
}

func MachineSpecReferencesNetworkInterfaceName(machine *computev1alpha1.Machine, nicName string) bool {
	for _, name := range MachineSpecNetworkInterfaceNames(machine) {
		if name == nicName {
			return true
		}
	}
	return false
}

func ByMachineSpecReferencingNetworkInterface(nicName string) MachinePredicate {
	return func(machine *computev1alpha1.Machine) bool {
		return MachineSpecReferencesNetworkInterfaceName(machine, nicName)
	}
}

func MachineSpecSecretNames(machine *computev1alpha1.Machine) []string {
	var names []string

	if imagePullSecretRef := machine.Spec.ImagePullSecretRef; imagePullSecretRef != nil {
		names = append(names, imagePullSecretRef.Name)
	}

	return names
}

func MachineSpecConfigMapNames(machine *computev1alpha1.Machine) []string {
	var names []string

	if ignitionRef := machine.Spec.IgnitionRef; ignitionRef != nil {
		names = append(names, ignitionRef.Name)
	}

	return names
}
