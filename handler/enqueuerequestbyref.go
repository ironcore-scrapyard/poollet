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
	"context"
	"fmt"

	"github.com/onmetal/controller-utils/metautils"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var enqueueRequestByFieldReferenceLog = logf.Log.WithName("handler").WithName("EnqueueRequestByFieldReference")

type EnqueueRequestByFieldReference struct {
	ReferentType client.Object
	Field        string

	client           client.Client
	baseReferentList client.ObjectList
	referentGVK      schema.GroupVersionKind
}

func (e *EnqueueRequestByFieldReference) InjectClient(c client.Client) error {
	e.client = c

	var err error
	e.baseReferentList, err = metautils.NewListForObject(e.client.Scheme(), e.ReferentType)
	if err != nil {
		return fmt.Errorf("error getting list for object: %w", err)
	}

	e.referentGVK, err = apiutil.GVKForObject(e.ReferentType, e.client.Scheme())
	if err != nil {
		return err
	}

	return nil
}

func (e *EnqueueRequestByFieldReference) handle(obj client.Object, reqs RequestSet) {
	ctx := context.TODO()
	log := enqueueRequestByFieldReferenceLog.WithValues("ReferentType", e.referentGVK.GroupKind())

	list := e.baseReferentList.DeepCopyObject().(client.ObjectList)

	if err := e.client.List(ctx, list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{
			e.Field: obj.GetName(),
		},
	); err != nil {
		log.Error(err, "Error listing objects by field")
		return
	}

	if err := InsertListRequests(reqs, list); err != nil {
		log.Error(err, "Error inserting list requests")
	}
}

func (e *EnqueueRequestByFieldReference) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	reqs := NewRequestSet()
	e.handle(event.Object, reqs)
	EnqueueRequestSet(reqs, queue)
}

func (e *EnqueueRequestByFieldReference) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	reqs := NewRequestSet()
	e.handle(event.ObjectOld, reqs)
	e.handle(event.ObjectNew, reqs)
	EnqueueRequestSet(reqs, queue)
}

func (e *EnqueueRequestByFieldReference) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	reqs := NewRequestSet()
	e.handle(event.Object, reqs)
	EnqueueRequestSet(reqs, queue)
}

func (e *EnqueueRequestByFieldReference) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
	reqs := NewRequestSet()
	e.handle(event.Object, reqs)
	EnqueueRequestSet(reqs, queue)
}
