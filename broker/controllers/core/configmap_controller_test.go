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

package core_test

import (
	. "github.com/onmetal/onmetal-api/testutils"
	mcmeta "github.com/onmetal/poollet/multicluster/meta"
	testdatav1 "github.com/onmetal/poollet/testdata/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("ConfigMapController", func() {
	ctx := SetupContext()
	ns, prov := SetupTest(ctx)

	It("should sync config map when referenced", func() {
		By("creating a config map")
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "configmap-",
			},
			Data: map[string]string{
				"foo": "bar",
			},
		}
		Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

		By("creating a foo referencing the config map")
		foo := &testdatav1.Foo{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "foo-",
			},
			Spec: testdatav1.FooSpec{
				Ref: &corev1.ObjectReference{
					APIVersion: corev1.SchemeGroupVersion.String(),
					Kind:       "ConfigMap",
					Name:       configMap.Name,
				},
			},
		}
		Expect(k8sClient.Create(ctx, foo)).To(Succeed())

		By("waiting for the original secret to report finalizers")
		Eventually(Object(configMap)).Should(
			HaveField("ObjectMeta.Finalizers", ConsistOf(domain.Subdomain(poolName).Slash("configmap"))),
		)

		By("waiting for the config map to be synced")
		configMapKey := client.ObjectKeyFromObject(configMap)
		targetConfigMap := &corev1.ConfigMap{}
		Eventually(func() error {
			return prov.Target(ctx, configMapKey, targetConfigMap)
		}).Should(Succeed())

		By("inspecting the synced config map")
		Expect(targetConfigMap.Namespace).NotTo(Equal(configMap.Namespace))
		Expect(mcmeta.IsControlledBy(
			clusterName,
			configMap,
			targetConfigMap,
		)).To(BeTrue(), "config map is not broker-controlled")
		Expect(targetConfigMap.Data).To(Equal(configMap.Data))

		By("updating the original config map")
		baseConfigMap := configMap.DeepCopy()
		configMap.Data = map[string]string{"foo": "baz"}
		Expect(k8sClient.Patch(ctx, configMap, client.MergeFrom(baseConfigMap))).To(Succeed())

		By("waiting for the synced config map to be updated")
		Eventually(Object(targetConfigMap)).Should(HaveField("Data", Equal(configMap.Data)))

		By("deleting the foo referencing the config map")
		Expect(k8sClient.Delete(ctx, foo)).To(Succeed())

		By("waiting for the synced config map to be gone")
		Eventually(Get(targetConfigMap)).Should(Satisfy(apierrors.IsNotFound))
	})
})
