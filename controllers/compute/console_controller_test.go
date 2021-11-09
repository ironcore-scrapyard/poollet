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

	commonv1alpha1 "github.com/onmetal/onmetal-api/apis/common/v1alpha1"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	partitionletcomputev1alpha1 "github.com/onmetal/partitionlet/apis/compute/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ConsoleController", func() {
	ctx := context.Background()
	ns := SetupTest(ctx)

	It("should sync consoles and the secrets from the parent", func() {
		By("creating a machine")
		parentMachine := &computev1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "machine-",
			},
			Spec: computev1alpha1.MachineSpec{
				MachinePool: corev1.LocalObjectReference{
					Name: machinePoolName,
				},
			},
		}
		Expect(k8sClient.Create(ctx, parentMachine)).To(Succeed())

		By("creating a lighthouse client secret")
		parentClientSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "client-secret-",
			},
			Data: map[string][]byte{
				"foo": []byte("bar"),
			},
		}
		Expect(k8sClient.Create(ctx, parentClientSecret)).To(Succeed())

		By("creating a console referencing that machine")
		parentConsole := &computev1alpha1.Console{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "console-",
			},
			Spec: computev1alpha1.ConsoleSpec{
				Type:       computev1alpha1.ConsoleTypeLightHouse,
				MachineRef: corev1.LocalObjectReference{Name: parentMachine.Name},
				LighthouseClientConfig: &computev1alpha1.ConsoleClientConfig{
					KeySecret: &commonv1alpha1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: parentClientSecret.Name},
						Key:                  "foo",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, parentConsole)).To(Succeed())

		By("waiting for the console and the secret to be synced down")
		console := &computev1alpha1.Console{}
		Eventually(func(g Gomega) {
			clientSecret := &corev1.Secret{}
			clientSecretKey := client.ObjectKey{Namespace: ns.Name, Name: partitionletcomputev1alpha1.ConsoleSecretName(parentConsole.Namespace, parentConsole.Name)}
			err := k8sClient.Get(ctx, clientSecretKey, clientSecret)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(clientSecret.Data).To(Equal(map[string][]byte{"foo": []byte("bar")}))

			consoleKey := client.ObjectKey{Namespace: ns.Name, Name: partitionletcomputev1alpha1.ConsoleName(parentConsole.Namespace, parentConsole.Name)}
			err = k8sClient.Get(ctx, consoleKey, console)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(console.Spec).To(Equal(computev1alpha1.ConsoleSpec{
				Type: computev1alpha1.ConsoleTypeLightHouse,
				MachineRef: corev1.LocalObjectReference{
					Name: partitionletcomputev1alpha1.MachineName(parentMachine.Namespace, parentMachine.Name),
				},
				LighthouseClientConfig: &computev1alpha1.ConsoleClientConfig{
					KeySecret: &commonv1alpha1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: partitionletcomputev1alpha1.ConsoleSecretName(parentConsole.Namespace, parentConsole.Name),
						},
						Key: "foo",
					},
				},
			}))
		}, timeout, interval).Should(Succeed())

		By("modifying the console state")
		console.Status.State = computev1alpha1.ConsoleStateReady
		Expect(k8sClient.Status().Update(ctx, console)).To(Succeed())

		By("waiting for the console state to by synced up again")
		parentConsoleKey := client.ObjectKeyFromObject(parentConsole)
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, parentConsoleKey, parentConsole)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(parentConsole.Status.State).To(Equal(computev1alpha1.ConsoleStateReady))
		}, timeout, interval).Should(Succeed())
	})
})
