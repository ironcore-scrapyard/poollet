/*
 * Copyright (c) 2021 by the OnMetal authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package storage

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/onmetal/controller-utils/envtestutils"
	"github.com/onmetal/controller-utils/kustomizeutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var sourceStoragePoolLabels = map[string]string{
	"storagepool-kind": "source",
}

const (
	storagePoolName     = "my-pool"
	storagePoolName_Vol = "my-pool-vol"
	providerID          = "custom://pool"
	timeout             = 2 * time.Second
	interval            = 100 * time.Millisecond
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	onmetalCRDs := &apiextensionsv1.CustomResourceDefinitionList{}
	Expect(kustomizeutils.RunKustomizeIntoList(".", scheme.Codecs.UniversalDeserializer(), onmetalCRDs)).To(Succeed())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDs: envtestutils.CRDPtrsFromCRDs(onmetalCRDs.Items),
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = storagev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
}, 60)

func SetupTest(ctx context.Context) *corev1.Namespace {
	var (
		cancel context.CancelFunc
	)
	ns := &corev1.Namespace{}
	BeforeEach(func() {
		var mgrCtx context.Context
		mgrCtx, cancel = context.WithCancel(ctx)
		*ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "testns-",
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed(), "failed to create test namespace")

		k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:             scheme.Scheme,
			Host:               "127.0.0.1",
			MetricsBindAddress: "0",
		})
		Expect(err).ToNot(HaveOccurred())

		// register reconciler here
		Expect((&VolumeReconciler{
			Namespace:                 ns.Name,
			Client:                    k8sManager.GetClient(),
			ParentClient:              k8sManager.GetClient(),
			ParentCache:               k8sManager.GetCache(),
			ParentFieldIndexer:        k8sManager.GetFieldIndexer(),
			StoragePoolName:           storagePoolName_Vol,
			SourceStoragePoolSelector: sourceStoragePoolLabels,
		}).SetupWithManager(k8sManager)).To(Succeed())

		Expect((&StoragePoolReconciler{
			Client:                    k8sManager.GetClient(),
			ParentClient:              k8sManager.GetClient(),
			ParentCache:               k8sManager.GetCache(),
			StoragePoolName:           storagePoolName,
			ProviderID:                providerID,
			SourceStoragePoolSelector: sourceStoragePoolLabels,
		}).SetupWithManager(k8sManager)).To(Succeed())

		go func() {
			Expect(k8sManager.Start(mgrCtx)).To(Succeed(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		cancel()
		Expect(k8sClient.Delete(ctx, ns)).To(Succeed(), "failed to delete test namespace")
		Expect(k8sClient.DeleteAllOf(ctx, &storagev1alpha1.StoragePool{})).To(Succeed())
	})

	return ns
}

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
