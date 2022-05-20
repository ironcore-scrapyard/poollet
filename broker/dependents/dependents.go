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
	"errors"

	"github.com/onmetal/poollet/broker/builder"
	brokerclient "github.com/onmetal/poollet/broker/client"
	brokerhandler "github.com/onmetal/poollet/broker/handler"
	poolletmeta "github.com/onmetal/poollet/meta"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type Dependent struct {
	Type       client.Object
	Field      string
	Predicates []predicate.Predicate
}

type Dependents struct {
	dependents []Dependent
}

func (d *Dependents) Dependent(referencedType client.Object, field string, prct ...predicate.Predicate) {
	d.dependents = append(d.dependents, Dependent{
		Type:       referencedType,
		Field:      field,
		Predicates: prct,
	})
}

func (d *Dependents) IsReferenced(ctx context.Context, r client.Reader, c brokerclient.Client, obj client.Object) (bool, error) {
	ok, err := d.isReferencedCached(ctx, c, obj)
	if err != nil || ok {
		return ok, err
	}

	return d.isReferencedLive(ctx, r, obj, c, c.Scheme())
}

func (d *Dependents) isReferencedCached(ctx context.Context, c client.Client, obj client.Object) (bool, error) {
	for _, dependent := range d.dependents {
		dependentList, err := poolletmeta.NewListForObject(dependent.Type, c.Scheme())
		if err != nil {
			return false, err
		}

		if err := c.List(ctx, dependentList,
			client.InNamespace(obj.GetNamespace()),
			client.MatchingFields{dependent.Field: obj.GetName()},
			client.Limit(1),
		); err != nil {
			return false, err
		}

		if meta.LenList(dependentList) > 0 {
			return true, nil
		}
	}

	return false, nil
}

func (d *Dependents) isReferencedLive(ctx context.Context, r client.Reader, obj client.Object, ex brokerclient.FieldExtractor, scheme *runtime.Scheme) (bool, error) {
	var (
		actualName    = obj.GetName()
		errReferenced = errors.New("referenced")
	)
	for _, dependent := range d.dependents {
		dependentList, err := poolletmeta.NewListForObject(dependent.Type, scheme)
		if err != nil {
			return false, err
		}

		if err := r.List(ctx, dependentList,
			client.InNamespace(obj.GetNamespace()),
		); err != nil {
			return false, err
		}

		if err := poolletmeta.EachListItem(dependentList, func(obj client.Object) error {
			refs, err := ex.ExtractField(obj, dependent.Field)
			if err != nil {
				return err
			}

			for _, ref := range refs {
				if ref == actualName {
					return errReferenced
				}
			}
			return nil
		}); err != nil {
			if !errors.Is(err, errReferenced) {
				return false, err
			}
			return true, nil
		}
	}

	return false, nil
}

func (d *Dependents) WatchDynamicReferences(b *builder.Builder, object client.Object) {
	for _, dependent := range d.dependents {
		dependent := dependent
		b.Watches(
			&source.Kind{Type: dependent.Type},
			&brokerhandler.EnqueueRequestForTypeByDependentFieldReference{
				Type:  object,
				Field: dependent.Field,
			},
			builder.WithPredicates(dependent.Predicates...),
		)
	}
}
