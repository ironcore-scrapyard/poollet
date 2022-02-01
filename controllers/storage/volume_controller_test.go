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

	partitionletstoragev1alpha1 "github.com/onmetal/partitionlet/apis/storage/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo"
)

var _ = Describe("VolumeController", func() {
	ctx := context.Background()
	ns := SetupTest(ctx)

	It("should sync a volume from the parent cluster", func() {
		By("creating a storage class")
		storageClass := &storagev1alpha1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "class-",
			},
			Spec: storagev1alpha1.StorageClassSpec{
				Capabilities: corev1.ResourceList{
					"iops":       resource.MustParse("1000"),
					"throughput": resource.MustParse("100"),
					"encryption": resource.MustParse("1"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())

		By("creating a volume")
		parentVolume := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "volume-",
			},
			Spec: storagev1alpha1.VolumeSpec{
				StorageClass: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				StoragePool: corev1.LocalObjectReference{
					Name: storagePoolName,
				},
				StoragePoolSelector: sourceStoragePoolLabels,
			},
		}
		Expect(k8sClient.Create(ctx, parentVolume)).To(Succeed())
		By("waiting for the volume to be synced")
		Eventually(func(g Gomega) {
			key := client.ObjectKey{Namespace: ns.Name, Name: partitionletstoragev1alpha1.VolumeName(ns.Name, parentVolume.Name)}
			volume := &storagev1alpha1.Volume{}
			err := k8sClient.Get(ctx, key, volume)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(volume.Spec).To(Equal(storagev1alpha1.VolumeSpec{
				StorageClass:        corev1.LocalObjectReference{Name: storageClass.Name},
				StoragePoolSelector: sourceStoragePoolLabels,
			}))
		}, timeout, interval).Should(Succeed())
	})

	It("should delete a synced volume if the parent volume gets deleted", func() {
		By("creating a storage class")
		storageClass := &storagev1alpha1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "class-",
			},
			Spec: storagev1alpha1.StorageClassSpec{
				Capabilities: corev1.ResourceList{
					"iops":       resource.MustParse("1000"),
					"throughput": resource.MustParse("100"),
					"encryption": resource.MustParse("1"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())

		By("creating a volume")
		parentVolume := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "volume-",
			},
			Spec: storagev1alpha1.VolumeSpec{
				StorageClass: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				StoragePool: corev1.LocalObjectReference{
					Name: storagePoolName,
				},
				StoragePoolSelector: sourceStoragePoolLabels,
			},
		}
		Expect(k8sClient.Create(ctx, parentVolume)).To(Succeed())

		By("waiting for the volume to be synced")
		volumeKey := client.ObjectKey{Namespace: ns.Name, Name: partitionletstoragev1alpha1.VolumeName(ns.Name, parentVolume.Name)}
		volume := &storagev1alpha1.Volume{}
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, volumeKey, volume)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(volume.Spec).To(Equal(storagev1alpha1.VolumeSpec{
				StorageClass:        corev1.LocalObjectReference{Name: storageClass.Name},
				StoragePoolSelector: sourceStoragePoolLabels,
			}))
		}, timeout, interval).Should(Succeed())

		By("deleting the parent volume")
		Expect(k8sClient.Delete(ctx, parentVolume)).To(Succeed())

		By("waiting for the volume to be gone")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, volumeKey, volume)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())

		By("waiting for the parent volume to be gone")
		parentVolumeKey := client.ObjectKeyFromObject(parentVolume)
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, parentVolumeKey, parentVolume)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())
	})

	It("should delete a synced volume but block deletion if the synced volume's deletion is blocked", func() {
		By("creating a storage class")
		storageClass := &storagev1alpha1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "class-",
			},
			Spec: storagev1alpha1.StorageClassSpec{
				Capabilities: corev1.ResourceList{
					"iops":       resource.MustParse("1000"),
					"throughput": resource.MustParse("100"),
					"encryption": resource.MustParse("1"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())

		By("creating a volume")
		parentVolume := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "volume-",
			},
			Spec: storagev1alpha1.VolumeSpec{
				StorageClass: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				StoragePool: corev1.LocalObjectReference{
					Name: storagePoolName,
				},
				StoragePoolSelector: sourceStoragePoolLabels,
			},
		}
		Expect(k8sClient.Create(ctx, parentVolume)).To(Succeed())

		By("waiting for the volume to be synced")
		volumeKey := client.ObjectKey{Namespace: ns.Name, Name: partitionletstoragev1alpha1.VolumeName(ns.Name, parentVolume.Name)}
		volume := &storagev1alpha1.Volume{}
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, volumeKey, volume)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(volume.Spec).To(Equal(storagev1alpha1.VolumeSpec{
				StorageClass:        corev1.LocalObjectReference{Name: storageClass.Name},
				StoragePoolSelector: sourceStoragePoolLabels,
			}))
		}, timeout, interval).Should(Succeed())

		By("adding a finalizer to block deletion on the volume")
		controllerutil.AddFinalizer(volume, "finalizer.block.test")
		Expect(k8sClient.Update(ctx, volume)).To(Succeed())

		By("deleting the parent volume")
		Expect(k8sClient.Delete(ctx, parentVolume)).To(Succeed())

		By("asserting the parent volume and child volume are both still there")
		parentVolumeKey := client.ObjectKeyFromObject(parentVolume)
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volumeKey, volume)).To(Succeed())
			g.Expect(k8sClient.Get(ctx, parentVolumeKey, parentVolume)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		By("removing the finalizer from the child volume")
		controllerutil.RemoveFinalizer(volume, "finalizer.block.test")
		Expect(k8sClient.Update(ctx, volume)).To(Succeed())

		By("waiting for the parent and child volume to be gone")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, parentVolumeKey, parentVolume)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)

			err = k8sClient.Get(ctx, volumeKey, volume)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())
	})
})
