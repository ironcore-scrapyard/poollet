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

package index

import (
	"context"
	"fmt"

	networkingv1alpha1 "github.com/onmetal/onmetal-api/apis/networking/v1alpha1"
	"github.com/onmetal/poollet/api/networking/index/fields"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ListNetworkInterfacesReferencingNetworkKey(ctx context.Context, c client.Client, networkKey client.ObjectKey) ([]networkingv1alpha1.NetworkInterface, error) {
	nicList := &networkingv1alpha1.NetworkInterfaceList{}
	if err := c.List(ctx, nicList,
		client.InNamespace(networkKey.Namespace),
		client.MatchingFields{fields.NetworkInterfaceSpecNetworkRefName: networkKey.Name},
	); err != nil {
		return nil, fmt.Errorf("error listing network interfaces referencing network key %s: %w", networkKey, err)
	}
	return nicList.Items, nil
}
