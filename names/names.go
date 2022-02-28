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
	"context"
	"fmt"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Strategy is a strategy to obtain keys for syncing parent objects to the cluster.
type Strategy interface {
	// Key returns the key for the synced object by giving the parent key and type.
	Key(ctx context.Context, parentKey client.ObjectKey, parentType client.Object) (client.ObjectKey, error)
}

// FixedNamespaceNamespacedNameStrategy is a strategy to obtain the key by setting the name of the
// synced object to <namespace>/<parent-namespace>--<parent-name>.
type FixedNamespaceNamespacedNameStrategy struct {
	Namespace string
}

// Key implements Strategy.
func (n FixedNamespaceNamespacedNameStrategy) Key(ctx context.Context, parentKey client.ObjectKey, parentObj client.Object) (client.ObjectKey, error) {
	return client.ObjectKey{
		Namespace: n.Namespace,
		Name:      fmt.Sprintf("%s--%s", parentKey.Namespace, parentKey.Name),
	}, nil
}

func Must(key client.ObjectKey, err error) client.ObjectKey {
	utilruntime.Must(err)
	return key
}
