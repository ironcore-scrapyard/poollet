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

package strategy_test

import (
	"fmt"

	partitionletmeta "github.com/onmetal/partitionlet/meta"
	. "github.com/onmetal/partitionlet/strategy"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Strategy", func() {
	Describe("MustKey", func() {
		It("should return the key if it computes without error", func() {
			Expect(MustKey(client.ObjectKey{}, nil)).To(Equal(client.ObjectKey{}))
		})

		It("should panic with the error if there is any", func() {
			Expect(func() { MustKey(client.ObjectKey{}, fmt.Errorf("some error")) }).To(Panic())
		})
	})

	Context("Simple", func() {
		It("should construct the key using a fixed namespace and a combination of namespace and name", func() {
			strategy := Simple{
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

	Context("Broker", func() {
		var (
			strat                    Broker
			stratWithFallback        Broker
			obj, ownedObj, parentObj client.Object
			key, parentKey           client.ObjectKey
		)
		BeforeEach(func() {
			strat = Broker{}
			stratWithFallback = Broker{
				Fallback: Simple{Namespace: "foo"},
			}
			obj = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
			}
			parentObj = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "baz",
					Name:      "bang",
				},
			}
			ownedObj = obj.DeepCopyObject().(client.Object)
			Expect(partitionletmeta.SetParentControllerReference(parentObj, ownedObj, scheme.Scheme)).To(Succeed())
			key = client.ObjectKeyFromObject(obj)
			parentKey = client.ObjectKeyFromObject(parentObj)
			_ = key
		})

		Describe("Key", func() {
			It("should compute the key from the parent object's parent controller", func() {
				Expect(strat.Key(ownedObj)).To(Equal(parentKey))
			})

			It("should error if not annotation is specified and no fallback is present", func() {
				_, err := strat.Key(obj)
				Expect(err).To(HaveOccurred())
			})

			It("should call fallback if no parent annotation is specified", func() {
				Expect(stratWithFallback.Key(obj)).To(Equal(client.ObjectKey{Namespace: "foo", Name: "foo--bar"}))
			})
		})

		Describe("Finalizer", func() {
			It("should use the fallback finalizer if the object is not parent-controlled", func() {
				Expect(stratWithFallback.Finalizer(obj, "foo")).To(Equal("foo"))
			})

			It("should error if no fallback is set and the object is not parent-controlled", func() {
				_, err := strat.Finalizer(obj, "foo")
				Expect(err).To(HaveOccurred())
			})

			It("should prefix the finalizer domain if the object is parent-controlled", func() {
				Expect(strat.Finalizer(ownedObj, "domain/foo")).To(Equal("broker.domain/foo"))
			})

			It("should add the partitionlet broker domain if the object is parent-controlled and the finalizer does not have a domain", func() {
				Expect(strat.Finalizer(ownedObj, "foo")).To(Equal("broker.partitionlet.onmetal.de/foo"))
			})
		})
	})
})
