// Copyright 2021 OnMetal authors
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

package v1alpha1

import (
	"fmt"
)

const (
	ConsoleParentNamespaceAnnotation = "partitionlet.onmetal.de/machine-parent-namespace"
	ConsoleParentNameAnnotation      = "partitionlet.onmetal.de/machine-parent-name"
)

func ConsoleName(parentNamespace, parentName string) string {
	return fmt.Sprintf("%s--%s", parentNamespace, parentName)
}

func ConsoleSecretName(parentConsoleNamespace, parentConsoleName string) string {
	return fmt.Sprintf("console-%s--%s", parentConsoleNamespace, parentConsoleName)
}

func ParentConsoleSecretName(parentConsoleName string) string {
	return fmt.Sprintf("console-%s", parentConsoleName)
}
