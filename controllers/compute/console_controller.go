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

package compute

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	partitionlethandler "github.com/onmetal/partitionlet/handler"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"sigs.k8s.io/controller-runtime/pkg/cache"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	commonv1alpha1 "github.com/onmetal/onmetal-api/apis/common/v1alpha1"

	partitionletcomputev1alpha1 "github.com/onmetal/partitionlet/apis/compute/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-logr/logr"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	consoleFinalizer                                    = "partitionlet.onmetal.de/console"
	consoleFieldOwner                                   = client.FieldOwner("partitionlet.onmetal.de/console")
	consoleSpecLighthouseClientConfigKeySecretNameField = ".console.spec.lighthouseClientConfig.keySecret.name"
)

type ConsoleReconciler struct {
	Scheme *runtime.Scheme
	client.Client
	ParentClient client.Client

	ParentFieldIndexer client.FieldIndexer
	ParentCache        cache.Cache

	Namespace       string
	MachinePoolName string
}

func (r *ConsoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	parentConsole := &computev1alpha1.Console{}
	if err := r.ParentClient.Get(ctx, req.NamespacedName, parentConsole); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, parentConsole)
}

func (r *ConsoleReconciler) reconcileExists(ctx context.Context, log logr.Logger, parentConsole *computev1alpha1.Console) (ctrl.Result, error) {
	if !parentConsole.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, parentConsole)
	}
	return r.reconcile(ctx, log, parentConsole)
}

func (r *ConsoleReconciler) delete(ctx context.Context, log logr.Logger, parentConsole *computev1alpha1.Console) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(parentConsole, consoleFinalizer) {
		return ctrl.Result{}, nil
	}

	objs := []client.Object{
		&computev1alpha1.Console{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: r.Namespace,
				Name:      partitionletcomputev1alpha1.ConsoleName(parentConsole.Namespace, parentConsole.Name),
			},
		},
	}
	if parentConsole.Spec.LighthouseClientConfig != nil {
		objs = append(objs, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: r.Namespace,
				Name:      partitionletcomputev1alpha1.ConsoleSecretName(parentConsole.Namespace, parentConsole.Name),
			},
		})
	}
	var goneCt int
	for _, obj := range objs {
		if err := r.ParentClient.Delete(ctx, obj); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("error deleting object: %w", err)
			}
			goneCt++
		}
	}

	if goneCt < len(objs) {
		log.Info("Not all objects are gone, requeueing")
		return reconcile.Result{Requeue: true}, nil
	}

	log.V(1).Info("Releasing finalizer")
	baseParentConsole := parentConsole.DeepCopy()
	controllerutil.RemoveFinalizer(parentConsole, consoleFinalizer)
	if err := r.ParentClient.Patch(ctx, parentConsole, client.MergeFrom(baseParentConsole)); err != nil {
		return ctrl.Result{}, fmt.Errorf("error releasing finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *ConsoleReconciler) lighthouseClientConfig(ctx context.Context, log logr.Logger, parentConsole *computev1alpha1.Console) (*computev1alpha1.ConsoleClientConfig, error) {
	if parentConsole.Spec.LighthouseClientConfig == nil {
		return nil, nil
	}

	parentLighthouseClientConfig := parentConsole.Spec.LighthouseClientConfig
	var keySecretRef *commonv1alpha1.SecretKeySelector
	if parentKeySecretRef := parentLighthouseClientConfig.KeySecret; parentKeySecretRef != nil {
		parentKeySecret := &corev1.Secret{}
		if err := r.ParentClient.Get(ctx, client.ObjectKey{Namespace: parentConsole.Namespace, Name: parentKeySecretRef.Name}, parentKeySecret); err != nil {
			return nil, fmt.Errorf("error getting parent key secret: %w", err)
		}

		key := parentKeySecretRef.Key
		if key == "" {
			key = computev1alpha1.DefaultKeySecretKey
		}
		keySecret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "Secret",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: r.Namespace,
				Name:      partitionletcomputev1alpha1.ConsoleSecretName(parentConsole.Namespace, parentConsole.Name),
			},
			Data: map[string][]byte{
				key: parentKeySecret.Data[key],
			},
		}
		if err := r.Patch(ctx, keySecret, client.Apply, consoleFieldOwner, client.ForceOwnership); err != nil {
			return nil, fmt.Errorf("could not sync parent key secret: %w", err)
		}

		keySecretRef = &commonv1alpha1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: keySecret.Name},
			Key:                  key,
		}
	}

	return &computev1alpha1.ConsoleClientConfig{
		Service:   nil, // TODO: Handle this field
		URL:       parentLighthouseClientConfig.URL,
		CABundle:  parentLighthouseClientConfig.CABundle,
		KeySecret: keySecretRef,
	}, nil
}

