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

import "github.com/onmetal/poollet/broker/cluster"

type Target interface {
	InjectTarget(target cluster.Cluster) error
}

func TargetInto(target cluster.Cluster, value interface{}) (bool, error) {
	if injectable, wantsTarget := value.(Target); wantsTarget {
		return true, injectable.InjectTarget(target)
	}
	return false, nil
}

type Cluster interface {
	InjectCluster(cluster cluster.Cluster) error
}

func ClusterInto(cluster cluster.Cluster, value interface{}) (bool, error) {
	if injectable, wantsTarget := value.(Cluster); wantsTarget {
		return true, injectable.InjectCluster(cluster)
	}
	return false, nil
}
