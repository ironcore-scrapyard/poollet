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

package helper

import networkingv1alpha1 "github.com/onmetal/onmetal-api/api/networking/v1alpha1"

func NetworkInterfaceReferencesNetworkName(nic *networkingv1alpha1.NetworkInterface, networkName string) bool {
	return nic.Spec.NetworkRef.Name == networkName
}

func NetworkInterfaceVirtualIPName(nic *networkingv1alpha1.NetworkInterface) string {
	virtualIP := nic.Spec.VirtualIP
	if virtualIP == nil {
		return ""
	}

	switch {
	case virtualIP.VirtualIPRef != nil:
		return virtualIP.VirtualIPRef.Name
	case virtualIP.Ephemeral != nil:
		return nic.Name
	default:
		return ""
	}
}

func AliasPrefixRoutingNetworkInterfaceNames(aliasPrefixRouting *networkingv1alpha1.AliasPrefixRouting) []string {
	res := make([]string, 0, len(aliasPrefixRouting.Destinations))
	for _, destination := range aliasPrefixRouting.Destinations {
		res = append(res, destination.Name)
	}
	return res
}