func (r *ConsoleReconciler) parentConsoleClientConfig(ctx context.Context, log logr.Logger, parentConsole, console *computev1alpha1.Console) (*computev1alpha1.ConsoleClientConfig, error) {
	if console.Status.ServiceClientConfig == nil {
		return nil, nil
	}

	serviceClientConfig := console.Status.ServiceClientConfig
	var parentKeySecretRef *commonv1alpha1.SecretKeySelector
	if keySecretRef := serviceClientConfig.KeySecret; keySecretRef != nil {
		keySecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: keySecretRef.Name}, keySecret); err != nil {
			return nil, fmt.Errorf("could not get key secret: %w", err)
		}

		key := keySecretRef.Key
		if key == "" {
			key = computev1alpha1.DefaultKeySecretKey
		}
		parentKeySecret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "Secret",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: parentConsole.Namespace,
				Name:      partitionletcomputev1alpha1.ParentConsoleSecretName(parentConsole.Name),
				Annotations: map[string]string{
					partitionletcomputev1alpha1.ConsoleParentNamespaceAnnotation: parentConsole.Namespace,
					partitionletcomputev1alpha1.ConsoleParentNameAnnotation:      parentConsole.Name,
				},
			},
			Data: map[string][]byte{
				key: keySecret.Data[key],
			},
		}
		if err := ctrl.SetControllerReference(parentConsole, parentKeySecret, r.Scheme); err != nil {
			return nil, fmt.Errorf("could not set controller reference on parent key secret: %w", err)
		}
		if err := r.ParentClient.Patch(ctx, parentKeySecret, client.Apply, consoleFieldOwner, client.ForceOwnership); err != nil {
			return nil, fmt.Errorf("error applying parent key secret: %w", err)
		}

		parentKeySecretRef = &commonv1alpha1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: parentKeySecret.Name},
			Key:                  key,
		}
	}
	return &computev1alpha1.ConsoleClientConfig{
		Service:   nil, // TODO: handle this field.
		URL:       serviceClientConfig.URL,
		CABundle:  serviceClientConfig.CABundle,
		KeySecret: parentKeySecretRef,
	}, nil
}

