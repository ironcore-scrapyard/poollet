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

package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type Client interface {
	client.Client
	FieldExtractor
}

type FieldExtractor interface {
	ExtractField(obj client.Object, field string) ([]string, error)
}

type Indexers map[string]client.IndexerFunc

type FieldExtractorRegistry struct {
	indexersByGVK map[schema.GroupVersionKind]Indexers
	scheme        *runtime.Scheme
}

func NewFieldExtractorRegistry(scheme *runtime.Scheme) *FieldExtractorRegistry {
	return &FieldExtractorRegistry{
		indexersByGVK: make(map[schema.GroupVersionKind]Indexers),
		scheme:        scheme,
	}
}

func (r *FieldExtractorRegistry) Register(object client.Object, field string, index client.IndexerFunc) error {
	gvk, err := apiutil.GVKForObject(object, r.scheme)
	if err != nil {
		return err
	}

	indexers := r.indexersByGVK[gvk]
	if indexers == nil {
		indexers = make(Indexers)
		r.indexersByGVK[gvk] = indexers
	}

	if _, ok := indexers[field]; ok {
		return fmt.Errorf("type %s field %s already registered", gvk, field)
	}
	indexers[field] = index
	return nil
}

func (r *FieldExtractorRegistry) ExtractField(obj client.Object, field string) ([]string, error) {
	gvk, err := apiutil.GVKForObject(obj, r.scheme)
	if err != nil {
		return nil, err
	}

	indexers := r.indexersByGVK[gvk]
	if indexers == nil {
		return nil, fmt.Errorf("no indexers for type %s", gvk)
	}

	extract := indexers[field]
	if extract == nil {
		return nil, fmt.Errorf("no index field %s for type %s", field, gvk)
	}

	return extract(obj), nil
}

type FieldIndexerRegistryOverlay struct {
	FieldIndexer client.FieldIndexer
	Registry     *FieldExtractorRegistry
}

func (f FieldIndexerRegistryOverlay) IndexField(ctx context.Context, object client.Object, field string, index client.IndexerFunc) error {
	if err := f.FieldIndexer.IndexField(ctx, object, field, index); err != nil {
		return err
	}
	if err := f.Registry.Register(object, field, index); err != nil {
		return err
	}
	return nil
}

type FieldExtractorClient struct {
	client.Client
	*FieldExtractorRegistry
}
