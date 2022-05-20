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
	brokermeta "github.com/onmetal/poollet/broker/meta"
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

var enqueueByBrokerOwnerLog = logf.Log.WithName("eventhandler").WithName("EnqueueRequestForBrokerOwner")

type EnqueueRequestForBrokerOwner struct {
	// ClusterName is the name of the cluster the owner is originating from.
	ClusterName string
	// OwnerType is the type of the Owner object to look for in ParentOwnerReferences. Only Group and Kind are compared.
	OwnerType client.Object
	// IsController if set will only look at the first OwnerReference with Controller: true.
	IsController bool
	groupKind    schema.GroupKind
}

func (e *EnqueueRequestForBrokerOwner) InjectScheme(scheme *runtime.Scheme) error {
	return e.parseOwnerTypeGroupKind(scheme)
}

func (e *EnqueueRequestForBrokerOwner) parseOwnerTypeGroupKind(scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(e.OwnerType, scheme)
	if err != nil {
		return err
	}

	e.groupKind = gvk.GroupKind()
	return nil
}

func (e *EnqueueRequestForBrokerOwner) getBrokerOwnerReferences(obj client.Object) []brokermeta.BrokerOwnerReference {
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

func (e *EnqueueRequestForBrokerOwner) getOwnerReconcileRequest(object client.Object, result map[reconcile.Request]struct{}) {
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

func (e *EnqueueRequestForBrokerOwner) enqueueRequests(reqs map[reconcile.Request]struct{}, queue workqueue.RateLimitingInterface) {
	for req := range reqs {
		queue.Add(req)
	}
}

func (e *EnqueueRequestForBrokerOwner) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	reqs := map[reconcile.Request]struct{}{}
	e.getOwnerReconcileRequest(event.Object, reqs)
	e.enqueueRequests(reqs, queue)
}

func (e *EnqueueRequestForBrokerOwner) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	reqs := map[reconcile.Request]struct{}{}
	e.getOwnerReconcileRequest(event.ObjectOld, reqs)
	e.getOwnerReconcileRequest(event.ObjectNew, reqs)
	e.enqueueRequests(reqs, queue)
}

func (e *EnqueueRequestForBrokerOwner) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	reqs := map[reconcile.Request]struct{}{}
	e.getOwnerReconcileRequest(event.Object, reqs)
	e.enqueueRequests(reqs, queue)
}

func (e *EnqueueRequestForBrokerOwner) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
	reqs := map[reconcile.Request]struct{}{}
	e.getOwnerReconcileRequest(event.Object, reqs)
	e.enqueueRequests(reqs, queue)
}
