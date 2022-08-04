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

package dependents

import (
	"context"

	"github.com/onmetal/controller-utils/metautils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
)

type ListOption interface {
	ListOptions(referenced client.Object) []client.ListOption
}

type ClientListOptions []client.ListOption

func (o ClientListOptions) ListOptions(_ client.Object) []client.ListOption {
	return o
}

type ListPredicate interface {
	Matches(ctx context.Context, referenced, obj client.Object) (bool, error)
}

type ListPredicateFunc func(ctx context.Context, referenced, obj client.Object) (bool, error)

func (f ListPredicateFunc) Matches(ctx context.Context, referenced, obj client.Object) (bool, error) {
	return f(ctx, referenced, obj)
}

type ListReferencer struct {
	ReferentType client.Object
	Options      []ListOption
	Predicates   []ListPredicate
	Live         bool

	scheme *runtime.Scheme
	reader client.Reader
}

func (r *ListReferencer) InjectScheme(scheme *runtime.Scheme) error {
	r.scheme = scheme
	return nil
}

func (r *ListReferencer) InjectAPIReader(apiReader client.Reader) error {
	if r.Live {
		r.reader = apiReader
	}
	return nil
}

func (r *ListReferencer) InjectClient(client client.Client) error {
	if !r.Live {
		r.reader = client
	}
	return nil
}

func (r *ListReferencer) InjectFunc(f inject.Func) error {
	for _, opt := range r.Options {
		if err := f(opt); err != nil {
			return err
		}
	}
	for _, p := range r.Predicates {
		if err := f(p); err != nil {
			return err
		}
	}
	return nil
}

func filterList(list client.ObjectList, f func(obj client.Object) (bool, error)) error {
	var filtered []client.Object
	if err := metautils.EachListItem(list, func(obj client.Object) error {
		if ok, err := f(obj); err != nil || !ok {
			return err
		}

		filtered = append(filtered, obj)
		return nil
	}); err != nil {
		return err
	}

	return metautils.SetList(list, filtered)
}

func (r *ListReferencer) References(ctx context.Context, referenced client.Object) (bool, error) {
	referentList, err := metautils.NewListForObject(r.scheme, r.ReferentType)
	if err != nil {
		return false, err
	}

	var opts []client.ListOption
	for _, o := range r.Options {
		opts = append(opts, o.ListOptions(referenced)...)
	}

	if err := r.reader.List(ctx, referentList, opts...); err != nil {
		return false, err
	}

	if len(r.Predicates) > 0 {
		if err := filterList(referentList, func(obj client.Object) (bool, error) {
			for _, p := range r.Predicates {
				if ok, err := p.Matches(ctx, referenced, obj); err != nil || !ok {
					return false, err
				}
			}
			return true, nil
		}); err != nil {
			return false, err
		}
	}

	return meta.LenList(referentList) > 0, nil
}

// FieldListOptions is a ListOption that adds client.MatchingFields with the field set to the name of the
// referenced object and client.InNamespace with the value set to the namespace the referenced object is in,
// thus this makes the use of InReferencedObjectNamespace superfluous.
type FieldListOptions struct {
	Field string
}

func (f *FieldListOptions) ListOptions(referenced client.Object) []client.ListOption {
	return []client.ListOption{
		client.InNamespace(referenced.GetNamespace()),
		client.MatchingFields{f.Field: referenced.GetName()},
	}
}

type FieldIndexerFuncPredicate struct {
	Field       string
	IndexerFunc client.IndexerFunc
}

func (f *FieldIndexerFuncPredicate) Matches(ctx context.Context, referenced, obj client.Object) (bool, error) {
	return slices.Contains(f.IndexerFunc(obj), referenced.GetName()), nil
}

type inReferencedObjectNamespace struct{}

// InReferencedObjectNamespace is a ListOption to use the namespace of the referenced object for listing referents.
//
// Example: A Secret references a ConfigMap. In this case, client.InNamespace will be produced with the value
// being the namespace name of the ConfigMap.
var InReferencedObjectNamespace = inReferencedObjectNamespace{}

func (inReferencedObjectNamespace) ListOptions(referenced client.Object) []client.ListOption {
	return []client.ListOption{client.InNamespace(referenced.GetNamespace())}
}

type inReferencedNamespace struct{}

func (inReferencedNamespace) ListOptions(referenced client.Object) []client.ListOption {
	namespace := referenced.(*corev1.Namespace)
	return []client.ListOption{client.InNamespace(namespace.Name)}
}

// InReferencedNamespaceNameNamespace is a ListOption to use the name of the referenced namespace for listing referents.
//
// Example: A Secret references a Namespace. In this case, client.InNamespace will be produced with the value
// being the name of the referenced Namespace.
var InReferencedNamespaceNameNamespace = inReferencedNamespace{}
