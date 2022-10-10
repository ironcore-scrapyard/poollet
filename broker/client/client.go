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
	"fmt"

	poolletclient "github.com/onmetal/poollet/client"
	mccontrolerutil "github.com/onmetal/poollet/multicluster/controllerutil"
	mcmeta "github.com/onmetal/poollet/multicluster/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func BrokerControlledListSingle(
	ctx context.Context,
	r client.Reader,
	scheme *runtime.Scheme,
	clusterName string,
	brokerOwner, obj client.Object,
	opts ...client.ListOption,
) error {
	if err := poolletclient.ListSingle(ctx, r, scheme, obj, opts...); err != nil {
		return err
	}
	if !mcmeta.IsControlledBy(clusterName, brokerOwner, obj) {
		return fmt.Errorf("object is not broker-controlled by broker owner")
	}
	return nil
}

func BrokerControlledListSingleAndDelete(
	ctx context.Context,
	r client.Reader,
	c client.Client,
	clusterName string,
	brokerOwner, obj client.Object,
	opts ...client.DeleteAllOfOption,
) error {
	deleteAllOfOptions := &client.DeleteAllOfOptions{}
	deleteAllOfOptions.ApplyOptions(opts)

	if err := BrokerControlledListSingle(
		ctx,
		r,
		c.Scheme(),
		clusterName,
		brokerOwner,
		obj,
		&deleteAllOfOptions.ListOptions,
	); err != nil {
		return err
	}

	return c.Delete(ctx, obj, &deleteAllOfOptions.DeleteOptions)
}

func brokerControlledMutate(clusterName string, brokerOwner, obj client.Object, f controllerutil.MutateFn, scheme *runtime.Scheme) error {
	if obj.GetResourceVersion() != "" {
		if !mcmeta.IsControlledBy(clusterName, brokerOwner, obj) {
			return fmt.Errorf("object is not broker-controlled by broker owner")
		}
		return f()
	}
	if err := f(); err != nil {
		return err
	}
	if err := mccontrolerutil.SetAncestry(clusterName, brokerOwner, obj); err != nil {
		return err
	}
	return mccontrolerutil.SetControllerReference(clusterName, brokerOwner, obj, scheme)
}

func BrokerControlledCreateOrPatch(
	ctx context.Context,
	c client.Client,
	clusterName string,
	parentOwner, obj client.Object,
	f controllerutil.MutateFn,
) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrPatch(ctx, c, obj, func() error {
		return brokerControlledMutate(clusterName, parentOwner, obj, f, c.Scheme())
	})
}

func BrokerControlledListSingleGenerateOrPatch(
	ctx context.Context,
	r client.Reader,
	c client.Client,
	clusterName string,
	parentOwner, obj client.Object,
	f controllerutil.MutateFn,
	opts ...client.ListOption,
) (controllerutil.OperationResult, error) {
	return poolletclient.ListSingleGenerateOrPatch(ctx, r, c, obj, func() error {
		return brokerControlledMutate(clusterName, parentOwner, obj, f, c.Scheme())
	}, opts...)
}
