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
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onmetal/onmetal-api/testutils"
	brokermeta "github.com/onmetal/poollet/broker/meta"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("VolumeController", func() {
	ctx := SetupContext()
	ns, provider := SetupTest(ctx)

	It("should sync a volume", func() {
		By("creating a volume")
		volume := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "volume-",
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: corev1.LocalObjectReference{
					Name: "my-class",
				},
				VolumePoolRef: &corev1.LocalObjectReference{
					Name: poolName,
				},
				Resources: map[corev1.ResourceName]resource.Quantity{
					"storage": resource.MustParse("1Gi"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, volume)).To(Succeed())

		By("waiting for the target volume to be available")
		targetVolume := &storagev1alpha1.Volume{}
		Eventually(func() error {
			return provider.Target(ctx, client.ObjectKeyFromObject(volume), targetVolume)
		}).Should(Succeed())

		By("inspecting the volume")
		Expect(targetVolume.Namespace).NotTo(Equal(volume.Namespace))
		Expect(targetVolume.Name).To(Equal(volume.Name))
		Expect(targetVolume).To(
			SatisfyAll(
				BeBrokerControlled(clusterName, volume),
				HaveField("Spec", Equal(storagev1alpha1.VolumeSpec{
					VolumeClassRef:     corev1.LocalObjectReference{Name: "my-class"},
					VolumePoolSelector: targetPoolSelector,
					Resources: map[corev1.ResourceName]resource.Quantity{
						"storage": resource.MustParse("1Gi"),
					},
				})),
			),
		)

		By("deleting the volume")
		Expect(k8sClient.Delete(ctx, volume)).To(Succeed())

		By("waiting for the target volume to be gone")
		Eventually(Get(targetVolume)).Should(Satisfy(apierrors.IsNotFound))
	})
})

func BeBrokerControlled(clusterName string, brokerOwner client.Object) types.GomegaMatcher {
	return Satisfy(func(obj client.Object) bool {
		return brokermeta.IsBrokerControlledBy(clusterName, brokerOwner, obj)
	})
}
