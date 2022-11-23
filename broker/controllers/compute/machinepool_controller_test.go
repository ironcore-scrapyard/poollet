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

package compute_test

import (
	computev1alpha1 "github.com/onmetal/onmetal-api/api/compute/v1alpha1"
	. "github.com/onmetal/onmetal-api/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("MachinePoolController", func() {
	ctx := SetupContext()
	_, _ = SetupTest(ctx)

	It("should sync the machine pool", func() {
		By("waiting for the machine pool controller to create the machine pool")
		machinePool := &computev1alpha1.MachinePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: poolName,
			},
		}
		Eventually(Get(machinePool)).Should(Succeed())

		By("inspecting the machine pool")
		Expect(machinePool.Spec).To(Equal(computev1alpha1.MachinePoolSpec{
			ProviderID: providerID,
		}))
		Expect(machinePool.Status.AvailableMachineClasses).To(BeEmpty())

		By("creating a pool matching the selectors")
		matchingPool := &computev1alpha1.MachinePool{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "matching-",
				Labels:       targetPoolSelector,
			},
		}
		Expect(k8sClient.Create(ctx, matchingPool)).To(Succeed())

		By("patching the matching pool status to include a machine class")
		baseMatchingPool := matchingPool.DeepCopy()
		matchingPool.Status.AvailableMachineClasses = []corev1.LocalObjectReference{{Name: "additional"}}
		Expect(k8sClient.Status().Patch(ctx, matchingPool, client.MergeFrom(baseMatchingPool))).To(Succeed())

		By("waiting for the machine pool to include the machine class")
		Eventually(Object(machinePool)).Should(
			HaveField("Status.AvailableMachineClasses", ConsistOf(corev1.LocalObjectReference{Name: "additional"})),
		)

		By("deleting the matching machine pool")
		Expect(k8sClient.Delete(ctx, matchingPool)).To(Succeed())

		By("waiting for the machine classes to be empty again")
		Eventually(Object(machinePool)).Should(HaveField("Status.AvailableMachineClasses", BeEmpty()))
	})
})
