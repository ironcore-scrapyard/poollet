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

package client

import (
	"context"

	"github.com/onmetal/poollet/multicluster/controllerutil"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var log = ctrl.Log.WithName("multicluster").WithName("client")

// IndexClusterTypeOwnerReferencesField creates an indexed field that contains the key strings
// (client.ObjectKey.String()) of the owner references obtained via controllerutil.GetClusterTypeOwnerReferences.
func IndexClusterTypeOwnerReferencesField(
	ctx context.Context,
	indexer client.FieldIndexer,
	clusterName string,
	ownerType client.Object,
	obj client.Object,
	field string,
	scheme *runtime.Scheme,
	controller bool,
) error {
	return indexer.IndexField(ctx, obj, field, func(object client.Object) []string {
		refs, err := controllerutil.GetClusterTypeOwnerReferences(clusterName, ownerType, object, scheme, controller)
		if err != nil {
			log.Error(err, "Error getting cluster type owner references")
			return nil
		}

		res := make([]string, len(refs))
		for i, ref := range refs {
			key := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
			res[i] = key.String()
		}
		return res
	})
}
