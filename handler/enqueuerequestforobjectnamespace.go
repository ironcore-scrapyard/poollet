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

package handler

import (
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type EnqueueRequestForObjectNamespace struct{}

func (e *EnqueueRequestForObjectNamespace) handle(obj client.Object, res RequestSet) {
	res.Insert(ctrl.Request{NamespacedName: client.ObjectKey{Name: obj.GetNamespace()}})
}

func (e *EnqueueRequestForObjectNamespace) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	res := NewRequestSet()
	e.handle(event.Object, res)
	EnqueueRequestSet(res, queue)
}

func (e *EnqueueRequestForObjectNamespace) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	res := NewRequestSet()
	e.handle(event.ObjectOld, res)
	e.handle(event.ObjectNew, res)
	EnqueueRequestSet(res, queue)
}

func (e *EnqueueRequestForObjectNamespace) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	res := NewRequestSet()
	e.handle(event.Object, res)
	EnqueueRequestSet(res, queue)
}

func (e *EnqueueRequestForObjectNamespace) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
	res := NewRequestSet()
	e.handle(event.Object, res)
	EnqueueRequestSet(res, queue)
}
