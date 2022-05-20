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

package sync

import (
	"errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CompositeMutationBuilder struct {
	Mutations   []func()
	PartialSync bool
}

func (b *CompositeMutationBuilder) Add(f func()) *CompositeMutationBuilder {
	b.Mutations = append(b.Mutations, f)
	return b
}

var errNoPartialCreate = errors.New("no partial create")

func (b *CompositeMutationBuilder) Mutate(obj client.Object) func() error {
	return func() error {
		if obj.GetResourceVersion() == "" && b.PartialSync {
			return errNoPartialCreate
		}
		for _, mutate := range b.Mutations {
			mutate()
		}
		return nil
	}
}

func IsPartialCreate(err error) bool {
	return errors.Is(err, errNoPartialCreate)
}

func IgnorePartialCreate(err error) error {
	if IsPartialCreate(err) {
		return nil
	}
	return err
}
