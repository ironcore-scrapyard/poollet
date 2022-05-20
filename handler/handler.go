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
	poolletmeta "github.com/onmetal/poollet/meta"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type RequestSet map[reconcile.Request]struct{}

func NewRequestSet(items ...reconcile.Request) RequestSet {
	s := make(RequestSet)
	s.Insert(items...)
	return s
}

func (s RequestSet) Insert(items ...reconcile.Request) RequestSet {
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}

func (s RequestSet) UnsortedList() []reconcile.Request {
	res := make([]reconcile.Request, 0, len(s))
	for item := range s {
		res = append(res, item)
	}
	return res
}

func InsertObjectRequest(s RequestSet, obj client.Object) {
	s.Insert(reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
}

func InsertListRequests(s RequestSet, list client.ObjectList) error {
	return poolletmeta.EachListItem(list, func(obj client.Object) error {
		InsertObjectRequest(s, obj)
		return nil
	})
}

func EnqueueRequestSet(s RequestSet, queue workqueue.RateLimitingInterface) {
	for req := range s {
		queue.Add(req)
	}
}
