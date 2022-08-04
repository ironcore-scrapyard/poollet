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
	"strings"

	"github.com/go-logr/logr"
	brokercontroller "github.com/onmetal/poollet/broker/controller"
	brokercorehandler "github.com/onmetal/poollet/broker/core/handler"
	brokerhandler "github.com/onmetal/poollet/broker/handler"
	"github.com/onmetal/poollet/broker/manager"
	brokerpredicate "github.com/onmetal/poollet/broker/predicate"
	poollethandler "github.com/onmetal/poollet/handler"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type Builder struct {
	mgr         manager.Manager
	clusterName string
	name        string
	ctrlOptions brokercontroller.Options

	globalPredicates       []predicate.Predicate
	globalTargetPredicates []predicate.Predicate

	filterNoTargetNamespace bool

	forInput ForInput

	watchTargetNamespaceCreated bool

	ownsInput       []OwnsInput
	ownsTargetInput []OwnsTargetInput

	referencesViaFieldInput       []ReferencesViaFieldInput
	referencesTargetViaFieldInput []ReferencesTargetViaFieldInput

	watchesInput       []WatchesInput
	watchesTargetInput []WatchesTargetInput
}

type ForInput struct {
	object     client.Object
	predicates []predicate.Predicate
	err        error
}

type OwnsInput struct {
	object     client.Object
	predicates []predicate.Predicate
}

type OwnsTargetInput struct {
	object     client.Object
	predicates []predicate.Predicate
}

type ReferencesViaFieldInput struct {
	object     client.Object
	field      string
	predicates []predicate.Predicate
}

type ReferencesTargetViaFieldInput struct {
	object     client.Object
	field      string
	predicates []predicate.Predicate
}

type WatchesInput struct {
	src          source.Source
	eventHandler handler.EventHandler
	predicates   []predicate.Predicate
}

type WatchesTargetInput struct {
	src          source.Source
	eventHandler handler.EventHandler
	predicates   []predicate.Predicate
}

type ForOption interface {
	ApplyToFor(o *ForInput)
}

type OwnsOption interface {
	ApplyToOwns(o *OwnsInput)
}

type OwnsTargetOption interface {
	ApplyToOwnsTarget(o *OwnsTargetInput)
}

type ReferencesViaFieldOption interface {
	ApplyToReferencesViaField(o *ReferencesViaFieldInput)
}

type ReferencesTargetViaFieldOption interface {
	ApplyToReferencesTargetViaField(o *ReferencesTargetViaFieldInput)
}

type WatchesOption interface {
	ApplyToWatches(o *WatchesInput)
}

type WatchesTargetOption interface {
	ApplyToWatchesTarget(o *WatchesTargetInput)
}

func ControllerManagedBy(mgr manager.Manager, clusterName string) *Builder {
	return &Builder{
		mgr:         mgr,
		clusterName: clusterName,
	}
}

type Predicates struct {
	predicates []predicate.Predicate
}

func WithPredicates(predicates ...predicate.Predicate) Predicates {
	return Predicates{predicates}
}

func (p Predicates) ApplyToFor(o *ForInput) {
	o.predicates = p.predicates
}

func (p Predicates) ApplyToOwns(o *OwnsInput) {
	o.predicates = p.predicates
}

func (p Predicates) ApplyToOwnsTarget(o *OwnsTargetInput) {
	o.predicates = p.predicates
}

func (p Predicates) ApplyToReferencesViaField(o *ReferencesViaFieldInput) {
	o.predicates = p.predicates
}

func (p Predicates) ApplyToReferencesTargetViaField(o *ReferencesTargetViaFieldInput) {
	o.predicates = p.predicates
}

func (p Predicates) ApplyToWatches(o *WatchesInput) {
	o.predicates = p.predicates
}

func (p Predicates) ApplyToWatchesTarget(o *WatchesTargetInput) {
	o.predicates = p.predicates
}

func (b *Builder) WithEventFilter(p predicate.Predicate) *Builder {
	b.globalPredicates = append(b.globalPredicates, p)
	return b
}

func (b *Builder) WithTargetEventFilter(p predicate.Predicate) *Builder {
	b.globalTargetPredicates = append(b.globalTargetPredicates, p)
	return b
}

func (b *Builder) FilterNoTargetNamespace() *Builder {
	b.filterNoTargetNamespace = true
	return b
}

func (b *Builder) Named(name string) *Builder {
	b.name = name
	return b
}

func (b *Builder) WithOptions(opts brokercontroller.Options) *Builder {
	b.ctrlOptions = opts
	return b
}

