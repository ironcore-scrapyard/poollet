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

package provider

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type Provider interface {
	Target(ctx context.Context, key client.ObjectKey, targetObj client.Object) error
}

type Registry struct {
	scheme    *runtime.Scheme
	providers map[schema.GroupVersionKind]Provider
}

func NewRegistry(scheme *runtime.Scheme) *Registry {
	return &Registry{
		scheme:    scheme,
		providers: make(map[schema.GroupVersionKind]Provider),
	}
}

func (r *Registry) typeProviderFor(object client.Object) (Provider, error) {
	gvk, err := apiutil.GVKForObject(object, r.scheme)
	if err != nil {
		return nil, err
	}

	provider, ok := r.providers[gvk]
	if !ok {
		return nil, fmt.Errorf("no provider registered for %s", gvk)
	}

	return provider, nil
}

func (r *Registry) Register(object client.Object, provider Provider) error {
	gvk, err := apiutil.GVKForObject(object, r.scheme)
	if err != nil {
		return err
	}

	if _, ok := r.providers[gvk]; ok {
		return fmt.Errorf("already registered provider for %s", gvk)
	}

	r.providers[gvk] = provider
	return nil
}

func (r *Registry) Target(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	provider, err := r.typeProviderFor(obj)
	if err != nil {
		return err
	}

	return provider.Target(ctx, key, obj)
}

func TargetNamespaceFor(ctx context.Context, provider Provider, obj client.Object) (string, error) {
	namespaceName := obj.GetNamespace()
	if namespaceName == "" {
		return "", fmt.Errorf("object %T does not have a namespace set", obj)
	}

	namespaceKey := client.ObjectKey{Name: namespaceName}
	targetNamespace := &corev1.Namespace{}
	if err := provider.Target(ctx, namespaceKey, targetNamespace); err != nil {
		return "", err
	}
	return targetNamespace.Name, nil
}
