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

	"github.com/onmetal/controller-utils/metautils"
	brokercluster "github.com/onmetal/poollet/broker/cluster"
	poollethandler "github.com/onmetal/poollet/handler"
	mcmeta "github.com/onmetal/poollet/multicluster/meta"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var enqueueBrokerOwnerOnNamespaceCreatedLog = logf.Log.WithName("eventhandler").WithName("EnqueueRequestForBrokerOwnerOnNamespaceCreated")

type EnqueueRequestForBrokerOwnerOnNamespaceCreated struct {
	ClusterName string
	OwnerType   client.Object
	groupKind   schema.GroupKind
	baseList    client.ObjectList
	client      client.Client
}

func (e *EnqueueRequestForBrokerOwnerOnNamespaceCreated) InjectCluster(cluster brokercluster.Cluster) error {
	e.client = cluster.GetClient()
	if err := e.parseOwnerTypeGroupKind(cluster.GetScheme()); err != nil {
		return err
	}
	if err := e.initializeBaseList(cluster.GetScheme()); err != nil {
		return err
	}
	return nil
}

func (e *EnqueueRequestForBrokerOwnerOnNamespaceCreated) parseOwnerTypeGroupKind(scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(e.OwnerType, scheme)
	if err != nil {
		return err
	}

	e.groupKind = gvk.GroupKind()
	return nil
}

func (e *EnqueueRequestForBrokerOwnerOnNamespaceCreated) initializeBaseList(scheme *runtime.Scheme) error {
	var err error
	e.baseList, err = metautils.NewListForObject(scheme, e.OwnerType)
	return err
}

func (e *EnqueueRequestForBrokerOwnerOnNamespaceCreated) handle(object client.Object, res poollethandler.RequestSet) {
	ctx := context.TODO()
	ns := object.(*corev1.Namespace)

	brokerCtrl := mcmeta.GetControllerOf(ns)
	if brokerCtrl == nil ||
		brokerCtrl.ClusterName != e.ClusterName ||
		brokerCtrl.Kind != "Namespace" {
		return
	}

	gv, err := schema.ParseGroupVersion(brokerCtrl.APIVersion)
	if err != nil {
		enqueueBrokerOwnerOnNamespaceCreatedLog.Error(err, "Error parsing api version")
		return
	}

	if gv.Group != corev1.GroupName {
		return
	}

	list := e.baseList.DeepCopyObject().(client.ObjectList)
	if err := e.client.List(ctx, list,
		client.InNamespace(brokerCtrl.Name),
	); err != nil {
		enqueueBrokerOwnerOnNamespaceCreatedLog.Error(err, "Error listing objects in source namespace")
	}

	if err := poollethandler.InsertListRequests(res, list); err != nil {
		enqueueBrokerOwnerOnNamespaceCreatedLog.Error(err, "Error inserting list requests")
	}
}

func (e *EnqueueRequestForBrokerOwnerOnNamespaceCreated) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	res := poollethandler.NewRequestSet()
	e.handle(event.Object, res)
	poollethandler.EnqueueRequestSet(res, queue)
}

func (e *EnqueueRequestForBrokerOwnerOnNamespaceCreated) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
}
func (e *EnqueueRequestForBrokerOwnerOnNamespaceCreated) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
}
func (e *EnqueueRequestForBrokerOwnerOnNamespaceCreated) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
}
