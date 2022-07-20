// Copyright 2021 OnMetal authors
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

package handler

import (
	"context"

	"github.com/onmetal/controller-utils/metautils"
	brokermeta "github.com/onmetal/poollet/broker/meta"
	poollethandler "github.com/onmetal/poollet/handler"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var enqueueByBrokerOwnerByFieldReferenceLog = logf.Log.WithName("eventhandler").WithName("EnqueueRequestForBrokerOwnerByFieldReference")

type EnqueueRequestForBrokerOwnerByFieldReference struct {
	// ClusterName is the name of the cluster the broker owner object originates from.
	ClusterName string
	// OwnerAndReferentType is the type of the Owner object to look for in ParentOwnerReferences. Only Group and Kind are compared.
	OwnerAndReferentType client.Object
	// IsController if set will only look at the first OwnerReference with Controller: true.
	IsController bool
	Field        string

	groupKind schema.GroupKind
	baseList  client.ObjectList
	client    client.Client
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) InjectClient(c client.Client) error {
	e.client = c
	if err := e.parseOwnerTypeGroupKind(c.Scheme()); err != nil {
		return err
	}
	if err := e.initializeBaseList(c.Scheme()); err != nil {
		return err
	}
	return nil
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) parseOwnerTypeGroupKind(scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(e.OwnerAndReferentType, scheme)
	if err != nil {
		return err
	}

	e.groupKind = gvk.GroupKind()
	return nil
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) initializeBaseList(scheme *runtime.Scheme) error {
	var err error
	e.baseList, err = metautils.NewListForObject(scheme, e.OwnerAndReferentType)
	return err
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) getBrokerOwnerReferences(obj client.Object) []brokermeta.BrokerOwnerReference {
	if obj == nil {
		return nil
	}

	if !e.IsController {
		return brokermeta.GetBrokerOwnerReferences(obj)
	}

	if ownerRef := brokermeta.GetBrokerControllerOf(obj); ownerRef != nil {
		return []brokermeta.BrokerOwnerReference{*ownerRef}
	}
	return nil
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) getOwnerReconcileRequest(object client.Object, result map[reconcile.Request]struct{}) {
	for _, ref := range e.getBrokerOwnerReferences(object) {
		if ref.ClusterName != e.ClusterName {
			continue
		}

		refGV, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			enqueueByBrokerOwnerLog.Error(err, "Could not parse parent controller reference APIVersion",
				"APIVersion", ref.APIVersion,
			)
			return
		}

		if ref.Kind == e.groupKind.Kind && refGV.Group == e.groupKind.Group {
			result[reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: ref.Namespace,
				Name:      ref.Name,
			}}] = struct{}{}
		}
	}
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) handle(obj client.Object, res poollethandler.RequestSet) {
	ctx := context.TODO()

	list := e.baseList.DeepCopyObject().(client.ObjectList)

	if err := e.client.List(ctx, list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{
			e.Field: obj.GetName(),
		},
	); err != nil {
		enqueueByBrokerOwnerByFieldReferenceLog.Error(err, "Error listing objects by field")
		return
	}

	if err := metautils.EachListItem(list, func(obj client.Object) error {
		e.getOwnerReconcileRequest(obj, res)
		return nil
	}); err != nil {
		enqueueByBrokerOwnerByFieldReferenceLog.Error(err, "Error traversing list")
	}
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	s := poollethandler.NewRequestSet()
	e.handle(event.Object, s)
	poollethandler.EnqueueRequestSet(s, queue)
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	s := poollethandler.NewRequestSet()
	e.handle(event.ObjectOld, s)
	e.handle(event.ObjectNew, s)
	poollethandler.EnqueueRequestSet(s, queue)
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	s := poollethandler.NewRequestSet()
	e.handle(event.Object, s)
	poollethandler.EnqueueRequestSet(s, queue)
}

func (e *EnqueueRequestForBrokerOwnerByFieldReference) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
	s := poollethandler.NewRequestSet()
	e.handle(event.Object, s)
	poollethandler.EnqueueRequestSet(s, queue)
}
