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

	"github.com/onmetal/controller-utils/metautils"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func checkCanGenerate(obj client.Object) error {
	if obj.GetGenerateName() == "" {
		return fmt.Errorf("must specify metadata.generateName")
	}
	if obj.GetName() != "" {
		return fmt.Errorf("must not specify metadata.name")
	}
	return nil
}

func ListSingle(ctx context.Context, r client.Reader, scheme *runtime.Scheme, obj client.Object, opts ...client.ListOption) error {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return err
	}

	list, err := metautils.NewListForObject(scheme, obj)
	if err != nil {
		return err
	}

	listOpts := &client.ListOptions{}
	listOpts.ApplyOptions(opts)
	if namespace := obj.GetNamespace(); namespace != "" {
		listOpts.Namespace = namespace
	}

	if err := r.List(ctx, list, listOpts); err != nil {
		return err
	}

	switch n := meta.LenList(list); n {
	case 0:
		// Yes, we're using Kind as Resource here (see controller-runtime).
		// Name is not a real name but good enough.
		return apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, "<matching options>")
	case 1:
		objs, err := metautils.ExtractList(list)
		if err != nil {
			return err
		}
		return scheme.Convert(objs[0], obj, nil)
	default:
		return apierrors.NewInternalError(fmt.Errorf("list single %s returned more than 1 (%d) objects", gvk, n))
	}
}

func ListSingleAndDelete(ctx context.Context, r client.Reader, c client.Client, obj client.Object, opts ...client.DeleteAllOfOption) error {
	deleteAllOfOptions := &client.DeleteAllOfOptions{}
	deleteAllOfOptions.ApplyOptions(opts)

	if err := ListSingle(ctx, r, c.Scheme(), obj, &deleteAllOfOptions.ListOptions); err != nil {
		return err
	}

	return c.Delete(ctx, obj, &deleteAllOfOptions.DeleteOptions)
}

func ControlledListSingle(ctx context.Context, r client.Reader, scheme *runtime.Scheme, owner, obj client.Object, opts ...client.ListOption) error {
	if err := ListSingle(ctx, r, scheme, obj, opts...); err != nil {
		return err
	}

	if !metav1.IsControlledBy(obj, owner) {
		return fmt.Errorf("object is not controlled by owner")
	}

	return nil
}

func ControlledListSingleAndDelete(ctx context.Context, r client.Reader, c client.Client, owner, obj client.Object, opts ...client.DeleteAllOfOption) error {
	deleteAllOfOptions := &client.DeleteAllOfOptions{}
	deleteAllOfOptions.ApplyOptions(opts)

	if err := ControlledListSingle(ctx, r, c.Scheme(), owner, obj, &deleteAllOfOptions.ListOptions); err != nil {
		return err
	}

	return c.Delete(ctx, obj, &deleteAllOfOptions.DeleteOptions)
}

// ListSingleGenerateOrPatch lists a single object and, if it does not exist, generates (i.e. create an object with
// GenerateName set) an object or updates the single object.
//
// The caller of ListSingleGenerateOrPatch *has* to make sure that the list call issued using client.Reader returns at most
// 1 object. client.Reader is used over client.Client since cached List calls may not yield objects created in
// previous invocations of ListSingleGenerateOrPatch, leading to inconsistent behavior.
func ListSingleGenerateOrPatch(ctx context.Context, r client.Reader, c client.Client, obj client.Object, f controllerutil.MutateFn, opts ...client.ListOption) (controllerutil.OperationResult, error) {
	if err := checkCanGenerate(obj); err != nil {
		return controllerutil.OperationResultNone, err
	}

	existing := obj.DeepCopyObject().(client.Object)
	if err := ListSingle(ctx, r, c.Scheme(), existing, opts...); err != nil {
		if !apierrors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}

		namespace := obj.GetNamespace()
		if err := f(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if obj.GetNamespace() != namespace {
			return controllerutil.OperationResultNone, fmt.Errorf("may not mutate namespace")
		}
		if err := checkCanGenerate(obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if err := c.Create(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, nil
	}

	if err := c.Scheme().Convert(existing, obj, nil); err != nil {
		return controllerutil.OperationResultNone, err
	}
	key := client.ObjectKeyFromObject(obj)
	base := obj.DeepCopyObject().(client.Object)
	if err := f(); err != nil {
		return controllerutil.OperationResultNone, err
	}
	if key != client.ObjectKeyFromObject(obj) {
		return controllerutil.OperationResultNone, fmt.Errorf("may not mutate namespace/name")
	}
	if err := c.Patch(ctx, obj, client.MergeFrom(base)); err != nil {
		return controllerutil.OperationResultNone, err
	}
	return controllerutil.OperationResultUpdated, nil
}

func ControlledListSingleGenerateOrPatch(ctx context.Context, r client.Reader, c client.Client, owner, obj client.Object, f controllerutil.MutateFn, opts ...client.ListOption) (controllerutil.OperationResult, error) {
	return ListSingleGenerateOrPatch(ctx, r, c, obj, func() error {
		if obj.GetResourceVersion() != "" {
			if !metav1.IsControlledBy(obj, owner) {
				return fmt.Errorf("object is not controlled by owner")
			}
			return f()
		}
		if err := f(); err != nil {
			return err
		}
		return ctrl.SetControllerReference(owner, obj, c.Scheme())
	}, opts...)
}
