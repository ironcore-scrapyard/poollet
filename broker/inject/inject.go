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

package inject

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type stringer struct {
	v interface{}
}

func (s stringer) String() string {
	if stringer, ok := s.v.(fmt.Stringer); ok {
		return stringer.String()
	}
	return fmt.Sprintf("%v", s.v)
}

func ShieldSource(src source.Source) source.Source {
	if syncing, ok := src.(source.SyncingSource); ok {
		return struct {
			source.SyncingSource
			fmt.Stringer
		}{syncing, stringer{src}}
	}
	return struct {
		source.Source
		fmt.Stringer
	}{src, stringer{src}}
}

func ShieldEventHandler(hdl handler.EventHandler) handler.EventHandler {
	return struct {
		handler.EventHandler
		fmt.Stringer
	}{hdl, stringer{hdl}}
}

func ShieldPredicate(prct predicate.Predicate) predicate.Predicate {
	return struct {
		predicate.Predicate
		fmt.Stringer
	}{prct, stringer{prct}}
}

func ShieldPredicates(prct []predicate.Predicate) []predicate.Predicate {
	res := make([]predicate.Predicate, 0, len(prct))
	for _, prct := range prct {
		res = append(res, ShieldPredicate(prct))
	}
	return res
}
