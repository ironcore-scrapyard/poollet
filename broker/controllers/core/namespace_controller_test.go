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
	"time"

	. "github.com/onmetal/onmetal-api/testutils"
	brokermeta "github.com/onmetal/poollet/broker/meta"
	testdatav1 "github.com/onmetal/poollet/testdata/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("NamespaceController", func() {
	ctx := SetupContext()
	ns, provider := SetupTest(ctx)

	It("should sync the namespace", func() {
		By("creating a foo in the namespace")
		foo := &testdatav1.Foo{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "foo-",
			},
		}
		Expect(k8sClient.Create(ctx, foo)).To(Succeed())

		By("waiting for the reconciler to report a target")
		targetNS := &corev1.Namespace{}
		Eventually(func() error {
			return provider.Target(ctx, client.ObjectKey{Name: ns.Name}, targetNS)
		}).Should(Succeed())

		By("inspecting the namespace")
		Expect(targetNS.Name).NotTo(Equal(ns.Name))
		Expect(brokermeta.IsBrokerControlledBy(clusterName, ns, targetNS)).To(BeTrue(), "target is not broker-controlled")

		By("deleting the foo")
		Expect(k8sClient.Delete(ctx, foo)).To(Succeed())

		By("waiting for the target namespace to be gone")
		Eventually(Object(targetNS), 20*time.Second).Should(HaveField("Status.Phase", Equal(corev1.NamespaceTerminating)))
	})
})
