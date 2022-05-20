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

package errors

import (
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type notSyncedError struct {
	groupResource schema.GroupResource
	sourceName    string
}

func (e *notSyncedError) Error() string {
	return fmt.Sprintf("resource %s/%s is not yet synced to target", e.groupResource, e.sourceName)
}

func NewNotSynced(gr schema.GroupResource, sourceName string) error {
	return &notSyncedError{groupResource: gr, sourceName: sourceName}
}

func IsNotSynced(err error) bool {
	return errors.As(err, new(*notSyncedError))
}

func IgnoreNotSynced(err error) error {
	if IsNotSynced(err) {
		return nil
	}
	return err
}

func IsNotSyncedOrNotFound(err error) bool {
	return IsNotSynced(err) || apierrors.IsNotFound(err)
}
