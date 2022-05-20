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

package hash

import (
	"fmt"
	"hash/fnv"
)

// FNV32A calculates the fnv 32 bit checksum of the given strings in hexadecimal form.
func FNV32A(s ...string) string {
	hasher := fnv.New32a()
	for _, s := range s {
		_, _ = hasher.Write([]byte(s))
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}
