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

package strategy

import (
	"fmt"
	"strings"

	partitionletmeta "github.com/onmetal/partitionlet/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Strategy is a strategy to obtain keys for syncing parent objects to the cluster.
type Strategy interface {
	// IsController reports whether the strategy expects to be the sole controller of items.
	IsController() bool
	// Key returns the key for the synced object by giving the parent object.
	Key(parentObject client.Object) (client.ObjectKey, error)
	// Finalizer returns the effective finalizer for the synced object by giving the parent object and finalizer.
	Finalizer(parentObject client.Object, finalizer string) (string, error)
	// SetParentControllerReference sets the parent controller reference on the given object.
	SetParentControllerReference(parentObject, object client.Object, scheme *runtime.Scheme) error
}

// Simple is the default partitionlet strategy, syncing to a specific namespace.
type Simple struct {
	Namespace string
}

// IsController implements Strategy.
func (s Simple) IsController() bool {
	return true
}

// Key implements Strategy.
func (s Simple) Key(parentObject client.Object) (client.ObjectKey, error) {
	return client.ObjectKey{
		Namespace: s.Namespace,
		Name:      fmt.Sprintf("%s--%s", parentObject.GetNamespace(), parentObject.GetName()),
	}, nil
}

// Finalizer implements Strategy.
func (s Simple) Finalizer(parentObject client.Object, finalizer string) (string, error) {
	return finalizer, nil
}

// SetParentControllerReference implements Strategy.
func (s Simple) SetParentControllerReference(parentObject, object client.Object, scheme *runtime.Scheme) error {
	return partitionletmeta.SetParentControllerReference(parentObject, object, scheme)
}

// Broker is a strategy to broker objects from another cluster (usually, via a backward loop).
type Broker struct {
	Fallback Strategy
}

// IsController implements Strategy.
func (s Broker) IsController() bool {
	return false
}

// Key implements Strategy.
func (s Broker) Key(parentObject client.Object) (client.ObjectKey, error) {
	controller := partitionletmeta.GetParentControllerOf(parentObject)
	if controller == nil {
		if s.Fallback != nil {
			return s.Fallback.Key(parentObject)
		}
		return client.ObjectKey{}, fmt.Errorf("could not determine parent controller of %v", parentObject)
	}
	return client.ObjectKey{Namespace: controller.Namespace, Name: controller.Name}, nil
}

// Finalizer implements Strategy.
func (s Broker) Finalizer(parentObject client.Object, finalizerName string) (string, error) {
	parentController := partitionletmeta.GetParentControllerOf(parentObject)
	if parentController == nil {
		if s.Fallback != nil {
			return s.Fallback.Finalizer(parentObject, finalizerName)
		}
		return "", fmt.Errorf("could not determine parent controller of %v", parentObject)
	}

	parts := strings.SplitN(finalizerName, "/", 2)
	if len(parts) == 2 {
		domain, name := parts[0], parts[1]
		return fmt.Sprintf("broker.%s/%s", domain, name), nil
	}
	return fmt.Sprintf("broker.partitionlet.onmetal.de/%s", parts[0]), nil
}

// SetParentControllerReference implements Strategy.
func (s Broker) SetParentControllerReference(parentObject, object client.Object, scheme *runtime.Scheme) error {
	parentController := partitionletmeta.GetParentControllerOf(parentObject)
	if parentController == nil {
		if s.Fallback != nil {
			return s.Fallback.SetParentControllerReference(parentObject, object, scheme)
		}
		return fmt.Errorf("could not determine parent controller of %v", parentObject)
	}

	if err := s.validateParentControllerRefersToObject(*parentController, object, scheme); err != nil {
		return err
	}
	return partitionletmeta.SetParentOwnerReference(parentObject, object, scheme)
}

func (s Broker) validateParentControllerRefersToObject(parentController partitionletmeta.ParentOwnerReference, object client.Object, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(object, scheme)
	if err != nil {
		return err
	}

	parentControllerGV, err := schema.ParseGroupVersion(parentController.APIVersion)
	if err != nil {
		return err
	}

	parentControllerGVK := parentControllerGV.WithKind(parentController.Kind)

	if gvk != parentControllerGVK {
		return fmt.Errorf("parent controller does not refer to object: kind mismatch: expected %s, actual %s", gvk, parentControllerGVK)
	}

	parentControllerKey := client.ObjectKey{Namespace: parentController.Namespace, Name: parentController.Name}
	if parentControllerKey != client.ObjectKeyFromObject(object) {
		return fmt.Errorf("parent controller does not refer to object: key mismatch: expected %s, actual %s", client.ObjectKeyFromObject(object), parentControllerKey)
	}

	return nil
}

// MustKey returns the key if err is nil. Panics otherwise.
func MustKey(key client.ObjectKey, err error) client.ObjectKey {
	utilruntime.Must(err)
	return key
}
