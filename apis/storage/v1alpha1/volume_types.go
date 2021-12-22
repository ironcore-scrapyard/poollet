// Copyright 2021 OnMetal authors
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

package v1alpha1

import (
	"fmt"

	"github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
)

const (
	VolumeParentNamespaceAnnotation = "partitionlet.onmetal.de/volume-parent-namespace"
	VolumeParentNameAnnotation      = "partitionlet.onmetal.de/volume-parent-name"
)

const (
	VolumeSynced v1alpha1.VolumeAttachmentConditionType = "Synced"
)

func VolumeName(parentNamespace, parentName string) string {
	return fmt.Sprintf("%s--%s", parentNamespace, parentName)
}