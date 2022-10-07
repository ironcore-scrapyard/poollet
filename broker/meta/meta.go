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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
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

type ObjectMeta struct {
	OwnerReferences []OwnerReference `json:"ownerReferences,omitempty"`
}

const (
	objectMetaAnnotation       = "broker.api.onmetal.de/object-meta"
	deprecatedBrokerAnnotation = "broker.onmeta.de/owner"
)

func GetObjectMeta(obj metav1.Object) ObjectMeta {
	annotations := obj.GetAnnotations()
	data, ok := annotations[objectMetaAnnotation]
	if !ok {
		if data, ok = annotations[deprecatedBrokerAnnotation]; ok {
			var owners []OwnerReference
			if err := json.Unmarshal([]byte(data), &owners); err != nil {
				log.Log.WithName("getObjectMeta").Error(err, "Error decoding object metadata")
				return ObjectMeta{}
			}
			return ObjectMeta{OwnerReferences: owners}
		}
		return ObjectMeta{}
	}

	var meta ObjectMeta
	if err := json.Unmarshal([]byte(data), &meta); err != nil {
		log.Log.WithName("getObjectMeta").Error(err, "Error decoding object metadata")
		return ObjectMeta{}
	}

	return meta
}

func SetObjectMeta(obj metav1.Object, meta ObjectMeta) {
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

func SetBrokerControllerReference(clusterName string, brokerOwner, childControlled client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(brokerOwner, scheme)
	if err != nil {
		return err
	}

	ref := OwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
		Controller:  pointer.Bool(true),
	}

	childControlledMeta := GetObjectMeta(childControlled)

	if existing := getBrokerControllerOf(&childControlledMeta); existing != nil && !referSameObject(*existing, ref) {
		return fmt.Errorf("object %s is already broker-controlled by %s %s",
			client.ObjectKeyFromObject(childControlled),
			existing.Kind,
			client.ObjectKey{Namespace: existing.Namespace, Name: existing.Name},
		)
	}

	upsertBrokerOwnerRef(ref, &childControlledMeta)
	SetObjectMeta(childControlled, childControlledMeta)
	return nil
}

func RemoveBrokerOwnerReference(clusterName string, brokerOwner, object client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(brokerOwner, scheme)
	if err != nil {
		return err
	}
	ref := OwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
	}
	meta := GetObjectMeta(object)
	if removeOwnerRef(ref, &meta) {
		SetObjectMeta(object, meta)
	}
	return nil
}

func removeOwnerRef(ref OwnerReference, meta *ObjectMeta) (removed bool) {
	if idx := indexBrokerOwnerRef(meta.OwnerReferences, ref); idx >= 0 {
		meta.OwnerReferences[idx] = meta.OwnerReferences[len(meta.OwnerReferences)-1]
		meta.OwnerReferences = meta.OwnerReferences[:len(meta.OwnerReferences)-1]
		return true
	}
	return false
}

func SetBrokerOwnerReference(clusterName string, brokerOwner, object client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(brokerOwner, scheme)
	if err != nil {
		return err
	}
	ref := OwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
	}
	meta := GetObjectMeta(object)
	upsertBrokerOwnerRef(ref, &meta)
	SetObjectMeta(object, meta)
	return nil
}

func HasBrokerOwnerReference(clusterName string, brokerOwner, object client.Object, scheme *runtime.Scheme) (bool, error) {
	gvk, err := apiutil.GVKForObject(brokerOwner, scheme)
	if err != nil {
		return false, err
	}

	ref := OwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
	}

	meta := GetObjectMeta(object)

	return indexBrokerOwnerRef(meta.OwnerReferences, ref) != -1, nil
}

func upsertBrokerOwnerRef(ref OwnerReference, meta *ObjectMeta) {
	if idx := indexBrokerOwnerRef(meta.OwnerReferences, ref); idx == -1 {
		meta.OwnerReferences = append(meta.OwnerReferences, ref)
	} else {
		meta.OwnerReferences[idx] = ref
	}
}

// indexBrokerOwnerRef returns the index of the broker owner reference in the slice if found, or -1.
func indexBrokerOwnerRef(ownerReferences []OwnerReference, ref OwnerReference) int {
	for index, r := range ownerReferences {
		if referSameObject(r, ref) {
			return index
		}
	}
	return -1
}

func IsBrokerControlledBy(clusterName string, brokerOwner, childControlled client.Object) bool {
	childControlledMeta := GetObjectMeta(childControlled)
	return isBrokerControlledBy(clusterName, brokerOwner, &childControlledMeta)
}

func isBrokerControlledBy(clusterName string, brokerOwner client.Object, childControlledMeta *ObjectMeta) bool {
	existing := getBrokerControllerOf(childControlledMeta)
	if existing == nil {
		return false
	}

	return existing.ClusterName == clusterName && existing.UID == brokerOwner.GetUID()
}

func referSameObject(a, b OwnerReference) bool {
	aGV, err := schema.ParseGroupVersion(a.APIVersion)
	if err != nil {
		return false
	}

	bGV, err := schema.ParseGroupVersion(b.APIVersion)
	if err != nil {
		return false
	}

	return a.ClusterName == b.ClusterName &&
		aGV.Group == bGV.Group &&
		a.Kind == b.Kind &&
		a.Namespace == b.Namespace &&
		a.Name == b.Name
}

func RefersToClusterAndType(clusterName string, ownerType client.Object, ref OwnerReference, scheme *runtime.Scheme) (bool, error) {
	if ref.ClusterName != clusterName {
		return false, nil
	}

	expectedOwnerGVK, err := apiutil.GVKForObject(ownerType, scheme)
	if err != nil {
		return false, err
	}

	actualOwnerGV, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return false, err
	}

	expectedOwnerGK := expectedOwnerGVK.GroupKind()
	actualOwnerGK := actualOwnerGV.WithKind(ref.Kind).GroupKind()

	return expectedOwnerGK == actualOwnerGK, nil
}

func getBrokerControllerOf(meta *ObjectMeta) *OwnerReference {
	if meta.OwnerReferences == nil {
		return nil
	}

	for _, ref := range meta.OwnerReferences {
		if controller := ref.Controller; controller != nil && *controller {
			ref := ref
			return &ref
		}
	}
	return nil
}

func GetBrokerControllerOf(childControllee client.Object) *OwnerReference {
	refs := GetObjectMeta(childControllee)
	return getBrokerControllerOf(&refs)
}
