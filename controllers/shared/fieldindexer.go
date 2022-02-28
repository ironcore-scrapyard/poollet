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

package shared

import (
	"github.com/onmetal/controller-utils/clientutils"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	MachineSpecVolumeAttachmentsVolumeClaimRefNameField = ".spec[?(machinePool.name == $machinePool)].volumeAttachments[*].claim.ref.name"
)

func NewParentFieldIndexer(machinePoolName string, parentFieldIndexer client.FieldIndexer, scheme *runtime.Scheme) *clientutils.SharedFieldIndexer {
	indexer := clientutils.NewSharedFieldIndexer(parentFieldIndexer, scheme)

	indexer.MustRegister(&computev1alpha1.Machine{}, MachineSpecVolumeAttachmentsVolumeClaimRefNameField, func(object client.Object) []string {
		machine := object.(*computev1alpha1.Machine)
		if machine.Spec.MachinePool.Name != machinePoolName {
			return nil
		}

		var volumeClaimNames []string
		for _, volumeAttachment := range machine.Spec.VolumeAttachments {
			if volumeClaim := volumeAttachment.VolumeClaim; volumeClaim != nil {
				volumeClaimNames = append(volumeClaimNames, volumeClaim.Ref.Name)
			}
		}
		return volumeClaimNames
	})

	return indexer
}
