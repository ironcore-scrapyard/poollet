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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	domain                              = "partitionlet.onmetal.de"
	parentControllerReferenceAnnotation = domain + "/parent-controller"
)

type ParentControllerReference struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Namespace  string    `json:"namespace,omitempty"`
	Name       string    `json:"name"`
	UID        types.UID `json:"uid"`
}

func SetParentControllerReference(parentOwner, childControlled client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(parentOwner, scheme)
	if err != nil {
		return err
	}

	ref := ParentControllerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  parentOwner.GetNamespace(),
		Name:       parentOwner.GetName(),
		UID:        parentOwner.GetUID(),
	}

	return UpsertParentControllerReference(ref, childControlled)
}

func UpsertParentControllerReference(ref ParentControllerReference, controlled client.Object) error {
	if existing := GetParentControllerOf(controlled); existing != nil && !referSameObject(*existing, ref) {
		return fmt.Errorf("object %s is already parent-controlled by %s %s",
			client.ObjectKeyFromObject(controlled),
			existing.Kind,
			client.ObjectKey{Namespace: existing.Namespace, Name: existing.Name},
		)
	}

	return setParentControllerRef(controlled, ref)
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

	ref := ParentControllerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  parentOwner.GetNamespace(),
		Name:       parentOwner.GetName(),
		UID:        parentOwner.GetUID(),
	}

	return referSameObject(ref, *existing), nil
}

func referSameObject(a, b ParentControllerReference) bool {
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

func setParentControllerRef(obj client.Object, ref ParentControllerReference) error {
	data, err := json.Marshal(ref)
	if err != nil {
		return err
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[parentControllerReferenceAnnotation] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

func GetParentControllerOf(childControllee client.Object) *ParentControllerReference {
	refData, ok := childControllee.GetAnnotations()[parentControllerReferenceAnnotation]
	if !ok {
		return nil
	}

	ref := &ParentControllerReference{}
	if err := json.Unmarshal([]byte(refData), ref); err != nil {
		log.Log.WithName("GetParentControllerOf").Error(err, "Error decoding parent controller reference")
		return nil
	}

	return ref
}
