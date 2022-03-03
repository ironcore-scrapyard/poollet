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

package names_test

import (
	"fmt"

	partitionletmeta "github.com/onmetal/partitionlet/meta"
	. "github.com/onmetal/partitionlet/names"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Names", func() {
	Describe("Must", func() {
		It("should return the key if it computes without error", func() {
			Expect(Must(client.ObjectKey{}, nil)).To(Equal(client.ObjectKey{}))
		})

		It("should panic with the error if there is any", func() {
			Expect(func() { Must(client.ObjectKey{}, fmt.Errorf("some error")) }).To(Panic())
		})
	})

	Context("FixedNamespaceNamespacedNameStrategy", func() {
		It("should construct the key using a fixed namespace and a combination of namespace and name", func() {
			strategy := FixedNamespaceNamespacedNameStrategy{
				Namespace: "default",
			}
			Expect(strategy.Key(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
			})).To(Equal(client.ObjectKey{Namespace: "default", Name: "foo--bar"}))
		})
	})

	Context("GrandparentControllerStrategy", func() {
		It("should compute the key from the parent object's parent controller", func() {
			strategy := &GrandparentControllerStrategy{}
			grandparentCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
			}
			parentCM := &corev1.ConfigMap{}
			Expect(partitionletmeta.SetParentControllerReference(grandparentCM, parentCM, scheme.Scheme)).To(Succeed())

			Expect(strategy.Key(parentCM)).To(Equal(client.ObjectKey{Namespace: "foo", Name: "bar"}))
		})

		It("should error if not annotation is specified and no fallback is present", func() {
			strategy := &GrandparentControllerStrategy{}
			_, err := strategy.Key(&corev1.ConfigMap{})
			Expect(err).To(HaveOccurred())
		})

		It("should call fallback if no parent annotation is specified", func() {
			strategy := &GrandparentControllerStrategy{Fallback: FixedNamespaceNamespacedNameStrategy{Namespace: "foo"}}
			Expect(strategy.Key(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
			})).To(Equal(client.ObjectKey{Namespace: "foo", Name: "foo--bar"}))
		})
	})
})
