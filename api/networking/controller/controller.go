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

package controller

import (
	"context"
	"fmt"

	computev1alpha1 "github.com/onmetal/onmetal-api/api/compute/v1alpha1"
	networkingv1alpha1 "github.com/onmetal/onmetal-api/api/networking/v1alpha1"
	computehelper "github.com/onmetal/poollet/api/compute/helper"
	networkingindexclient "github.com/onmetal/poollet/api/networking/client/index"
	networkinghelper "github.com/onmetal/poollet/api/networking/helper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func IsNetworkInterfaceUsedCached(ctx context.Context, c client.Client, nic *networkingv1alpha1.NetworkInterface, machinePoolName string) (bool, error) {
	machineRef := nic.Spec.MachineRef
	if machineRef == nil {
		return false, nil
	}

	machine := &computev1alpha1.Machine{}
	machineKey := client.ObjectKey{Namespace: nic.Namespace, Name: machineRef.Name}
	if err := c.Get(ctx, machineKey, machine); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	return computehelper.MachineRunsInMachinePool(machine, machinePoolName) &&
			machineRef.UID == machine.UID &&
			computehelper.MachineSpecNetworkInterfaceNames(machine).Has(nic.Name),
		nil
}

func IsNetworkInterfaceUsedLive(ctx context.Context, r client.Reader, nic *networkingv1alpha1.NetworkInterface, machinePoolName string) (bool, error) {
	machineRef := nic.Spec.MachineRef
	if machineRef == nil {
		return false, nil
	}

	machine := &computev1alpha1.Machine{}
	machineKey := client.ObjectKey{Namespace: nic.Namespace, Name: machineRef.Name}
	if err := r.Get(ctx, machineKey, machine); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	return computehelper.MachineRunsInMachinePool(machine, machinePoolName) &&
			machineRef.UID == machine.UID &&
			computehelper.MachineSpecNetworkInterfaceNames(machine).Has(nic.Name),
		nil
}

func IsNetworkInterfaceUsedCachedOrLive(ctx context.Context, r client.Reader, c client.Client, nic *networkingv1alpha1.NetworkInterface, machinePoolName string) (bool, error) {
	if ok, err := IsNetworkInterfaceUsedCached(ctx, c, nic, machinePoolName); err != nil || ok {
		return ok, err
	}
	if ok, err := IsNetworkInterfaceUsedLive(ctx, r, nic, machinePoolName); err != nil || ok {
		return ok, err
	}
	return false, nil
}

