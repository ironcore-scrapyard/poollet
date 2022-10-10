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

package predicate

import (
	"context"
	"fmt"

	mcmeta "github.com/onmetal/poollet/multicluster/meta"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func NameLabelsPredicate(expectedName string, expectedLabels map[string]string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		if expectedName != "" && obj.GetName() != expectedName {
			return false
		}

		return labels.SelectorFromValidatedSet(expectedLabels).Matches(labels.Set(obj.GetLabels()))
	})
}

type FilterNoTargetNamespacePredicate struct {
	ClusterName string
	client      client.Client
}

func (p *FilterNoTargetNamespacePredicate) InjectClient(c client.Client) error {
	p.client = c
	return nil
}

func (p *FilterNoTargetNamespacePredicate) namespaceFrom(ctx context.Context, obj client.Object) (*corev1.Namespace, error) {
	if ns, ok := obj.(*corev1.Namespace); ok {
		return ns, nil
	}

	namespaceName := obj.GetNamespace()
	if namespaceName == "" {
		return nil, fmt.Errorf("object %T does not provide a namespace", obj)
	}

	namespace := &corev1.Namespace{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: namespaceName}, namespace); err != nil {
		return nil, err
	}

	return namespace, nil
}

var filterTargetNamespacePredicateLog = logf.Log.WithName("predicate").WithName("FilterNoTargetNamespacePredicate")

func (p *FilterNoTargetNamespacePredicate) filter(obj client.Object) bool {
	ctx := context.TODO()
	ns, err := p.namespaceFrom(ctx, obj)
	if err != nil {
		filterTargetNamespacePredicateLog.Error(err, "Error getting namespace for object")
		return false
	}

	brokerCtrl := mcmeta.GetControllerOf(ns)
	return brokerCtrl == nil ||
		brokerCtrl.ClusterName != p.ClusterName ||
		brokerCtrl.Kind != "Namespace"
}

func (p *FilterNoTargetNamespacePredicate) Create(event event.CreateEvent) bool {
	return p.filter(event.Object)
}

func (p *FilterNoTargetNamespacePredicate) Delete(event event.DeleteEvent) bool {
	return p.filter(event.Object)
}

func (p *FilterNoTargetNamespacePredicate) Update(event event.UpdateEvent) bool {
	return p.filter(event.ObjectNew)
}

func (p *FilterNoTargetNamespacePredicate) Generic(event event.GenericEvent) bool {
	return p.filter(event.Object)
}