func (b *Builder) For(object client.Object, opts ...ForOption) *Builder {
	if b.forInput.object != nil {
		b.forInput.err = fmt.Errorf("may call For() only once")
		return b
	}

	input := ForInput{
		object: object,
	}
	for _, opt := range opts {
		opt.ApplyToFor(&input)
	}
	b.forInput = input
	return b
}

func (b *Builder) WatchTargetNamespaceCreated() *Builder {
	b.watchTargetNamespaceCreated = true
	return b
}

func (b *Builder) Owns(object client.Object, opts ...OwnsOption) *Builder {
	input := OwnsInput{
		object: object,
	}
	for _, opt := range opts {
		opt.ApplyToOwns(&input)
	}
	b.ownsInput = append(b.ownsInput, input)
	return b
}

func (b *Builder) OwnsTarget(object client.Object, opts ...OwnsTargetOption) *Builder {
	input := OwnsTargetInput{
		object: object,
	}
	for _, opt := range opts {
		opt.ApplyToOwnsTarget(&input)
	}
	b.ownsTargetInput = append(b.ownsTargetInput, input)
	return b
}

func (b *Builder) ReferencesViaField(object client.Object, field string, opts ...ReferencesViaFieldOption) *Builder {
	input := ReferencesViaFieldInput{
		object: object,
		field:  field,
	}
	for _, opt := range opts {
		opt.ApplyToReferencesViaField(&input)
	}
	b.referencesViaFieldInput = append(b.referencesViaFieldInput, input)
	return b
}

func (b *Builder) ReferencesTargetViaField(object client.Object, field string, opts ...ReferencesTargetViaFieldOption) *Builder {
	input := ReferencesTargetViaFieldInput{
		object: object,
		field:  field,
	}
	for _, opt := range opts {
		opt.ApplyToReferencesTargetViaField(&input)
	}
	b.referencesTargetViaFieldInput = append(b.referencesTargetViaFieldInput, input)
	return b
}

func (b *Builder) Watches(src source.Source, eventHandler handler.EventHandler, opts ...WatchesOption) *Builder {
	input := WatchesInput{
		src:          src,
		eventHandler: eventHandler,
	}
	for _, opt := range opts {
		opt.ApplyToWatches(&input)
	}
	b.watchesInput = append(b.watchesInput, input)
	return b
}

func (b *Builder) WatchesTarget(src source.Source, eventHandler handler.EventHandler, opts ...WatchesTargetOption) *Builder {
	input := WatchesTargetInput{
		src:          src,
		eventHandler: eventHandler,
	}
	for _, opt := range opts {
		opt.ApplyToWatchesTarget(&input)
	}
	b.watchesTargetInput = append(b.watchesTargetInput, input)
	return b
}

func joinPredicates(prcts ...[]predicate.Predicate) []predicate.Predicate {
	var res []predicate.Predicate
	for _, prct := range prcts {
		res = append(res, prct...)
	}
	return res
}

func (b *Builder) getControllerName(gvk schema.GroupVersionKind) string {
	if b.name != "" {
		return b.name
	}
	return strings.ToLower(gvk.Kind)
}

func (b *Builder) newController(r reconcile.Reconciler) (brokercontroller.Controller, error) {
	globalOpts := b.mgr.GetControllerOptions()

	ctrlOptions := b.ctrlOptions
	if ctrlOptions.Reconciler == nil {
		ctrlOptions.Reconciler = r
	}

	gvk, err := apiutil.GVKForObject(b.forInput.object, b.mgr.GetScheme())
	if err != nil {
		return nil, err
	}

	if ctrlOptions.MaxConcurrentReconciles == 0 {
		gk := gvk.GroupKind().String()
		if concurrency, ok := globalOpts.GroupKindConcurrency[gk]; ok && concurrency > 0 {
			ctrlOptions.MaxConcurrentReconciles = concurrency
		}
	}

	if ctrlOptions.CacheSyncTimeout == 0 && globalOpts.CacheSyncTimeout != nil {
		ctrlOptions.CacheSyncTimeout = *globalOpts.CacheSyncTimeout
	}

	controllerName := b.getControllerName(gvk)

	if ctrlOptions.LogConstructor == nil {
		log := b.mgr.GetLogger().WithValues(
			"controller", controllerName,
			"controllerGroup", gvk.Group,
			"controllerKind", gvk.Kind,
		)

		lowerCamelCaseKind := strings.ToLower(gvk.Kind[:1]) + gvk.Kind[1:]
		ctrlOptions.LogConstructor = func(req *reconcile.Request) logr.Logger {
			log := log
			if req != nil {
				log = log.WithValues(
					lowerCamelCaseKind, klog.KRef(req.Namespace, req.Name),
					"namespace", req.Namespace, "name", req.Name,
				)
			}
			return log
		}
	}

	return brokercontroller.New(controllerName, b.mgr, ctrlOptions)
}

