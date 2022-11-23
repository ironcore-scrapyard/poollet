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

import storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"

func VolumeRunsInVolumePool(volume *storagev1alpha1.Volume, poolName string) bool {
	volumePoolRef := volume.Spec.VolumePoolRef
	if volumePoolRef == nil {
		return false
	}

	return volumePoolRef.Name == poolName
}

func FilterVolumesRunningInVolumePool(volumes []storagev1alpha1.Volume, poolName string) []storagev1alpha1.Volume {
	var res []storagev1alpha1.Volume
	for _, volume := range volumes {
		if VolumeRunsInVolumePool(&volume, poolName) {
			res = append(res, volume)
		}
	}
	return res
}

func VolumeReferencesAccessSecretName(volume *storagev1alpha1.Volume, secretName string) bool {
	access := volume.Status.Access
	if access == nil {
		return false
	}

	secretRef := access.SecretRef
	if secretRef == nil {
		return false
	}

	return secretRef.Name == secretName
}

func FilterVolumesReferencingSecretAccessName(volumes []storagev1alpha1.Volume, secretName string) []storagev1alpha1.Volume {
	var res []storagev1alpha1.Volume
	for _, volume := range volumes {
		if VolumeReferencesAccessSecretName(&volume, secretName) {
			res = append(res, volume)
		}
	}
	return res
}

func VolumeSpecSecretNames(volume *storagev1alpha1.Volume) []string {
	var secretNames []string

	if imagePullSecretRef := volume.Spec.ImagePullSecretRef; imagePullSecretRef != nil {
		secretNames = append(secretNames, imagePullSecretRef.Name)
	}

	return secretNames
}

func VolumeStatusSecretNames(volume *storagev1alpha1.Volume) []string {
	var secretNames []string

	if access := volume.Status.Access; access != nil {
		if secretRef := access.SecretRef; secretRef != nil {
			secretNames = append(secretNames, secretRef.Name)
		}
	}

	return secretNames
}
