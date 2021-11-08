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

package compute

import (
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("MachinePoolController", func() {
	ctx := controllerruntime.SetupSignalHandler()
	_ = SetupTest(ctx)

	It("should sync its machine pool from the source machine pools", func() {
		By("creating a source machine pool")
		sourcePool1 := &computev1alpha1.MachinePool{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "source-pool-1-",
				Labels:       sourceMachinePoolLabels,
			},
			Spec: computev1alpha1.MachinePoolSpec{
				ProviderID: "source://pool-1",
			},
		}
		Expect(k8sClient.Create(ctx, sourcePool1)).To(Succeed())

		By("updating the available machine classes of that pool")
		sourcePool1.Status.AvailableMachineClasses = []corev1.LocalObjectReference{{Name: "foo"}}
		Expect(k8sClient.Status().Update(ctx, sourcePool1)).To(Succeed())

		By("creating another source machine pool")
		sourcePool2 := &computev1alpha1.MachinePool{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "source-pool-2-",
				Labels:       sourceMachinePoolLabels,
			},
			Spec: computev1alpha1.MachinePoolSpec{
				ProviderID: "source://pool-2",
			},
		}
		Expect(k8sClient.Create(ctx, sourcePool2)).To(Succeed())

		By("updating the available machine classes of that pool")
		sourcePool2.Status.AvailableMachineClasses = []corev1.LocalObjectReference{{Name: "bar"}}
		Expect(k8sClient.Status().Update(ctx, sourcePool2)).To(Succeed())

		By("waiting for the accumulating machine pool to report both classes")
		Eventually(func(g Gomega) {
			pool := &computev1alpha1.MachinePool{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "my-pool"}, pool)).To(Succeed())

			By("inspecting the pool")
			Expect(pool.Spec.ProviderID).To(Equal("custom://pool"))
			g.Expect(pool.Status.AvailableMachineClasses).To(ConsistOf(
				corev1.LocalObjectReference{Name: "foo"},
				corev1.LocalObjectReference{Name: "bar"},
			))
		}, timeout, interval).Should(Succeed())
	})
})
