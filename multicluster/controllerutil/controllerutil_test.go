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

package controllerutil_test

import (
	. "github.com/onmetal/poollet/multicluster/controllerutil"
	. "github.com/onmetal/poollet/multicluster/meta"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Meta", func() {
	const (
		sourceCluster = "source"
		childCluster  = "child"
	)

	var (
		sourceObj, childObj, grandchildObj *corev1.Secret
	)
	BeforeEach(func() {
		sourceObj = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				UID:       "source",
				Namespace: "source-ns",
				Name:      "source",
			},
		}
		childObj = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				UID:       "child",
				Namespace: "child-ns",
				Name:      "child",
			},
		}
		grandchildObj = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				UID:       "grandchild",
				Namespace: "grandchild-ns",
				Name:      "grandchild",
			},
		}
	})

	Describe("SetAncestry", func() {
		It("set the lineage on the child object to a single reference to the parent", func() {
			Expect(SetAncestry(sourceCluster, sourceObj, childObj)).To(Succeed())
			Expect(GetAncestors(childObj)).To(Equal([]Ancestor{
				{
					ClusterName: sourceCluster,
					Namespace:   sourceObj.GetNamespace(),
					Name:        sourceObj.GetName(),
					UID:         sourceObj.GetUID(),
				},
			}))
		})

		It("should set the lineage on the grandchild object to ordered references to the parents", func() {
			Expect(SetAncestry(sourceCluster, sourceObj, childObj)).To(Succeed())
			Expect(SetAncestry(childCluster, childObj, grandchildObj)).To(Succeed())
			Expect(GetAncestors(grandchildObj)).To(Equal([]Ancestor{
				{
					ClusterName: sourceCluster,
					Namespace:   sourceObj.GetNamespace(),
					Name:        sourceObj.GetName(),
					UID:         sourceObj.GetUID(),
				},
				{
					ClusterName: childCluster,
					Namespace:   childObj.GetNamespace(),
					Name:        childObj.GetName(),
					UID:         childObj.GetUID(),
				},
			}))
		})

		It("should error if the ancestors mismatch", func() {
			Expect(SetAncestry(sourceCluster, sourceObj, childObj)).To(Succeed())
			Expect(SetAncestry(childCluster, childObj, grandchildObj)).To(Succeed())
			Expect(SetAncestry(sourceCluster, sourceObj, grandchildObj)).To(HaveOccurred())
		})
	})

	Describe("GetRootUID", func() {
		It("should return the own uid", func() {
			Expect(GetRootUID(sourceObj)).To(Equal(sourceObj.GetUID()))
		})

		It("should return the first ancestor's uid", func() {
			Expect(SetAncestry(sourceCluster, sourceObj, childObj)).To(Succeed())
			Expect(SetAncestry(childCluster, childObj, grandchildObj)).To(Succeed())

			Expect(GetRootUID(childObj)).To(Equal(sourceObj.GetUID()))
			Expect(GetRootUID(grandchildObj)).To(Equal(sourceObj.GetUID()))
		})
	})
})
