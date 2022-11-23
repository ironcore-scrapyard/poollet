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

package storage_test

import (
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	. "github.com/onmetal/onmetal-api/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("VolumePoolController", func() {
	ctx := SetupContext()
	_, _ = SetupTest(ctx)

	It("should sync the volume pool", func() {
		By("waiting for the volume pool controller to create the volume pool")
		volumePool := &storagev1alpha1.VolumePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: poolName,
			},
		}
		Eventually(Get(volumePool)).Should(Succeed())

		By("inspecting the volume pool")
		Expect(volumePool.Spec).To(Equal(storagev1alpha1.VolumePoolSpec{
			ProviderID: providerID,
		}))
		Expect(volumePool.Status.AvailableVolumeClasses).To(BeEmpty())

		By("creating a pool matching the selectors")
		matchingPool := &storagev1alpha1.VolumePool{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "matching-",
				Labels:       targetPoolSelector,
			},
		}
		Expect(k8sClient.Create(ctx, matchingPool)).To(Succeed())

		By("patching the matching pool status to include a volume class")
		baseMatchingPool := matchingPool.DeepCopy()
		matchingPool.Status.AvailableVolumeClasses = []corev1.LocalObjectReference{{Name: "additional"}}
		Expect(k8sClient.Status().Patch(ctx, matchingPool, client.MergeFrom(baseMatchingPool))).To(Succeed())

		By("waiting for the volume pool to include the volume class")
		Eventually(Object(volumePool)).Should(
			HaveField("Status.AvailableVolumeClasses", ConsistOf(corev1.LocalObjectReference{Name: "additional"})),
		)

		By("deleting the matching volume pool")
		Expect(k8sClient.Delete(ctx, matchingPool)).To(Succeed())

		By("waiting for the volume classes to be empty again")
		Eventually(Object(volumePool)).Should(HaveField("Status.AvailableVolumeClasses", BeEmpty()))
	})
})
