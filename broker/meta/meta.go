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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const BrokerOwnerReferenceAnnotation = "broker.onmetal.de/owner"

type BrokerOwnerReference struct {
	ClusterName string    `json:"clusterName"`
	APIVersion  string    `json:"apiVersion"`
	Kind        string    `json:"kind"`
	Namespace   string    `json:"namespace,omitempty"`
	Name        string    `json:"name"`
	UID         types.UID `json:"uid"`
	Controller  *bool     `json:"controller,omitempty"`
}

func SetBrokerControllerReference(clusterName string, brokerOwner, childControlled client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(brokerOwner, scheme)
	if err != nil {
		return err
	}

	ref := BrokerOwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
		Controller:  pointer.Bool(true),
	}

	if existing := GetBrokerControllerOf(childControlled); existing != nil && !referSameObject(*existing, ref) {
		return fmt.Errorf("object %s is already broker-controlled by %s %s",
			client.ObjectKeyFromObject(childControlled),
			existing.Kind,
			client.ObjectKey{Namespace: existing.Namespace, Name: existing.Name},
		)
	}

	upsertBrokerOwnerRef(ref, childControlled)
	return nil
}

func RemoveBrokerOwnerReference(clusterName string, brokerOwner, object client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(brokerOwner, scheme)
	if err != nil {
		return err
	}
	ref := BrokerOwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
	}
	removeOwnerRef(ref, object)
	return nil
}

func removeOwnerRef(ref BrokerOwnerReference, object client.Object) {
	owners := GetBrokerOwnerReferences(object)
	if idx := indexBrokerOwnerRef(owners, ref); idx >= 0 {
		owners[idx] = owners[len(owners)-1]
		owners = owners[:len(owners)-1]
	} else {
		return
	}
	SetBrokerOwnerReferences(object, owners)
}

func SetBrokerOwnerReference(clusterName string, brokerOwner, object client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(brokerOwner, scheme)
	if err != nil {
		return err
	}
	ref := BrokerOwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
	}
	upsertBrokerOwnerRef(ref, object)
	return nil
}

func HasBrokerOwnerReference(clusterName string, brokerOwner, object client.Object, scheme *runtime.Scheme) (bool, error) {
	gvk, err := apiutil.GVKForObject(brokerOwner, scheme)
	if err != nil {
		return false, err
	}
	ref := BrokerOwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
	}
	return indexBrokerOwnerRef(GetBrokerOwnerReferences(object), ref) != -1, nil
}

func upsertBrokerOwnerRef(ref BrokerOwnerReference, object client.Object) {
	owners := GetBrokerOwnerReferences(object)
	if idx := indexBrokerOwnerRef(owners, ref); idx == -1 {
		owners = append(owners, ref)
	} else {
		owners[idx] = ref
	}
	SetBrokerOwnerReferences(object, owners)
}

// indexBrokerOwnerRef returns the index of the broker owner reference in the slice if found, or -1.
func indexBrokerOwnerRef(ownerReferences []BrokerOwnerReference, ref BrokerOwnerReference) int {
	for index, r := range ownerReferences {
		if referSameObject(r, ref) {
			return index
		}
	}
	return -1
}

func IsBrokerControlledBy(clusterName string, brokerOwner, childControlled client.Object) bool {
	existing := GetBrokerControllerOf(childControlled)
	if existing == nil {
		return false
	}

	return existing.ClusterName == clusterName && existing.UID == brokerOwner.GetUID()
}

func referSameObject(a, b BrokerOwnerReference) bool {
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

func SetBrokerOwnerReferences(obj client.Object, refs []BrokerOwnerReference) {
	data, err := json.Marshal(refs)
	if err != nil {
		log.Log.WithName("SetBrokerOwnerReferences").Error(err, "Error encoding broker owner references")
		return
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[BrokerOwnerReferenceAnnotation] = string(data)
	obj.SetAnnotations(annotations)
}

func GetBrokerOwnerReferences(obj client.Object) []BrokerOwnerReference {
	refsData, ok := obj.GetAnnotations()[BrokerOwnerReferenceAnnotation]
	if !ok {
		return nil
	}

	var refs []BrokerOwnerReference
	if err := json.Unmarshal([]byte(refsData), &refs); err != nil {
		log.Log.WithName("GetBrokerOwnerReferences").Error(err, "Error decoding broker owner references")
		return nil
	}

	return refs
}

func RefersToClusterAndType(clusterName string, ownerType client.Object, ref BrokerOwnerReference, scheme *runtime.Scheme) (bool, error) {
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

func GetBrokerControllerOf(childControllee client.Object) *BrokerOwnerReference {
	refs := GetBrokerOwnerReferences(childControllee)
	if refs == nil {
		return nil
	}

	for _, ref := range refs {
		if controller := ref.Controller; controller != nil && *controller {
			ref := ref
			return &ref
		}
	}
	return nil
}
