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

package predicate

import (
	"fmt"
	"strings"

	"github.com/onmetal/onmetal-api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("VolatileConditionFieldsPredicate")

var DefaultVolatileConditionFields = []string{
	"lastTransitionTime",
	"lastUpdateTime",
	"lastScaleTime",
	"lastProbeTime",
	"lastHeartbeatTime",
}

// VolatileConditionFieldsPredicate removes volatile fields from any condition
// in an object and then does an equals-based comparison.
type VolatileConditionFieldsPredicate struct {
	// Fields specifies the volatile fields to erase.
	// If nil, this is set to DefaultVolatileConditionFields.
	Fields []string
}

func jsonPath(fields []string) string {
	return "." + strings.Join(fields, ".")
}

func nestedFieldSlice(obj map[string]interface{}, fields ...string) ([]map[string]interface{}, bool, error) {
	slice, ok, err := unstructured.NestedSlice(obj, fields...)
	if err != nil || !ok {
		return nil, ok, err
	}

	res := make([]map[string]interface{}, 0, len(slice))
	for _, v := range slice {
		if field, ok := v.(map[string]interface{}); ok {
			res = append(res, field)
			continue
		}

		return nil, false, fmt.Errorf("%v accessor error: contains non-field key in the slice: %v is of type %T, expected map[string]interface{}", jsonPath(fields), v, v)
	}

	return res, true, nil
}

func setNestedFieldSlice(obj map[string]interface{}, value []map[string]interface{}, fields ...string) error {
	m := make([]interface{}, 0, len(value))
	for _, v := range value {
		m = append(m, v)
	}
	return unstructured.SetNestedSlice(obj, m, fields...)
}

func (s *VolatileConditionFieldsPredicate) pruneVolatileConditionFields(obj client.Object) (client.Object, error) {
	// Create a copy of the original object as well as converting that copy to
	// unstructured data.
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj.DeepCopyObject())
	if err != nil {
		return nil, err
	}

	conditions, ok, err := nestedFieldSlice(unstructuredObj, "status", "conditions")
	if err != nil {
		return nil, err
	}
	// No .status.conditions means nothing to prune, return the original.
	if !ok {
		return obj, nil
	}

	timeFields := s.Fields
	if timeFields == nil {
		timeFields = DefaultVolatileConditionFields
	}
	newConditions := make([]map[string]interface{}, 0, len(conditions))
	for _, conditionValue := range conditions {
		for _, timeField := range timeFields {
			delete(conditionValue, timeField)
		}
	}

	if err := setNestedFieldSlice(unstructuredObj, newConditions, "status", "conditions"); err != nil {
		return nil, err
	}

	withoutTimeFields := obj.DeepCopyObject()
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj, withoutTimeFields); err != nil {
		return nil, err
	}
	return withoutTimeFields.(client.Object), nil
}

func (s *VolatileConditionFieldsPredicate) Create(event event.CreateEvent) bool {
	return true
}

func (s *VolatileConditionFieldsPredicate) Delete(event event.DeleteEvent) bool {
	return true
}

func (s *VolatileConditionFieldsPredicate) Update(event event.UpdateEvent) bool {
	oldObj, err := s.pruneVolatileConditionFields(event.ObjectOld)
	if err != nil {
		log.Error(err, "Error pruning volatile fields, cannot filter event", "OldObject", oldObj)
		return true
	}

	newObj, err := s.pruneVolatileConditionFields(event.ObjectNew)
	if err != nil {
		log.Error(err, "Error pruning volatile fields, cannot filter event", "NewObject", newObj)
		return true
	}

	oldObj.SetResourceVersion("")
	newObj.SetResourceVersion("")

	equal := equality.Semantic.DeepEqual(oldObj, newObj)
	log.V(5).Info("Equal objects", "Equal", equal)
	return !equal
}

func (s *VolatileConditionFieldsPredicate) Generic(event event.GenericEvent) bool {
	return true
}
