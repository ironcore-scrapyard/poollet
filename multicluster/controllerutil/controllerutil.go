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

package controllerutil

import (
	"fmt"

	mcmeta "github.com/onmetal/poollet/multicluster/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type AlreadyOwnedError struct {
	Object metav1.Object
	Owner  mcmeta.OwnerReference
}

func (e *AlreadyOwnedError) Error() string {
	return fmt.Sprintf("object %s/%s is already owned by another %s controller %s.%s/%s",
		e.Object.GetNamespace(),
		e.Object.GetName(),
		e.Owner.Kind,
		e.Owner.ClusterName,
		e.Owner.Namespace,
		e.Owner.Name,
	)
}

func newAlreadyOwnedError(obj metav1.Object, brokerOwner mcmeta.OwnerReference) *AlreadyOwnedError {
	return &AlreadyOwnedError{
		Object: obj,
		Owner:  brokerOwner,
	}
}

func SetControllerReference(clusterName string, brokerOwner, childControlled metav1.Object, scheme *runtime.Scheme) error {
	ro, ok := brokerOwner.(runtime.Object)
	if !ok {
		return fmt.Errorf("%T is not a runtime.Object, cannot call SetControllerReference", brokerOwner)
	}

	gvk, err := apiutil.GVKForObject(ro, scheme)
	if err != nil {
		return err
	}

	ref := mcmeta.OwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
		Controller:  pointer.Bool(true),
	}

	if existing := mcmeta.GetControllerOf(childControlled); existing != nil && !referSameObject(*existing, ref) {
		return newAlreadyOwnedError(childControlled, *existing)
	}

	upsertOwnerRef(ref, childControlled)
	return nil
}

func RemoveOwnerReference(clusterName string, brokerOwner, object metav1.Object, scheme *runtime.Scheme) error {
	// Validate the owner.
	ro, ok := brokerOwner.(runtime.Object)
	if !ok {
		return fmt.Errorf("%T is not a runtime.Object, cannot call RemoveBrokerOwnerReference", brokerOwner)
	}

	gvk, err := apiutil.GVKForObject(ro, scheme)
	if err != nil {
		return err
	}
	ref := mcmeta.OwnerReference{
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

func removeOwnerRef(ref mcmeta.OwnerReference, object metav1.Object) {
	owners := mcmeta.GetOwnerReferences(object)
	if idx := indexOwnerRef(owners, ref); idx >= 0 {
		owners[idx] = owners[len(owners)-1]
		owners = owners[:len(owners)-1]
		mcmeta.SetOwnerReferences(object, owners)
	}
}

func SetOwnerReference(clusterName string, brokerOwner, object metav1.Object, scheme *runtime.Scheme) error {
	// Validate the owner.
	ro, ok := brokerOwner.(runtime.Object)
	if !ok {
		return fmt.Errorf("%T is not a runtime.Object, cannot call SetOwnerReference", brokerOwner)
	}

	gvk, err := apiutil.GVKForObject(ro, scheme)
	if err != nil {
		return err
	}
	ref := mcmeta.OwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
	}

	upsertOwnerRef(ref, object)
	return nil
}

func HasOwnerReference(clusterName string, brokerOwner, object metav1.Object, scheme *runtime.Scheme) (bool, error) {
	// Validate the owner.
	ro, ok := brokerOwner.(runtime.Object)
	if !ok {
		return false, fmt.Errorf("%T is not a runtime.Object, cannot call HasOwnerReference", brokerOwner)
	}

	gvk, err := apiutil.GVKForObject(ro, scheme)
	if err != nil {
		return false, err
	}

	ref := mcmeta.OwnerReference{
		ClusterName: clusterName,
		APIVersion:  gvk.GroupVersion().String(),
		Kind:        gvk.Kind,
		Namespace:   brokerOwner.GetNamespace(),
		Name:        brokerOwner.GetName(),
		UID:         brokerOwner.GetUID(),
	}

	return indexOwnerRef(mcmeta.GetOwnerReferences(object), ref) != -1, nil
}

func upsertOwnerRef(ref mcmeta.OwnerReference, obj metav1.Object) {
	owners := mcmeta.GetOwnerReferences(obj)
	if idx := indexOwnerRef(owners, ref); idx == -1 {
		owners = append(owners, ref)
	} else {
		owners[idx] = ref
	}
	mcmeta.SetOwnerReferences(obj, owners)
}

// indexOwnerRef returns the index of the broker owner reference in the slice if found, or -1.
func indexOwnerRef(ownerReferences []mcmeta.OwnerReference, ref mcmeta.OwnerReference) int {
	for index, r := range ownerReferences {
		if referSameObject(r, ref) {
			return index
		}
	}
	return -1
}

func referSameObject(a, b mcmeta.OwnerReference) bool {
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

func RefersToClusterAndType(clusterName string, ownerType client.Object, ref mcmeta.OwnerReference, scheme *runtime.Scheme) (bool, error) {
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

// GetClusterTypeOwnerReferences gets the mcmeta.OwnerReferences for the given cluster name and owner type,
// optionally filtering whether it's a controller or not.
func GetClusterTypeOwnerReferences(clusterName string, ownerType client.Object, obj client.Object, scheme *runtime.Scheme, controller bool) ([]mcmeta.OwnerReference, error) {
	var refs []mcmeta.OwnerReference
	if controller {
		if ref := mcmeta.GetControllerOf(obj); ref != nil {
			refs = []mcmeta.OwnerReference{*ref}
		}
	} else {
		refs = mcmeta.GetOwnerReferences(obj)
	}

	var filtered []mcmeta.OwnerReference
	for _, ref := range refs {
		ok, err := RefersToClusterAndType(clusterName, ownerType, ref, scheme)
		if err != nil {
			return nil, fmt.Errorf("[reference %v] %w", ref, err)
		}
		if ok {
			filtered = append(filtered, ref)
		}
	}

	return filtered, nil
}
