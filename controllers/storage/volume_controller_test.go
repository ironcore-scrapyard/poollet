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

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/partitionlet/names"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("VolumeController", func() {
	ctx := context.Background()
	ns := SetupTest(ctx)

	It("should sync a volume operated by the storage pool from the parent cluster", func() {
		By("creating a storage class")
		storageClass := &storagev1alpha1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "storageclass-",
			},
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())

		By("creating a parent volume w/o storage pool name")
		parent := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "parent-volume-",
			},
			Spec: storagev1alpha1.VolumeSpec{
				StorageClassRef: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
			},
		}
		Expect(k8sClient.Create(ctx, parent)).To(Succeed())

		By("asserting it is not synced")
		childKey := names.Must(namesStrategy.Key(ctx, client.ObjectKeyFromObject(parent), parent))
		child := &storagev1alpha1.Volume{}
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, childKey, child)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())

		By("patching the storage pool name of the parent")
		baseParent := parent.DeepCopy()
		parent.Spec.StoragePool.Name = storagePoolName
		Expect(k8sClient.Patch(ctx, parent, client.MergeFrom(baseParent))).To(Succeed())

		By("waiting for the volume to be synced")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, childKey, child)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(child.Spec).To(Equal(storagev1alpha1.VolumeSpec{
				StorageClassRef: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				StoragePoolSelector: sourceStoragePoolLabels,
				StoragePool: corev1.LocalObjectReference{
					Name: sourceStoragePoolName,
				},
			}))
		}, timeout, interval).Should(Succeed())

		By("deleting the parent volume")
		Expect(k8sClient.Delete(ctx, parent)).Should(Succeed())

		By("waiting for the volume to be gone")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, childKey, child)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())
	})

	It("should sync a volume referenced by a machine running on the machinepool from the parent cluster", func() {
		By("creating a storage class")
		storageClass := &storagev1alpha1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "storageclass-",
			},
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())

		By("creating a parent volume with another storage pool name")
		parent := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "parent-volume-",
			},
			Spec: storagev1alpha1.VolumeSpec{
				StorageClassRef: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				StoragePool: corev1.LocalObjectReference{
					Name: "other-storagepool",
				},
			},
		}
		Expect(k8sClient.Create(ctx, parent)).To(Succeed())

		By("asserting it is not synced")
		childKey := names.Must(namesStrategy.Key(ctx, client.ObjectKeyFromObject(parent), parent))
		child := &storagev1alpha1.Volume{}
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, childKey, child)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())

		By("creating a parent volume claim")
		parentClaim := &storagev1alpha1.VolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "parent-claim-",
			},
			Spec: storagev1alpha1.VolumeClaimSpec{
				VolumeRef: corev1.LocalObjectReference{
					Name: parent.Name,
				},
				StorageClassRef: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				Resources: corev1.ResourceList{
					"storage": resource.MustParse("1Gi"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, parentClaim)).To(Succeed())

		By("patching the parent volume's claim reference")
		baseParent := parent.DeepCopy()
		parent.Spec.ClaimRef = storagev1alpha1.ClaimReference{
			Name: parentClaim.Name,
			UID:  parentClaim.UID,
		}
		Expect(k8sClient.Patch(ctx, parent, client.MergeFrom(baseParent))).To(Succeed())

		By("asserting it is still not synced")
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, childKey, child)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())

		By("creating a parent machine maintained by the machine pool using the volume claim")
		parentMachine := &computev1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "machine-",
			},
			Spec: computev1alpha1.MachineSpec{
				VolumeAttachments: []computev1alpha1.VolumeAttachment{
					{
						Name:     parentClaim.Name,
						Priority: 0,
						VolumeAttachmentSource: computev1alpha1.VolumeAttachmentSource{
							VolumeClaim: &computev1alpha1.VolumeClaimAttachmentSource{
								Ref: corev1.LocalObjectReference{Name: parentClaim.Name},
							},
						},
					},
				},
				MachinePool: corev1.LocalObjectReference{Name: machinePoolName},
			},
		}
		Expect(k8sClient.Create(ctx, parentMachine)).To(Succeed())

		By("waiting for the volume to be synced")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, childKey, child)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(child.Spec).To(Equal(storagev1alpha1.VolumeSpec{
				StorageClassRef: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				StoragePoolSelector: sourceStoragePoolLabels,
				StoragePool: corev1.LocalObjectReference{
					Name: sourceStoragePoolName,
				},
			}))
		}, timeout, interval).Should(Succeed())

		By("deleting the parent machine & claim")
		Expect(k8sClient.Delete(ctx, parentMachine)).To(Succeed())
		Expect(k8sClient.Delete(ctx, parentClaim)).To(Succeed())

		By("waiting for the volume to be gone")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, childKey, child)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())
	})
})
