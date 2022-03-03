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

package names

import (
	"fmt"

	partitionletmeta "github.com/onmetal/partitionlet/meta"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Strategy is a strategy to obtain keys for syncing parent objects to the cluster.
type Strategy interface {
	// Key returns the key for the synced object by giving the parent key and type.
	Key(parentObject client.Object) (client.ObjectKey, error)
}

// FixedNamespaceNamespacedNameStrategy is a strategy to obtain the key by setting the name of the
// synced object to <namespace>/<parent-namespace>--<parent-name>.
type FixedNamespaceNamespacedNameStrategy struct {
	Namespace string
}

// Key implements Strategy.
func (n FixedNamespaceNamespacedNameStrategy) Key(parentObject client.Object) (client.ObjectKey, error) {
	return client.ObjectKey{
		Namespace: n.Namespace,
		Name:      fmt.Sprintf("%s--%s", parentObject.GetNamespace(), parentObject.GetName()),
	}, nil
}

// GrandparentControllerStrategy is a strategy that determines the target key by using the parent controller
// of the parent object, thus the 'grandparent' object's key will be used.
// If the object does not have a controller, the Fallback is used, if any is supplied. Otherwise, an error
// is thrown.
type GrandparentControllerStrategy struct {
	Fallback Strategy
}

// Key implements Strategy.
func (s GrandparentControllerStrategy) Key(parentObject client.Object) (client.ObjectKey, error) {
	controller := partitionletmeta.GetParentControllerOf(parentObject)
	if controller == nil {
		if s.Fallback != nil {
			return s.Fallback.Key(parentObject)
		}
		return client.ObjectKey{}, fmt.Errorf("could not determine source parent controller of %v", parentObject)
	}
	return client.ObjectKey{Namespace: controller.Namespace, Name: controller.Name}, nil
}

func Must(key client.ObjectKey, err error) client.ObjectKey {
	utilruntime.Must(err)
	return key
}
