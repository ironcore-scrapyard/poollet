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

package storage

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
)

var _ = Describe("StoragePoolController", func() {
	ctx := context.Background()
	_ = SetupTest(ctx)

	It("should sync its storage pool from the source storage pools", func() {
		By("creating a source storage pool")
		sourcePool1 := &storagev1alpha1.StoragePool{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "source-pool-1-",
				Labels:       sourceStoragePoolLabels,
			},
			Spec: storagev1alpha1.StoragePoolSpec{
				ProviderID: "source://pool-1",
			},
		}
		Expect(k8sClient.Create(ctx, sourcePool1)).To(Succeed())

		By("updating the available storage classes of that pool")
		sourcePool1.Status.AvailableStorageClasses = []corev1.LocalObjectReference{{Name: "foo"}}
		Expect(k8sClient.Status().Update(ctx, sourcePool1)).To(Succeed())

		By("creating another source storage pool")
		sourcePool2 := &storagev1alpha1.StoragePool{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "source-pool-2-",
				Labels:       sourceStoragePoolLabels,
			},
			Spec: storagev1alpha1.StoragePoolSpec{
				ProviderID: "source://pool-2",
			},
		}
		Expect(k8sClient.Create(ctx, sourcePool2)).To(Succeed())

		By("updating the available storage classes of that pool")
		sourcePool2.Status.AvailableStorageClasses = []corev1.LocalObjectReference{{Name: "bar"}}
		Expect(k8sClient.Status().Update(ctx, sourcePool2)).To(Succeed())

		By("waiting for the accumulating storage pool to report both classes")
		Eventually(func(g Gomega) {
			pool := &storagev1alpha1.StoragePool{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: storagePoolName}, pool)).To(Succeed())

			By("inspecting the pool")
			Expect(pool.Spec.ProviderID).To(Equal(storagePoolProviderID))
			g.Expect(pool.Status.AvailableStorageClasses).To(ConsistOf(
				corev1.LocalObjectReference{Name: "foo"},
				corev1.LocalObjectReference{Name: "bar"},
			))
		}, timeout, interval).Should(Succeed())
	})
})
