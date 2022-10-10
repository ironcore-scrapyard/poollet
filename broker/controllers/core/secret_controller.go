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

type SecretReconciler struct {
	brokerreconcile.Mixin

	Provider provider.Provider

	brokerclient.Client
	APIReader    client.Reader
	TargetClient brokerclient.Client
	Scheme       *runtime.Scheme

	ClusterName string
	PoolName    string
	Domain      domain.Domain
}

func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, req.NamespacedName, secret); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, secret)
}

func (r *SecretReconciler) domain() domain.Domain {
	return r.Domain.Subdomain(r.PoolName)
}

func (r *SecretReconciler) finalizer() string {
	return r.domain().Slash("secret")
}

func (r *SecretReconciler) reconcileExists(ctx context.Context, log logr.Logger, secret *corev1.Secret) (ctrl.Result, error) {
	if !secret.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, secret)
	}

	ok, err := r.IsReferenced(ctx, secret)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return r.delete(ctx, log, secret)
	}

	return r.reconcile(ctx, log, secret)
}

func (r *SecretReconciler) delete(ctx context.Context, log logr.Logger, secret *corev1.Secret) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(secret, r.finalizer()) {
		log.V(2).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Delete")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, secret)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}
	log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Deleting target if exists")
	targetSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: targetNamespace, Name: secret.Name}}
	existed, err := clientutils.DeleteIfExists(ctx, r.TargetClient, targetSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting target: %w", err)
	}

	if existed {
		log.V(1).Info("Issued target deletion")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Target gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, secret, r.finalizer()); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Removed finalizer")
	return ctrl.Result{}, nil
}

func (r *SecretReconciler) reconcile(ctx context.Context, log logr.Logger, secret *corev1.Secret) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Determining target namespace")
	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, secret)
	if err != nil {
		return ctrl.Result{}, brokererrors.IgnoreNotSynced(err)
	}
	log.WithValues("TargetNamespace", targetNamespace)
	log.V(1).Info("Determined target namespace")

	log.V(1).Info("Ensuring finalizer")
	requeue, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, secret, r.finalizer())
	if err != nil || requeue {
		return ctrl.Result{Requeue: requeue}, err
	}

	targetSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      secret.Name,
		},
	}
	log.V(1).Info("Applying target")
	if _, err := brokerclient.BrokerControlledCreateOrPatch(
		ctx,
		r.TargetClient,
		r.ClusterName,
		secret,
		targetSecret,
		func() error {
			targetSecret.Data = secret.Data
			return nil
		},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("error applying target: %w", err)
	}

	log.V(1).Info("Applied target secret")
	return ctrl.Result{}, nil
}

func (r *SecretReconciler) Target(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	targetSecret := obj.(*corev1.Secret)

	secret := &corev1.Secret{}
	if err := r.Get(ctx, key, secret); err != nil {
		return err
	}

	targetNamespace, err := provider.TargetNamespaceFor(ctx, r.Provider, secret)
	if err != nil {
		return err
	}

	targetSecretKey := client.ObjectKey{Namespace: targetNamespace, Name: key.Name}
	if err := r.TargetClient.Get(ctx, targetSecretKey, targetSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		return brokererrors.NewNotSynced(corev1.Resource("secrets"), key.String())
	}

	return nil
}

func (r *SecretReconciler) SetupWithManager(mgr broker.Manager) error {
	b := broker.NewControllerManagedBy(mgr, r.ClusterName).
		FilterNoTargetNamespace().
		WatchTargetNamespaceCreated().
		For(&corev1.Secret{}).
		OwnsTarget(&corev1.Secret{})

	if err := r.WatchDynamicReferences(b, mgr); err != nil {
		return err
	}

	return b.Complete(r)
}
