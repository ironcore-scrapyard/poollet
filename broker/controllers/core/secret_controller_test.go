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

package core_test

import (
	. "github.com/onmetal/onmetal-api/testutils"
	testdatav1 "github.com/onmetal/poollet/testdata/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("SecretController", func() {
	ctx := SetupContext()
	ns, prov := SetupTest(ctx)

	It("should sync secrets when referenced", func() {
		By("creating a secret")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "secret-",
			},
			Data: map[string][]byte{
				"foo": []byte("bar"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		By("creating a foo referencing the secret")
		foo := &testdatav1.Foo{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "foo-",
			},
			Spec: testdatav1.FooSpec{
				Ref: &corev1.ObjectReference{
					APIVersion: corev1.SchemeGroupVersion.String(),
					Kind:       "Secret",
					Name:       secret.Name,
				},
			},
		}
		Expect(k8sClient.Create(ctx, foo)).To(Succeed())

		By("waiting for the secret to be synced")
		secretKey := client.ObjectKeyFromObject(secret)
		targetSecret := &corev1.Secret{}
		Eventually(func() error {
			return prov.Target(ctx, secretKey, targetSecret)
		}).Should(Succeed())

		By("inspecting the synced secret")
		Expect(targetSecret.Namespace).NotTo(Equal(secret.Namespace))
		Expect(targetSecret.Data).To(Equal(secret.Data))
	})
})
