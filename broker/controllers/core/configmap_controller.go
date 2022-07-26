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

package core

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	"github.com/onmetal/poollet/broker"
	brokerclient "github.com/onmetal/poollet/broker/client"
	brokerreconcile "github.com/onmetal/poollet/broker/dependents"
	"github.com/onmetal/poollet/broker/domain"
	brokererrors "github.com/onmetal/poollet/broker/errors"
	"github.com/onmetal/poollet/broker/provider"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type ConfigMapReconciler struct {
	brokerreconcile.Dependents

	Provider provider.Provider

	brokerclient.Client
	APIReader    client.Reader
	TargetClient brokerclient.Client
	Scheme       *runtime.Scheme

	ClusterName string
	PoolName    string
	Domain      domain.Domain
}

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).V(1)
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, configMap); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, configMap)
}

func (r *ConfigMapReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.PoolName)
}

func (r *ConfigMapReconciler) finalizer() string {
	return r.domain().Slash("configmap")
}

func (r *ConfigMapReconciler) reconcileExists(ctx context.Context, log logr.Logger, configMap *corev1.ConfigMap) (ctrl.Result, error) {
	if !configMap.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, configMap)
	}

	ok, err := r.IsReferenced(ctx, r.APIReader, r.Client, configMap)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return r.delete(ctx, log, configMap)
	}

	return r.reconcile(ctx, log, configMap)
}

func (r *ConfigMapReconciler) delete(ctx context.Context, log logr.Logger, configMap *corev1.ConfigMap) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(configMap, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, configMap)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}
	log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Deleting target if exists")
	targetConfigMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: targetNamespace, Name: configMap.Name}}
	existed, err := clientutils.DeleteIfExists(ctx, r.TargetClient, targetConfigMap)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting target: %w", err)
	}

	if existed {
		log.V(1).Info("Issued target deletion")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Target gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, configMap, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Removed finalizer")
	return ctrl.Result{}, nil
}

func (r *ConfigMapReconciler) reconcile(ctx context.Context, log logr.Logger, configMap *corev1.ConfigMap) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, configMap)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}
	log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Ensuring finalizer")
	requeue, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, configMap, r.finalizer())
	if err != nil || requeue {
		return ctrl.Result{Requeue: requeue}, err
	}

	targetConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      configMap.Name,
		},
		Data: configMap.Data,
	}
	log.V(1).Info("Applying target")
	if _, err := brokerclient.BrokerControlledCreateOrPatch(ctx, r.TargetClient, r.ClusterName, configMap, targetConfigMap, func() error {
		targetConfigMap.Data = configMap.Data
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("error applying target: %w", err)
	}

	log.V(1).Info("Applied target")
	return ctrl.Result{}, nil
}

func (r *ConfigMapReconciler) Target(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	targetConfigMap := obj.(*corev1.ConfigMap)

	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, key, configMap); err != nil {
		return err
	}

	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, configMap)
	if err != nil {
		return err
	}

	targetConfigMapKey := client.ObjectKey{Namespace: targetNamespace, Name: key.Name}
	if err := r.TargetClient.Get(ctx, targetConfigMapKey, targetConfigMap); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return brokererrors.NewNotSynced(corev1.Resource("configmaps"), key.String())
	}

	return nil
}

func (r *ConfigMapReconciler) SetupWithManager(mgr broker.Manager) error {
	b := broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&corev1.ConfigMap{}).
		OwnsTarget(&corev1.ConfigMap{})

	r.WatchDynamicReferences(b, &corev1.ConfigMap{})

	return b.Complete(r)
}