func IsAliasPrefixUsedCached(ctx context.Context, c client.Client, aliasPrefix *networkingv1alpha1.AliasPrefix, machinePoolName string) (bool, error) {
	aliasPrefixRouting := &networkingv1alpha1.AliasPrefixRouting{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(aliasPrefix), aliasPrefixRouting); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	for _, destination := range aliasPrefixRouting.Destinations {
		nic := &networkingv1alpha1.NetworkInterface{}
		nicKey := client.ObjectKey{Namespace: aliasPrefix.Namespace, Name: destination.Name}
		if err := c.Get(ctx, nicKey, nic); err != nil {
			if !apierrors.IsNotFound(err) {
				return false, fmt.Errorf("error getting network interface %s: %w", nicKey, err)
			}
			continue
		}

		if ok, err := IsNetworkInterfaceUsedCached(ctx, c, nic, machinePoolName); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func IsAliasPrefixUsedLive(ctx context.Context, r client.Reader, aliasPrefix *networkingv1alpha1.AliasPrefix, machinePoolName string) (bool, error) {
	aliasPrefixRouting := &networkingv1alpha1.AliasPrefixRouting{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(aliasPrefix), aliasPrefixRouting); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	for _, destination := range aliasPrefixRouting.Destinations {
		nic := &networkingv1alpha1.NetworkInterface{}
		nicKey := client.ObjectKey{Namespace: aliasPrefix.Namespace, Name: destination.Name}
		if err := r.Get(ctx, nicKey, nic); err != nil {
			if !apierrors.IsNotFound(err) {
				return false, fmt.Errorf("error getting network interface %s: %w", nicKey, err)
			}
			continue
		}

		if ok, err := IsNetworkInterfaceUsedLive(ctx, r, nic, machinePoolName); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func IsAliasPrefixUsedCachedOrLive(ctx context.Context, r client.Reader, c client.Client, aliasPrefix *networkingv1alpha1.AliasPrefix, machinePoolName string) (bool, error) {
	if ok, err := IsAliasPrefixUsedCached(ctx, c, aliasPrefix, machinePoolName); err != nil || ok {
		return ok, err
	}
	if ok, err := IsAliasPrefixUsedLive(ctx, r, aliasPrefix, machinePoolName); err != nil || ok {
		return ok, err
	}
	return false, nil
}

func IsNetworkUsedCached(ctx context.Context, c client.Client, network *networkingv1alpha1.Network, machinePoolName string) (bool, error) {
	nics, err := networkingindexclient.ListNetworkInterfacesReferencingNetworkKey(ctx, c, client.ObjectKeyFromObject(network))
	if err != nil {
		return false, err
	}

	for _, nic := range nics {
		if ok, err := IsNetworkInterfaceUsedCached(ctx, c, &nic, machinePoolName); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func IsNetworkUsedLive(ctx context.Context, r client.Reader, network *networkingv1alpha1.Network, machinePoolName string) (bool, error) {
	nicList := &networkingv1alpha1.NetworkInterfaceList{}
	if err := r.List(ctx, nicList,
		client.InNamespace(network.Namespace),
	); err != nil {
		return false, err
	}

	for _, nic := range nicList.Items {
		if !networkinghelper.NetworkInterfaceReferencesNetworkName(&nic, network.Name) {
			continue
		}

		if ok, err := IsNetworkInterfaceUsedLive(ctx, r, &nic, machinePoolName); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func IsNetworkUsedCachedOrLive(ctx context.Context, r client.Reader, c client.Client, network *networkingv1alpha1.Network, machinePoolName string) (bool, error) {
	if ok, err := IsNetworkUsedCached(ctx, c, network, machinePoolName); err != nil || ok {
		return ok, err
	}
	if ok, err := IsNetworkUsedLive(ctx, r, network, machinePoolName); err != nil || ok {
		return ok, err
	}
	return false, nil
}

func IsVirtualIPUsedCached(ctx context.Context, c client.Client, virtualIP *networkingv1alpha1.VirtualIP, machinePoolName string) (bool, error) {
	targetRef := virtualIP.Spec.TargetRef
	if targetRef == nil {
		return false, nil
	}

	nic := &networkingv1alpha1.NetworkInterface{}
	nicKey := client.ObjectKey{Namespace: virtualIP.Namespace, Name: targetRef.Name}
	if err := c.Get(ctx, nicKey, nic); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	return IsNetworkInterfaceUsedCached(ctx, c, nic, machinePoolName)
}

func IsVirtualIPUsedLive(ctx context.Context, r client.Reader, virtualIP *networkingv1alpha1.VirtualIP, machinePoolName string) (bool, error) {
	targetRef := virtualIP.Spec.TargetRef
	if targetRef == nil {
		return false, nil
	}

	nic := &networkingv1alpha1.NetworkInterface{}
	nicKey := client.ObjectKey{Namespace: virtualIP.Namespace, Name: targetRef.Name}
	if err := r.Get(ctx, nicKey, nic); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	return IsNetworkInterfaceUsedLive(ctx, r, nic, machinePoolName)
}

func IsVirtualIPUsedCachedOrLive(ctx context.Context, r client.Reader, c client.Client, virtualIP *networkingv1alpha1.VirtualIP, machinePoolName string) (bool, error) {
	if ok, err := IsVirtualIPUsedCached(ctx, c, virtualIP, machinePoolName); err != nil || ok {
		return ok, err
	}
	if ok, err := IsVirtualIPUsedLive(ctx, r, virtualIP, machinePoolName); err != nil || ok {
		return ok, err
	}
	return false, nil
}
