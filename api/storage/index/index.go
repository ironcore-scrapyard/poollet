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

	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/poollet/api/storage/helper"
	"github.com/onmetal/poollet/api/storage/index/fields"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func VolumeSpecVolumePoolRefNameField(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(ctx, &storagev1alpha1.Volume{}, fields.VolumeSpecVolumePoolRefName, func(obj client.Object) []string {
		volume := obj.(*storagev1alpha1.Volume)

		volumePoolRef := volume.Spec.VolumePoolRef
		if volumePoolRef == nil {
			return []string{""}
		}

		return []string{volumePoolRef.Name}
	})
}

func ExtractVolumeSpecSecretNames(obj client.Object) []string {
	volume := obj.(*storagev1alpha1.Volume)

	if secretNames := helper.VolumeSpecSecretNames(volume); len(secretNames) > 0 {
		return secretNames
	}
	return []string{""}
}

func VolumeSpecSecretNamesField(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(ctx, &storagev1alpha1.Volume{}, fields.VolumeSpecSecretNamesField, ExtractVolumeSpecSecretNames)
}

func VolumeStatusSecretNamesField(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(ctx, &storagev1alpha1.Volume{}, fields.VolumeStatusSecretNamesField, func(obj client.Object) []string {
		volume := obj.(*storagev1alpha1.Volume)

		if secretNames := helper.VolumeStatusSecretNames(volume); len(secretNames) > 0 {
			return secretNames
		}
		return []string{""}
	})
}