func (b *Builder) predicates(predicates []predicate.Predicate) []predicate.Predicate {
	if b.filterNoTargetNamespace {
		return joinPredicates(b.globalPredicates, []predicate.Predicate{
			&brokerpredicate.FilterNoTargetNamespacePredicate{
				ClusterName: b.clusterName,
			},
		}, predicates)
	}
	return joinPredicates(b.globalPredicates, predicates)
}

func (b *Builder) targetPredicates(predicates []predicate.Predicate) []predicate.Predicate {
	return joinPredicates(b.globalTargetPredicates, predicates)
}

func (b *Builder) doWatch(c brokercontroller.Controller) error {
	src := &source.Kind{Type: b.forInput.object}
	hdler := &handler.EnqueueRequestForObject{}
	allPredicates := b.predicates(b.forInput.predicates)
	if err := c.Watch(src, hdler, allPredicates...); err != nil {
		return err
	}

	if b.watchTargetNamespaceCreated {
		src := &source.Kind{Type: &corev1.Namespace{}}
		hdler := &brokercorehandler.EnqueueRequestForBrokerOwnerOnNamespaceCreated{
			ClusterName: b.clusterName,
			OwnerType:   b.forInput.object,
		}
		allPredicates := b.targetPredicates(nil)
		if err := c.WatchTarget(src, hdler, allPredicates...); err != nil {
			return err
		}
	}

	for _, own := range b.ownsInput {
		src := &source.Kind{Type: own.object}
		hdler := &handler.EnqueueRequestForOwner{
			OwnerType:    b.forInput.object,
			IsController: true,
		}
		allPredicates := b.predicates(own.predicates)
		if err := c.Watch(src, hdler, allPredicates...); err != nil {
			return err
		}
	}

	for _, ownTarget := range b.ownsTargetInput {
		src := &source.Kind{Type: ownTarget.object}
		hdler := &brokerhandler.EnqueueRequestForBrokerOwner{
			OwnerType:    b.forInput.object,
			IsController: true,
			ClusterName:  b.clusterName,
		}
		allPredicates := b.targetPredicates(ownTarget.predicates)
		if err := c.WatchTarget(src, hdler, allPredicates...); err != nil {
			return err
		}
	}

	for _, ref := range b.referencesViaFieldInput {
		src := &source.Kind{Type: ref.object}
		hdler := &poollethandler.EnqueueRequestByFieldReference{
			Type:  b.forInput.object,
			Field: ref.field,
		}
		allPredicates := b.predicates(ref.predicates)
		if err := c.Watch(src, hdler, allPredicates...); err != nil {
			return err
		}
	}

	for _, refTarget := range b.referencesTargetViaFieldInput {
		src := &source.Kind{Type: refTarget.object}
		hdler := &brokerhandler.EnqueueRequestForBrokerOwnerByFieldReference{
			ClusterName:          b.clusterName,
			OwnerAndReferentType: b.forInput.object,
			IsController:         true,
			Field:                refTarget.field,
		}
		allPredicates := b.targetPredicates(refTarget.predicates)
		if err := c.WatchTarget(src, hdler, allPredicates...); err != nil {
			return err
		}
	}

	for _, watch := range b.watchesInput {
		src := watch.src
		hdler := watch.eventHandler
		allPredicates := b.predicates(watch.predicates)
		if err := c.Watch(src, hdler, allPredicates...); err != nil {
			return err
		}
	}

	for _, watchTarget := range b.watchesTargetInput {
		src := watchTarget.src
		hdler := watchTarget.eventHandler
		allPredicates := b.targetPredicates(watchTarget.predicates)
		if err := c.WatchTarget(src, hdler, allPredicates...); err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) Build(r reconcile.Reconciler) (controller.Controller, error) {
	if b.mgr == nil {
		return nil, fmt.Errorf("must specify manager")
	}
	if r == nil {
		return nil, fmt.Errorf("must specify reconciler")
	}
	if b.clusterName == "" {
		return nil, fmt.Errorf("must specify non-empty target name")
	}
	if b.forInput.err != nil {
		return nil, b.forInput.err
	}
	if b.forInput.object == nil {
		return nil, fmt.Errorf("must provide an object for reconciliation")
	}

	c, err := b.newController(r)
	if err != nil {
		return nil, err
	}

	if err := b.doWatch(c); err != nil {
		return nil, err
	}

	return c, nil
}

func (b *Builder) Complete(r reconcile.Reconciler) error {
	_, err := b.Build(r)
	return err
}
