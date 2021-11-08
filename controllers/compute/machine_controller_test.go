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

	partitionletcomputev1alpha1 "github.com/onmetal/partitionlet/apis/compute/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo"
)

var _ = Describe("MachineController", func() {
	ctx := context.Background()
	ns := SetupTest(ctx)

	It("should sync a machine from the parent cluster", func() {
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

		By("creating a machine w/ that machine class")
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
			},
		}
		Expect(k8sClient.Create(ctx, parentMachine)).To(Succeed())

		By("waiting for the machine to be synced")
		Eventually(func(g Gomega) {
			key := client.ObjectKey{Namespace: ns.Name, Name: partitionletcomputev1alpha1.MachineName(ns.Name, parentMachine.Name)}
			machine := &computev1alpha1.Machine{}
			err := k8sClient.Get(ctx, key, machine)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(machine.Spec).To(Equal(computev1alpha1.MachineSpec{
				MachineClass:        corev1.LocalObjectReference{Name: machineClass.Name},
				MachinePoolSelector: sourceMachinePoolLabels,
				Image:               parentMachine.Spec.Image,
			}))
		}, timeout, interval).Should(Succeed())
	})

	It("should reconcile machines if the machine class appears later than the machine", func() {
		const machineClassName = "later-class"
		By("creating a machine w/ that machine class")
		parentMachine := &computev1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "machine-",
			},
			Spec: computev1alpha1.MachineSpec{
				MachineClass: corev1.LocalObjectReference{
					Name: machineClassName,
				},
				MachinePool: corev1.LocalObjectReference{
					Name: machinePoolName,
				},
			},
		}
		Expect(k8sClient.Create(ctx, parentMachine)).To(Succeed())

		By("creating a machine class")
		machineClass := &computev1alpha1.MachineClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: machineClassName,
			},
			Spec: computev1alpha1.MachineClassSpec{
				Capabilities: corev1.ResourceList{
					"cpu":    resource.MustParse("1"),
					"memory": resource.MustParse("1Gi"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, machineClass)).To(Succeed())

		By("waiting for the machine to be synced")
		Eventually(func(g Gomega) {
			key := client.ObjectKey{Namespace: ns.Name, Name: partitionletcomputev1alpha1.MachineName(ns.Name, parentMachine.Name)}
			machine := &computev1alpha1.Machine{}
			err := k8sClient.Get(ctx, key, machine)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(machine.Spec).To(Equal(computev1alpha1.MachineSpec{
				MachineClass:        corev1.LocalObjectReference{Name: machineClassName},
				MachinePoolSelector: sourceMachinePoolLabels,
				Image:               parentMachine.Spec.Image,
			}))
		}, timeout, interval).Should(Succeed())
	})
})
