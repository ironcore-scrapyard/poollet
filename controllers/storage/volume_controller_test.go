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
	"time"

	"github.com/onmetal/controller-utils/metautils"
	. "github.com/onmetal/controller-utils/testutils"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/partitionlet/strategy"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
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
		parentVolume := &storagev1alpha1.Volume{
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
		Expect(k8sClient.Create(ctx, parentVolume)).To(Succeed())

		By("asserting it is not synced")
		volumeKey := strategy.MustKey(strat.Key(parentVolume))
		volume := &storagev1alpha1.Volume{}
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, volumeKey, volume)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "error is not a not-found error: %v", err)
		}, timeout, interval).Should(Succeed())

		By("patching the storage pool name of the parent")
		baseParent := parentVolume.DeepCopy()
		parentVolume.Spec.StoragePool.Name = storagePoolName
		Expect(k8sClient.Patch(ctx, parentVolume, client.MergeFrom(baseParent))).To(Succeed())

		By("waiting for the volume to be synced")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, volumeKey, volume)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(volume.Spec).To(Equal(storagev1alpha1.VolumeSpec{
				StorageClassRef: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				StoragePoolSelector: sourceStoragePoolLabels,
				StoragePool: corev1.LocalObjectReference{
					Name: sourceStoragePoolName,
				},
			}))
		}, timeout, interval).Should(Succeed())

		By("creating an access secret")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    volumeKey.Namespace,
				GenerateName: "volume-access-",
			},
			Data: map[string][]byte{"foo": []byte("bar")},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		By("patching the volume's status to include the secret access data")
		baseChild := volume.DeepCopy()
		volume.Status.Access = &storagev1alpha1.VolumeAccess{
			SecretRef:        corev1.LocalObjectReference{Name: secret.Name},
			Driver:           "custom-driver",
			VolumeAttributes: map[string]string{"some": "attribute"},
		}
		Expect(k8sClient.Status().Patch(ctx, volume, client.MergeFrom(baseChild))).To(Succeed())

		By("waiting for the parent volume's status to be updated")
		parentVolumeKey := client.ObjectKeyFromObject(parentVolume)
		parentAccessSecret := &corev1.Secret{}
		var parentAccessSecretKey client.ObjectKey
		Eventually(func(g Gomega) {
			Expect(k8sClient.Get(ctx, parentVolumeKey, parentVolume)).To(Succeed())
			g.Expect(parentVolume.Status).To(MatchFields(IgnoreMissing|IgnoreExtras, Fields{
				"Access": PointTo(MatchFields(IgnoreMissing|IgnoreExtras, Fields{
					"SecretRef":        Not(BeZero()),
					"Driver":           Equal("custom-driver"),
					"VolumeAttributes": Equal(map[string]string{"some": "attribute"}),
				})),
			}))

			parentAccessSecretKey = client.ObjectKey{Namespace: parentVolume.Namespace, Name: parentVolume.Status.Access.SecretRef.Name}
			g.Expect(k8sClient.Get(ctx, parentAccessSecretKey, parentAccessSecret)).To(Succeed())
			g.Expect(parentAccessSecret.Data).To(Equal(map[string][]byte{"foo": []byte("bar")}))
			g.Expect(metautils.IsControlledBy(scheme.Scheme, parentVolume, parentAccessSecret))
		}, timeout, interval).Should(Succeed())

		By("deleting the parent volume")
		Expect(k8sClient.Delete(ctx, parentVolume)).Should(Succeed())

		By("waiting for the volume and its dependencies to be gone")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volumeKey, volume)).To(MatchErrorFunc(apierrors.IsNotFound))
		}, 10*time.Hour, interval).Should(Succeed())
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
		parentVolume := &storagev1alpha1.Volume{
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
		Expect(k8sClient.Create(ctx, parentVolume)).To(Succeed())

		By("asserting it is not synced")
		volumeKey := strategy.MustKey(strat.Key(parentVolume))
		volume := &storagev1alpha1.Volume{}
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volumeKey, volume)).To(MatchErrorFunc(apierrors.IsNotFound))
		}, timeout, interval).Should(Succeed())

		By("creating a parent volume claim")
		parentClaim := &storagev1alpha1.VolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "parent-claim-",
			},
			Spec: storagev1alpha1.VolumeClaimSpec{
				VolumeRef: corev1.LocalObjectReference{
					Name: parentVolume.Name,
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
		baseParent := parentVolume.DeepCopy()
		parentVolume.Spec.ClaimRef = storagev1alpha1.ClaimReference{
			Name: parentClaim.Name,
			UID:  parentClaim.UID,
		}
		Expect(k8sClient.Patch(ctx, parentVolume, client.MergeFrom(baseParent))).To(Succeed())

		By("asserting it is still not synced")
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volumeKey, volume)).To(MatchErrorFunc(apierrors.IsNotFound))
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
			err := k8sClient.Get(ctx, volumeKey, volume)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(volume.Spec).To(Equal(storagev1alpha1.VolumeSpec{
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
			g.Expect(k8sClient.Get(ctx, volumeKey, volume)).To(MatchErrorFunc(apierrors.IsNotFound))
		}, timeout, interval).Should(Succeed())
	})
})
