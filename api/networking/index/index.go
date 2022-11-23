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

	networkingv1alpha1 "github.com/onmetal/onmetal-api/api/networking/v1alpha1"
	"github.com/onmetal/poollet/api/networking/helper"
	"github.com/onmetal/poollet/api/networking/index/fields"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NetworkInterfaceNetworkNameField(ctx context.Context, c client.FieldIndexer) error {
	return c.IndexField(ctx, &networkingv1alpha1.NetworkInterface{}, fields.NetworkInterfaceSpecNetworkRefName, func(obj client.Object) []string {
		nic := obj.(*networkingv1alpha1.NetworkInterface)
		return []string{nic.Spec.NetworkRef.Name}
	})
}

func NetworkInterfaceVirtualIPNameField(ctx context.Context, c client.FieldIndexer) error {
	return c.IndexField(ctx, &networkingv1alpha1.NetworkInterface{}, fields.NetworkInterfaceVirtualIPName, func(obj client.Object) []string {
		nic := obj.(*networkingv1alpha1.NetworkInterface)
		return []string{helper.NetworkInterfaceVirtualIPName(nic)}
	})
}

func AliasPrefixRoutingNetworkInterfaceNamesField(ctx context.Context, c client.FieldIndexer) error {
	return c.IndexField(ctx, &networkingv1alpha1.AliasPrefixRouting{}, fields.AliasPrefixRoutingNetworkInterfaceNames, func(obj client.Object) []string {
		aliasPrefixRouting := obj.(*networkingv1alpha1.AliasPrefixRouting)
		if names := helper.AliasPrefixRoutingNetworkInterfaceNames(aliasPrefixRouting); len(names) > 0 {
			return names
		}
		return []string{""}
	})
}

func VirtualIPSpecTargetRefNameField(ctx context.Context, c client.FieldIndexer) error {
	return c.IndexField(ctx, &networkingv1alpha1.VirtualIP{}, fields.VirtualIPSpecTargetRefName, func(obj client.Object) []string {
		virtualIP := obj.(*networkingv1alpha1.VirtualIP)
		targetRef := virtualIP.Spec.TargetRef
		if targetRef == nil {
			return []string{""}
		}
		return []string{targetRef.Name}
	})
}
