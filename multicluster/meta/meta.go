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

package meta

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type OwnerReference struct {
	ClusterName string    `json:"clusterName"`
	APIVersion  string    `json:"apiVersion"`
	Kind        string    `json:"kind"`
	Namespace   string    `json:"namespace,omitempty"`
	Name        string    `json:"name"`
	UID         types.UID `json:"uid"`
	Controller  *bool     `json:"controller,omitempty"`
}

func GetOwnerReferences(obj metav1.Object) []OwnerReference {
	return getObjectMeta(obj).OwnerReferences
}

func SetOwnerReferences(obj metav1.Object, refs []OwnerReference) {
	meta := getObjectMeta(obj)
	meta.OwnerReferences = refs
	setObjectMeta(obj, meta)
}

type ObjectMeta struct {
	OwnerReferences []OwnerReference `json:"ownerReferences,omitempty"`
}

const (
	objectMetaAnnotation       = "multicluster.api.onmetal.de/object-meta"
	deprecatedBrokerAnnotation = "broker.onmetal.de/owner"
)

func getObjectMeta(obj metav1.Object) *ObjectMeta {
	annotations := obj.GetAnnotations()
	data, ok := annotations[objectMetaAnnotation]
	if !ok {
		if data, ok = annotations[deprecatedBrokerAnnotation]; ok {
			var owners []OwnerReference
			if err := json.Unmarshal([]byte(data), &owners); err != nil {
				log.Log.WithName("getObjectMeta").Error(err, "Error decoding object metadata")
				return &ObjectMeta{}
			}
			return &ObjectMeta{OwnerReferences: owners}
		}
		return &ObjectMeta{}
	}

	meta := &ObjectMeta{}
	if err := json.Unmarshal([]byte(data), meta); err != nil {
		log.Log.WithName("getObjectMeta").Error(err, "Error decoding object metadata")
		return &ObjectMeta{}
	}

	return meta
}

func setObjectMeta(obj metav1.Object, meta *ObjectMeta) {
	data, err := json.Marshal(meta)
	if err != nil {
		log.Log.WithName("setObjectMeta").Error(err, "Error encoding object metadata")
		return
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[objectMetaAnnotation] = string(data)
	delete(annotations, deprecatedBrokerAnnotation)
	obj.SetAnnotations(annotations)
}

func GetControllerOf(childControllee metav1.Object) *OwnerReference {
	brokerOwnerReferences := GetOwnerReferences(childControllee)
	if len(brokerOwnerReferences) == 0 {
		return nil
	}

	for _, ref := range brokerOwnerReferences {
		if controller := ref.Controller; controller != nil && *controller {
			ref := ref
			return &ref
		}
	}
	return nil
}

func IsControlledBy(clusterName string, brokerOwner, childControlled metav1.Object) bool {
	existing := GetControllerOf(childControlled)
	if existing == nil {
		return false
	}

	return existing.ClusterName == clusterName && existing.UID == brokerOwner.GetUID()
}
