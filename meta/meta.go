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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

func NewListForObject(obj client.Object, scheme *runtime.Scheme) (client.ObjectList, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, err
	}

	listGVK := gvk.GroupVersion().WithKind(gvk.Kind + "List")

	switch obj.(type) {
	case *unstructured.Unstructured:
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(listGVK)
		return list, nil
	case *metav1.PartialObjectMetadata:
		list := &metav1.PartialObjectMetadataList{}
		list.SetGroupVersionKind(listGVK)
		return list, nil
	default:
		rList, err := scheme.New(listGVK)
		if err != nil {
			return nil, err
		}
		if list, ok := rList.(client.ObjectList); ok {
			return list, nil
		}
		return nil, fmt.Errorf("type %T does not implement client.ObjectList", rList)
	}
}

func EachListItem(list client.ObjectList, f func(obj client.Object) error) error {
	return meta.EachListItem(list, func(rObj runtime.Object) error {
		obj, ok := rObj.(client.Object)
		if !ok {
			return fmt.Errorf("object %T does not implement client.Object", rObj)
		}

		return f(obj)
	})
}

func SetLabel(obj metav1.Object, key, value string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	labels[key] = value
	obj.SetLabels(labels)
}

func SetLabels(obj metav1.Object, set map[string]string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	for k, v := range set {
		labels[k] = v
	}
	obj.SetLabels(labels)
}

func SetAnnotation(obj metav1.Object, key, value string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[key] = value
	obj.SetAnnotations(annotations)
}

func SetAnnotations(obj metav1.Object, set map[string]string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	for k, v := range set {
		annotations[k] = v
	}
	obj.SetAnnotations(annotations)
}
