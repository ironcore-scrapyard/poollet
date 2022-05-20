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
	brokerclient "github.com/onmetal/poollet/broker/client"
	poollethandler "github.com/onmetal/poollet/handler"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var enqueueRequestForTypeByDependentFieldReferenceLog = logf.Log.WithName("eventhandler").WithName("EnqueueRequestForTypeByDependentFieldReference")

type EnqueueRequestForTypeByDependentFieldReference struct {
	Type  client.Object
	Field string

	client  brokerclient.Client
	typeGVK schema.GroupVersionKind
	mapper  meta.RESTMapper
}

func (e *EnqueueRequestForTypeByDependentFieldReference) InjectBrokerClient(c brokerclient.Client) error {
	e.client = c
	var err error

	e.typeGVK, err = apiutil.GVKForObject(e.Type, c.Scheme())
	if err != nil {
		return err
	}

	return nil
}

func (e *EnqueueRequestForTypeByDependentFieldReference) InjectMapper(mapper meta.RESTMapper) error {
	e.mapper = mapper
	return nil
}

func (e *EnqueueRequestForTypeByDependentFieldReference) handle(obj client.Object, reqs poollethandler.RequestSet) {
	log := enqueueRequestForTypeByDependentFieldReferenceLog.WithValues(
		"Type", e.typeGVK.String(),
		"Field", e.Field,
	)

	refs, err := e.client.ExtractField(obj, e.Field)
	if err != nil {
		log.Error(err, "Error extracting field")
		return
	}

	mapping, err := e.mapper.RESTMapping(e.typeGVK.GroupKind(), e.typeGVK.Version)
	if err != nil {
		log.Error(err, "Error determining rest mapping")
		return
	}

	for _, name := range refs {
		req := reconcile.Request{
			NamespacedName: client.ObjectKey{
				Name: name,
			},
		}
		if mapping.Scope.Name() != meta.RESTScopeNameRoot {
			req.Namespace = obj.GetNamespace()
		}

		reqs.Insert(req)
	}
}

func (e *EnqueueRequestForTypeByDependentFieldReference) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	reqs := poollethandler.NewRequestSet()
	e.handle(event.Object, reqs)
	poollethandler.EnqueueRequestSet(reqs, queue)
}

func (e *EnqueueRequestForTypeByDependentFieldReference) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	reqs := poollethandler.NewRequestSet()
	e.handle(event.ObjectOld, reqs)
	e.handle(event.ObjectNew, reqs)
	poollethandler.EnqueueRequestSet(reqs, queue)
}

func (e *EnqueueRequestForTypeByDependentFieldReference) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	reqs := poollethandler.NewRequestSet()
	e.handle(event.Object, reqs)
	poollethandler.EnqueueRequestSet(reqs, queue)
}

func (e *EnqueueRequestForTypeByDependentFieldReference) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
	reqs := poollethandler.NewRequestSet()
	e.handle(event.Object, reqs)
	poollethandler.EnqueueRequestSet(reqs, queue)
}
