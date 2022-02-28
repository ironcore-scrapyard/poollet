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
	"context"

	. "github.com/onmetal/controller-utils/testutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/partitionlet/names"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/onmetal/onmetal-api/apis/common/v1alpha1"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
)

var _ = Describe("MachineController", func() {
	ctx := context.Background()
	ns := SetupTest(ctx)

	It("should sync a machine from the parent cluster", func() {
		By("creating a storage class")
		storageClass := &storagev1alpha1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "storage-class-",
			},
			Spec: storagev1alpha1.StorageClassSpec{
				Capabilities: corev1.ResourceList{
					"iops": resource.MustParse("1000"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())

		By("creating a volume using that storage class")
		parentVolume := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "machine-volume-",
			},
			Spec: storagev1alpha1.VolumeSpec{
				StorageClassRef: corev1.LocalObjectReference{
					Name: storageClass.Name,
				},
				Resources: corev1.ResourceList{
					"storage": resource.MustParse("20Gi"),
				},
				StoragePool: corev1.LocalObjectReference{
					Name: "some-storage-pool",
				},
			},
		}
		Expect(k8sClient.Create(ctx, parentVolume)).To(Succeed())

		By("creating a volume claim claiming that volume")
		parentVolumeClaim := &storagev1alpha1.VolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "machine-volumeclaim-",
			},
			Spec: storagev1alpha1.VolumeClaimSpec{
				VolumeRef: corev1.LocalObjectReference{Name: parentVolume.Name},
				Resources: corev1.ResourceList{
					"storage": resource.MustParse("20Gi"),
				},
				StorageClassRef: corev1.LocalObjectReference{Name: storageClass.Name},
			},
		}
		Expect(k8sClient.Create(ctx, parentVolumeClaim)).To(Succeed())

		By("patching the volume claim to bound")
		parentVolumeClaimBase := parentVolumeClaim.DeepCopy()
		parentVolumeClaim.Status.Phase = storagev1alpha1.VolumeClaimBound
		Expect(k8sClient.Status().Patch(ctx, parentVolumeClaim, client.MergeFrom(parentVolumeClaimBase))).To(Succeed())

		By("creating a machine class")
		machineClass := &computev1alpha1.MachineClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "class-",
			},
			Spec: computev1alpha1.MachineClassSpec{
				Capabilities: corev1.ResourceList{
					"cpu":    resource.MustParse("1"),
					"memory": resource.MustParse("1Gi"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, machineClass)).To(Succeed())

		By("creating an ignition config map")
		parentIgnition := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "cm-ignition-",
			},
			Data: map[string]string{
				computev1alpha1.DefaultIgnitionKey: "my: ignition",
			},
		}
		Expect(k8sClient.Create(ctx, parentIgnition)).To(Succeed())

		By("creating a machine w/ machine class, ignition and attachment")
		parentMachine := &computev1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "machine-",
			},
			Spec: computev1alpha1.MachineSpec{
				MachineClass: corev1.LocalObjectReference{
					Name: machineClass.Name,
				},
				MachinePool: corev1.LocalObjectReference{
					Name: machinePoolName,
				},
				Ignition: &commonv1alpha1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: parentIgnition.Name,
					},
				},
				Interfaces: []computev1alpha1.Interface{
					{
						Name: "myinterface",
						Target: corev1.LocalObjectReference{
							Name: "my-subnet",
						},
					},
				},
				VolumeAttachments: []computev1alpha1.VolumeAttachment{
					{
						Name: "myvolume",
						VolumeAttachmentSource: computev1alpha1.VolumeAttachmentSource{
							VolumeClaim: &computev1alpha1.VolumeClaimAttachmentSource{
								Ref: corev1.LocalObjectReference{Name: parentVolumeClaim.Name},
							},
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, parentMachine)).To(Succeed())

		By("waiting for the machine & ignition to be synced")
		machineKey := names.Must(namesStrategy.Key(ctx, client.ObjectKeyFromObject(parentMachine), parentMachine))
		machine := &computev1alpha1.Machine{}
		ignition := &corev1.ConfigMap{}
		volumeClaim := &storagev1alpha1.VolumeClaim{}
		volumeKey := names.Must(namesStrategy.Key(ctx, client.ObjectKeyFromObject(parentVolume), parentVolume))
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, machineKey, machine)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(machine.Spec).To(MatchFields(IgnoreExtras|IgnoreMissing, Fields{
				"MachineClass":        Equal(corev1.LocalObjectReference{Name: machineClass.Name}),
				"MachinePoolSelector": Equal(sourceMachinePoolLabels),
				"MachinePool":         Equal(corev1.LocalObjectReference{Name: sourceMachinePoolName}),
				"Interfaces": Equal([]computev1alpha1.Interface{
					{
						Name: "myinterface",
						Target: corev1.LocalObjectReference{
							Name: "my-subnet",
						},
					},
				}),
				"Ignition": Equal(&commonv1alpha1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: MachineIgnitionNameFromMachineName(machineKey.Name),
					},
				}),
				"VolumeAttachments": ConsistOf(MatchFields(IgnoreExtras|IgnoreMissing, Fields{
					"Name": Equal("myvolume"),
					"VolumeAttachmentSource": MatchFields(IgnoreExtras|IgnoreMissing, Fields{
						"VolumeClaim": PointTo(MatchFields(IgnoreExtras|IgnoreMissing, Fields{
							"Ref": MatchFields(IgnoreExtras|IgnoreMissing, Fields{
								"Name": Not(BeEmpty()),
							}),
						})),
					}),
				})),
			}))

			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: machine.Spec.Ignition.Name}, ignition)).To(Succeed())
			g.Expect(ignition.Data[computev1alpha1.DefaultIgnitionKey]).To(Equal("my: ignition"))

			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: machine.Spec.VolumeAttachments[0].VolumeClaim.Ref.Name}, volumeClaim)).To(Succeed())
			g.Expect(volumeClaim.Spec).To(Equal(storagev1alpha1.VolumeClaimSpec{
				VolumeRef:       corev1.LocalObjectReference{Name: volumeKey.Name},
				Selector:        nil,
				Resources:       parentVolume.Spec.Resources,
				StorageClassRef: parentVolume.Spec.StorageClassRef,
			}))
		}, timeout, interval).Should(Succeed())

		By("patching the parent machine's interface status")
		baseParentMachine := parentMachine.DeepCopy()
		parentMachine.Status.Interfaces = []computev1alpha1.InterfaceStatus{{
			Name: "myinterface",
			IP:   commonv1alpha1.MustParseIP("10.0.0.1"),
		}}
		Expect(k8sClient.Status().Patch(ctx, parentMachine, client.MergeFrom(baseParentMachine))).To(Succeed())

		By("waiting for the machine interfaces to be synced")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, machineKey, machine)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(machine.Status.Interfaces).To(Equal([]computev1alpha1.InterfaceStatus{{
				Name: "myinterface",
				IP:   commonv1alpha1.MustParseIP("10.0.0.1"),
			}}))
		}, timeout, interval).Should(Succeed())

		By("deleting the parent machine")
		Expect(k8sClient.Delete(ctx, parentMachine)).To(Succeed())

		By("waiting for the machine and all its dependencies to be gone")
		ignitionKey := client.ObjectKeyFromObject(ignition)
		volumeClaimKey := client.ObjectKeyFromObject(volumeClaim)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, machineKey, machine)).To(MatchErrorFunc(apierrors.IsNotFound))
			g.Expect(k8sClient.Get(ctx, ignitionKey, ignition)).To(MatchErrorFunc(apierrors.IsNotFound))
			g.Expect(k8sClient.Get(ctx, volumeClaimKey, volumeClaim)).To(MatchErrorFunc(apierrors.IsNotFound))
		}, timeout, interval).Should(Succeed())
	})
})
