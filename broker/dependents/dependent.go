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

	"github.com/onmetal/poollet/broker/builder"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type Dependent struct {
	ReferentType client.Object

	CachedReferencer Referencer
	LiveReferencer   Referencer

	Handler    handler.EventHandler
	Predicates []predicate.Predicate
}

type Dependents interface {
	Dependent(dependent Dependent)
	IsReferenced(ctx context.Context, referenced client.Object) (bool, error)
}

type Mixin struct {
	dependents []Dependent
}

func (d *Mixin) InjectFunc(f inject.Func) error {
	for _, dependent := range d.dependents {
		if err := f(dependent); err != nil {
			return err
		}
	}
	return nil
}

func (d *Mixin) Dependent(dependent Dependent) {
	d.dependents = append(d.dependents, dependent)
}

func (d *Mixin) IsReferenced(ctx context.Context, referenced client.Object) (bool, error) {
	if ok, err := d.isReferencedCached(ctx, referenced); err != nil || ok {
		return ok, err
	}

	return d.isReferencedLive(ctx, referenced)
}

func (d *Mixin) isReferencedCached(ctx context.Context, referenced client.Object) (bool, error) {
	for _, dependent := range d.dependents {
		if ok, err := dependent.CachedReferencer.References(ctx, referenced); err != nil || ok {
			return ok, err
		}
	}

	return false, nil
}

func (d *Mixin) isReferencedLive(ctx context.Context, referenced client.Object) (bool, error) {
	for _, dependent := range d.dependents {
		if ok, err := dependent.LiveReferencer.References(ctx, referenced); err != nil || ok {
			return ok, err
		}
	}

	return false, nil
}

func (d *Mixin) WatchDynamicReferences(b *builder.Builder, mgr ctrl.Manager) error {
	for _, dependent := range d.dependents {
		dependent := dependent
		if err := mgr.SetFields(dependent.CachedReferencer); err != nil {
			return err
		}
		if err := mgr.SetFields(dependent.LiveReferencer); err != nil {
			return err
		}

		b.Watches(
			&source.Kind{Type: dependent.ReferentType},
			dependent.Handler,
			builder.WithPredicates(dependent.Predicates...),
		)
	}
	return nil
}
