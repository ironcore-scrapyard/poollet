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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type EnqueueRequestForParentObject struct {
	ParentNamespaceAnnotation string
	ParentNameAnnotation      string
}

func (e *EnqueueRequestForParentObject) getParentReconcileRequests(obj client.Object, reqs map[reconcile.Request]struct{}) {
	annotations := obj.GetAnnotations()
	namespace, ok := annotations[e.ParentNamespaceAnnotation]
	if !ok {
		return
	}
	name, ok := annotations[e.ParentNameAnnotation]
	if !ok {
		return
	}

	reqs[reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}] = struct{}{}
}

func (e *EnqueueRequestForParentObject) enqueueRequests(reqs map[reconcile.Request]struct{}, queue workqueue.RateLimitingInterface) {
	for req := range reqs {
		queue.Add(req)
	}
}

func (e *EnqueueRequestForParentObject) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	reqs := make(map[reconcile.Request]struct{})
	e.getParentReconcileRequests(event.Object, reqs)
	e.enqueueRequests(reqs, queue)
}

func (e *EnqueueRequestForParentObject) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	reqs := make(map[reconcile.Request]struct{})
	e.getParentReconcileRequests(event.ObjectOld, reqs)
	e.getParentReconcileRequests(event.ObjectNew, reqs)
	e.enqueueRequests(reqs, queue)
}

func (e *EnqueueRequestForParentObject) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	reqs := make(map[reconcile.Request]struct{})
	e.getParentReconcileRequests(event.Object, reqs)
	e.enqueueRequests(reqs, queue)
}

func (e *EnqueueRequestForParentObject) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
	reqs := make(map[reconcile.Request]struct{})
	e.getParentReconcileRequests(event.Object, reqs)
	e.enqueueRequests(reqs, queue)
}
