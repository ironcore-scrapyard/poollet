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

package builder

import (
	"fmt"

	"github.com/onmetal/poollet/broker/dependents"
	brokerhandler "github.com/onmetal/poollet/broker/handler"
	poollethandler "github.com/onmetal/poollet/handler"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type Dependent struct {
	dependents dependents.Dependents

	referentType   client.Object
	referencedType client.Object

	fieldReferenceInput     *FieldReferenceInput
	namespaceReferenceInput *NamespaceReferenceInput

	predicates []predicate.Predicate
}

type FieldReferenceInput struct {
	field       string
	indexerFunc client.IndexerFunc

	cachedListOptions    []dependents.ListOption
	cachedListPredicates []dependents.ListPredicate

	liveListOptions    []dependents.ListOption
	liveListPredicates []dependents.ListPredicate

	globalListOptions    []dependents.ListOption
	globalListPredicates []dependents.ListPredicate
}

type FieldReferenceOption interface {
	ApplyToFieldReference(o *FieldReferenceInput)
}

type NamespaceReferenceInput struct {
	cachedListOptions    []dependents.ListOption
	cachedListPredicates []dependents.ListPredicate

	liveListOptions    []dependents.ListOption
	liveListPredicates []dependents.ListPredicate

	globalListOptions    []dependents.ListOption
	globalListPredicates []dependents.ListPredicate
}

type NamespaceReferenceOption interface {
	ApplyToNamespaceReference(o *NamespaceReferenceInput)
}

func NewDependentFor(dependents dependents.Dependents) *Dependent {
	return &Dependent{
		dependents: dependents,
	}
}

func (d *Dependent) Referent(referentType client.Object) *Dependent {
	d.referentType = referentType
	return d
}

func (d *Dependent) Referenced(referencedType client.Object) *Dependent {
	d.referencedType = referencedType
	return d
}

func (d *Dependent) FieldReference(field string, indexerFunc client.IndexerFunc, opts ...FieldReferenceOption) *Dependent {
	input := FieldReferenceInput{
		field:       field,
		indexerFunc: indexerFunc,
	}
	for _, opt := range opts {
		opt.ApplyToFieldReference(&input)
	}
	d.fieldReferenceInput = &input
	return d
}

func (d *Dependent) NamespaceReference(opts ...NamespaceReferenceOption) *Dependent {
	input := NamespaceReferenceInput{}
	for _, opt := range opts {
		opt.ApplyToNamespaceReference(&input)
	}
	d.namespaceReferenceInput = &input
	return d
}

func (d *Dependent) WithEventFilter(predicate predicate.Predicate) *Dependent {
	d.predicates = append(d.predicates, predicate)
	return d
}

func (d *Dependent) cachedReferencer() (dependents.Referencer, error) {
	switch {
	case d.fieldReferenceInput != nil:
		var listOptions []dependents.ListOption
		listOptions = append(listOptions, d.fieldReferenceInput.globalListOptions...)
		listOptions = append(listOptions, d.fieldReferenceInput.cachedListOptions...)
		listOptions = append(listOptions, &dependents.FieldListOptions{Field: d.fieldReferenceInput.field})

		var listPredicates []dependents.ListPredicate
		listPredicates = append(listPredicates, d.fieldReferenceInput.globalListPredicates...)
		listPredicates = append(listPredicates, d.fieldReferenceInput.cachedListPredicates...)

		return &dependents.ListReferencer{
			ReferentType: d.referentType,
			Options:      listOptions,
			Predicates:   listPredicates,
		}, nil
	case d.namespaceReferenceInput != nil:
		var listOptions []dependents.ListOption
		listOptions = append(listOptions, d.namespaceReferenceInput.globalListOptions...)
		listOptions = append(listOptions, d.namespaceReferenceInput.cachedListOptions...)
		listOptions = append(listOptions, dependents.InReferencedNamespaceNameNamespace)

		var listPredicates []dependents.ListPredicate
		listPredicates = append(listPredicates, d.namespaceReferenceInput.globalListPredicates...)
		listPredicates = append(listPredicates, d.namespaceReferenceInput.cachedListPredicates...)

		return &dependents.ListReferencer{
			ReferentType: d.referentType,
			Options:      listOptions,
			Predicates:   listPredicates,
		}, nil
	default:
		return nil, fmt.Errorf("must specify one of: FieldReference, NamespaceReference")
	}
}

func (d *Dependent) liveReferencer() (dependents.Referencer, error) {
	switch {
	case d.fieldReferenceInput != nil:
		var opts []dependents.ListOption
		opts = append(opts, d.fieldReferenceInput.globalListOptions...)
		opts = append(opts, d.fieldReferenceInput.liveListOptions...)

		var listPredicates []dependents.ListPredicate
		listPredicates = append(listPredicates, d.fieldReferenceInput.globalListPredicates...)
		listPredicates = append(listPredicates, d.fieldReferenceInput.liveListPredicates...)
		listPredicates = append(listPredicates, &dependents.FieldIndexerFuncPredicate{
			Field:       d.fieldReferenceInput.field,
			IndexerFunc: d.fieldReferenceInput.indexerFunc,
		})

		return &dependents.ListReferencer{
			Live:         true,
			ReferentType: d.referentType,
			Options:      opts,
			Predicates:   listPredicates,
		}, nil
	case d.namespaceReferenceInput != nil:
		var listOptions []dependents.ListOption
		listOptions = append(listOptions, d.namespaceReferenceInput.globalListOptions...)
		listOptions = append(listOptions, d.namespaceReferenceInput.liveListOptions...)
		listOptions = append(listOptions, dependents.InReferencedNamespaceNameNamespace)

		var listPredicates []dependents.ListPredicate
		listPredicates = append(listPredicates, d.namespaceReferenceInput.globalListPredicates...)
		listPredicates = append(listPredicates, d.namespaceReferenceInput.cachedListPredicates...)

		return &dependents.ListReferencer{
			ReferentType: d.referentType,
			Options:      listOptions,
			Predicates:   listPredicates,
		}, nil
	default:
		return nil, fmt.Errorf("must specify one of: FieldReference, NamespaceReference")
	}
}

func (d *Dependent) handler() (handler.EventHandler, error) {
	switch {
	case d.fieldReferenceInput != nil:
		return &brokerhandler.EnqueueRequestForTypeByDependentFieldReference{
			Type:  d.referencedType,
			Field: d.fieldReferenceInput.field,
		}, nil
	case d.namespaceReferenceInput != nil:
		return &poollethandler.EnqueueRequestForObjectNamespace{}, nil
	default:
		return nil, fmt.Errorf("must specify one of: FieldReference, NamespaceReference")
	}
}

func (d *Dependent) Build() (dependents.Dependent, error) {
	if d.referentType == nil {
		return dependents.Dependent{}, fmt.Errorf("must specify Referent")
	}

	cachedReferencer, err := d.cachedReferencer()
	if err != nil {
		return dependents.Dependent{}, err
	}

	liveReferencer, err := d.liveReferencer()
	if err != nil {
		return dependents.Dependent{}, err
	}

	hdl, err := d.handler()
	if err != nil {
		return dependents.Dependent{}, err
	}

	dependent := dependents.Dependent{
		ReferentType:     d.referentType,
		CachedReferencer: cachedReferencer,
		LiveReferencer:   liveReferencer,
		Handler:          hdl,
		Predicates:       d.predicates,
	}

	d.dependents.Dependent(dependent)

	return dependent, nil
}

func (d *Dependent) Complete() error {
	_, err := d.Build()
	return err
}

type CachedListOptions struct {
	options []dependents.ListOption
}

func (c CachedListOptions) ApplyToFieldReference(o *FieldReferenceInput) {
	o.cachedListOptions = c.options
}

func (c CachedListOptions) ApplyToNamespaceReference(o *NamespaceReferenceInput) {
	o.cachedListOptions = c.options
}

func WithCachedListOptions(opts ...dependents.ListOption) CachedListOptions {
	return CachedListOptions{options: opts}
}

func WithCachedClientListOptions(opts ...client.ListOption) CachedListOptions {
	return WithCachedListOptions(dependents.ClientListOptions(opts))
}

type CachedListPredicates struct {
	predicates []dependents.ListPredicate
}

func (c CachedListPredicates) ApplyToFieldReference(o *FieldReferenceInput) {
	o.cachedListPredicates = c.predicates
}

func (c CachedListPredicates) ApplyToNamespaceReference(o *NamespaceReferenceInput) {
	o.cachedListPredicates = c.predicates
}

func WithCachedListPredicates(predicates ...dependents.ListPredicate) CachedListPredicates {
	return CachedListPredicates{predicates: predicates}
}

type LiveListOptions struct {
	options []dependents.ListOption
}

func (c LiveListOptions) ApplyToFieldReference(o *FieldReferenceInput) {
	o.liveListOptions = c.options
}

func (c LiveListOptions) ApplyToNamespaceReference(o *NamespaceReferenceInput) {
	o.liveListOptions = c.options
}

func WithLiveListOptions(opts ...dependents.ListOption) LiveListOptions {
	return LiveListOptions{options: opts}
}

func WithLiveClientListOptions(opts ...client.ListOption) LiveListOptions {
	return WithLiveListOptions(dependents.ClientListOptions(opts))
}

type LiveListPredicates struct {
	predicates []dependents.ListPredicate
}

func (c LiveListPredicates) ApplyToFieldReference(o *FieldReferenceInput) {
	o.liveListPredicates = c.predicates
}

func (c LiveListPredicates) ApplyToNamespaceReference(o *NamespaceReferenceInput) {
	o.liveListPredicates = c.predicates
}

func WithLiveListPredicates(predicates ...dependents.ListPredicate) LiveListPredicates {
	return LiveListPredicates{predicates: predicates}
}

type ListOptions struct {
	options []dependents.ListOption
}

func (c ListOptions) ApplyToFieldReference(o *FieldReferenceInput) {
	o.globalListOptions = c.options
}

func (c ListOptions) ApplyToNamespaceReference(o *NamespaceReferenceInput) {
	o.globalListOptions = c.options
}

func WithListOptions(opts ...dependents.ListOption) ListOptions {
	return ListOptions{options: opts}
}

func WithClientListOptions(opts ...client.ListOption) ListOptions {
	return WithListOptions(dependents.ClientListOptions(opts))
}

type ListPredicates struct {
	predicates []dependents.ListPredicate
}

func (c ListPredicates) ApplyToFieldReference(o *FieldReferenceInput) {
	o.globalListPredicates = c.predicates
}

func (c ListPredicates) ApplyToNamespaceReference(o *NamespaceReferenceInput) {
	o.globalListPredicates = c.predicates
}

func WithListPredicates(predicates ...dependents.ListPredicate) ListPredicates {
	return ListPredicates{predicates: predicates}
}
