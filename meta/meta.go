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

const (
	domain                         = "partitionlet.onmetal.de"
	parentOwnerReferenceAnnotation = domain + "/parent-owner"
)

type ParentOwnerReference struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Namespace  string    `json:"namespace,omitempty"`
	Name       string    `json:"name"`
	UID        types.UID `json:"uid"`
	Controller *bool     `json:"controller,omitempty"`
}

func SetParentControllerReference(parentOwner, childControlled client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(parentOwner, scheme)
	if err != nil {
		return err
	}

	ref := ParentOwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  parentOwner.GetNamespace(),
		Name:       parentOwner.GetName(),
		UID:        parentOwner.GetUID(),
		Controller: pointer.Bool(true),
	}

	if existing := GetParentControllerOf(childControlled); existing != nil && !referSameObject(*existing, ref) {
		return fmt.Errorf("object %s is already parent-controlled by %s %s",
			client.ObjectKeyFromObject(childControlled),
			existing.Kind,
			client.ObjectKey{Namespace: existing.Namespace, Name: existing.Name},
		)
	}

	upsertParentOwnerRef(ref, childControlled)
	return nil
}

func SetParentOwnerReference(parentOwner, object client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(parentOwner, scheme)
	if err != nil {
		return err
	}
	ref := ParentOwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  parentOwner.GetNamespace(),
		Name:       parentOwner.GetName(),
		UID:        parentOwner.GetUID(),
	}
	upsertParentOwnerRef(ref, object)
	return nil
}

func upsertParentOwnerRef(ref ParentOwnerReference, object client.Object) {
	owners := GetParentOwnerReferences(object)
	if idx := indexParentOwnerRef(owners, ref); idx == -1 {
		owners = append(owners, ref)
	} else {
		owners[idx] = ref
	}
	SetParentOwnerReferences(object, owners)
}

// indexParentOwnerRef returns the index of the parent owner reference in the slice if found, or -1.
func indexParentOwnerRef(ownerReferences []ParentOwnerReference, ref ParentOwnerReference) int {
	for index, r := range ownerReferences {
		if referSameObject(r, ref) {
			return index
		}
	}
	return -1
}

func IsParentControlledBy(parentOwner, childControlled client.Object, scheme *runtime.Scheme) (bool, error) {
	existing := GetParentControllerOf(childControlled)
	if existing == nil {
		return false, nil
	}

	gvk, err := apiutil.GVKForObject(parentOwner, scheme)
	if err != nil {
		return false, err
	}

	ref := ParentOwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  parentOwner.GetNamespace(),
		Name:       parentOwner.GetName(),
		UID:        parentOwner.GetUID(),
	}

	return referSameObject(ref, *existing), nil
}

func referSameObject(a, b ParentOwnerReference) bool {
	aGV, err := schema.ParseGroupVersion(a.APIVersion)
	if err != nil {
		return false
	}

	bGV, err := schema.ParseGroupVersion(b.APIVersion)
	if err != nil {
		return false
	}

	return aGV.Group == bGV.Group && a.Kind == b.Kind && a.Namespace == b.Namespace && a.Name == b.Name
}

func SetParentOwnerReferences(obj client.Object, refs []ParentOwnerReference) {
	data, err := json.Marshal(refs)
	if err != nil {
		log.Log.WithName("SetParentOwnerReferences").Error(err, "Error encoding parent owner references")
		return
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[parentOwnerReferenceAnnotation] = string(data)
	obj.SetAnnotations(annotations)
}

func GetParentOwnerReferences(obj client.Object) []ParentOwnerReference {
	refsData, ok := obj.GetAnnotations()[parentOwnerReferenceAnnotation]
	if !ok {
		return nil
	}

	var refs []ParentOwnerReference
	if err := json.Unmarshal([]byte(refsData), &refs); err != nil {
		log.Log.WithName("GetParentOwnerReferences").Error(err, "Error decoding parent owner references")
		return nil
	}

	return refs
}

func GetParentControllerOf(childControllee client.Object) *ParentOwnerReference {
	refs := GetParentOwnerReferences(childControllee)
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