func (r *ConsoleReconciler) reconcile(ctx context.Context, log logr.Logger, parentConsole *computev1alpha1.Console) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(parentConsole, consoleFinalizer) {
		baseParentConsole := parentConsole.DeepCopy()
		controllerutil.AddFinalizer(parentConsole, consoleFinalizer)
		if err := r.ParentClient.Patch(ctx, parentConsole, client.MergeFrom(baseParentConsole)); err != nil {
			return ctrl.Result{}, fmt.Errorf("error adding finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	lighthouseClientConfig, err := r.lighthouseClientConfig(ctx, log, parentConsole)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error computing lighthouse client config: %w", err)
	}

	console := &computev1alpha1.Console{
		TypeMeta: metav1.TypeMeta{
			APIVersion: computev1alpha1.GroupVersion.String(),
			Kind:       "Console",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.Namespace,
			Name:      partitionletcomputev1alpha1.ConsoleName(parentConsole.Namespace, parentConsole.Name),
			Annotations: map[string]string{
				partitionletcomputev1alpha1.ConsoleParentNamespaceAnnotation: parentConsole.Namespace,
				partitionletcomputev1alpha1.ConsoleParentNameAnnotation:      parentConsole.Name,
			},
		},
		Spec: computev1alpha1.ConsoleSpec{
			Type: parentConsole.Spec.Type,
			MachineRef: corev1.LocalObjectReference{
				Name: partitionletcomputev1alpha1.MachineName(parentConsole.Namespace, parentConsole.Spec.MachineRef.Name),
			},
			LighthouseClientConfig: lighthouseClientConfig,
		},
	}
	if err := r.Patch(ctx, console, client.Apply, consoleFieldOwner, client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not apply console: %w", err)
	}

	parentConsoleClientConfig, err := r.parentConsoleClientConfig(ctx, log, parentConsole, console)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error computing parent console client config: %w", err)
	}

	parentConsoleBase := parentConsole.DeepCopy()
	parentConsole.Status.State = console.Status.State
	parentConsole.Status.ServiceClientConfig = parentConsoleClientConfig
	if err := r.ParentClient.Status().Patch(ctx, parentConsole, client.MergeFrom(parentConsoleBase)); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not patch parent console status: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *ConsoleReconciler) SetupWithManager(mgr manager.Manager) error {
	log := ctrl.Log.WithName("console-reconciler")
	ctx := ctrl.LoggerInto(context.Background(), log)

	c, err := controller.New("console", mgr, controller.Options{
		Reconciler: r,
		Log:        mgr.GetLogger().WithName("console"),
	})
	if err != nil {
		return fmt.Errorf("error creating controller: %w", err)
	}

	if err := r.ParentFieldIndexer.IndexField(ctx, &computev1alpha1.Console{}, consoleSpecLighthouseClientConfigKeySecretNameField, func(obj client.Object) []string {
		console := obj.(*computev1alpha1.Console)
		lighthouseClientConfig := console.Spec.LighthouseClientConfig
		if lighthouseClientConfig == nil {
			return nil
		}

		keySecret := lighthouseClientConfig.KeySecret
		if lighthouseClientConfig.KeySecret == nil {
			return nil
		}

		return []string{keySecret.Name}
	}); err != nil {
		return fmt.Errorf("could not setup %s index: %w", consoleSpecLighthouseClientConfigKeySecretNameField, err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&computev1alpha1.Console{}, r.ParentCache),
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			console := obj.(*computev1alpha1.Console)
			machine := &computev1alpha1.Machine{}
			if err := r.ParentClient.Get(ctx, client.ObjectKey{Namespace: console.Namespace, Name: console.Spec.MachineRef.Name}, machine); err != nil {
				log.Error(err, "Error getting parent machine", "Machine", console.Spec.MachineRef.Name)
				return false
			}

			return machine.Spec.MachinePool.Name == r.MachinePoolName
		}),
	); err != nil {
		return fmt.Errorf("error setting up parent console watch: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&corev1.Secret{}, r.ParentCache),
		handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
			list := &computev1alpha1.ConsoleList{}
			if err := r.ParentClient.List(ctx, list, client.MatchingFields{consoleSpecLighthouseClientConfigKeySecretNameField: obj.GetName()}); err != nil {
				log.Error(err, "Error listing consoles using lighthouse client config key secret")
				return nil
			}

			res := make([]reconcile.Request, 0, len(list.Items))
			for _, item := range list.Items {
				res = append(res, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&item)})
			}
			return res
		}),
		predicate.GenerationChangedPredicate{},
	); err != nil {
		return fmt.Errorf("error setting up parent secret watch: %w", err)
	}

	if err := c.Watch(
		&source.Kind{Type: &corev1.Secret{}},
		&partitionlethandler.EnqueueRequestForParentObject{
			ParentNamespaceAnnotation: partitionletcomputev1alpha1.ConsoleParentNamespaceAnnotation,
			ParentNameAnnotation:      partitionletcomputev1alpha1.ConsoleParentNameAnnotation,
		},
	); err != nil {
		return fmt.Errorf("error setting up secret watch: %w", err)
	}

	if err := c.Watch(
		&source.Kind{Type: &computev1alpha1.Console{}},
		&partitionlethandler.EnqueueRequestForParentObject{
			ParentNamespaceAnnotation: partitionletcomputev1alpha1.ConsoleParentNamespaceAnnotation,
			ParentNameAnnotation:      partitionletcomputev1alpha1.ConsoleParentNameAnnotation,
		},
	); err != nil {
		return fmt.Errorf("error setting up console watch: %w", err)
	}

	return nil
}
