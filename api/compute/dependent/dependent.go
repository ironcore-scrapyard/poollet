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

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	computehelper "github.com/onmetal/poollet/api/compute/helper"
	computeindex "github.com/onmetal/poollet/api/compute/index"
	computefields "github.com/onmetal/poollet/api/compute/index/fields"
	computepredicate "github.com/onmetal/poollet/api/compute/predicate"
	"github.com/onmetal/poollet/broker/dependents"
	dependentbuilder "github.com/onmetal/poollet/broker/dependents/builder"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MachineMachinePoolPredicate struct {
	MachinePoolName string
}

func (p *MachineMachinePoolPredicate) Matches(ctx context.Context, referenced, obj client.Object) (bool, error) {
	machine := obj.(*computev1alpha1.Machine)
	return computehelper.MachineRunsInMachinePool(machine, p.MachinePoolName), nil
}

func SetupMachineToNamespace(namespaceDependents dependents.Dependents, machinePoolName string) error {
	return dependentbuilder.NewDependentFor(namespaceDependents).
		Referent(&computev1alpha1.Machine{}).
		NamespaceReference(
			dependentbuilder.WithCachedClientListOptions(client.MatchingFields{computefields.MachineSpecMachinePoolRefName: machinePoolName}),
			dependentbuilder.WithLiveListPredicates(&MachineMachinePoolPredicate{MachinePoolName: machinePoolName}),
		).
		WithEventFilter(computepredicate.MachineRunsInMachinePoolPredicate(machinePoolName)).
		Complete()
}

func SetupMachineToConfigMap(configMapDependents dependents.Dependents, machinePoolName string) error {
	return dependentbuilder.NewDependentFor(configMapDependents).
		Referent(&computev1alpha1.Machine{}).
		Referenced(&corev1.ConfigMap{}).
		FieldReference(computefields.MachineSpecConfigMapNames, computeindex.ExtractMachineConfigMapNames,
			dependentbuilder.WithListPredicates(&MachineMachinePoolPredicate{MachinePoolName: machinePoolName}),
		).
		WithEventFilter(computepredicate.MachineRunsInMachinePoolPredicate(machinePoolName)).
		Complete()
}

func SetupMachineToSecret(secretDependents dependents.Dependents, machinePoolName string) error {
	return dependentbuilder.NewDependentFor(secretDependents).
		Referent(&computev1alpha1.Machine{}).
		Referenced(&corev1.Secret{}).
		FieldReference(computefields.MachineSpecSecretNames, computeindex.ExtractMachineSecretNames,
			dependentbuilder.WithListPredicates(&MachineMachinePoolPredicate{MachinePoolName: machinePoolName}),
		).
		WithEventFilter(computepredicate.MachineRunsInMachinePoolPredicate(machinePoolName)).
		Complete()
}
