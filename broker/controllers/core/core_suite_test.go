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
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onmetal/poollet/broker"
	brokercluster "github.com/onmetal/poollet/broker/cluster"
	brokercontrollerscommon "github.com/onmetal/poollet/broker/controllers/common"
	"github.com/onmetal/poollet/broker/controllers/core"
	brokermeta "github.com/onmetal/poollet/broker/meta"
	"github.com/onmetal/poollet/broker/provider"
	testdatav1 "github.com/onmetal/poollet/testdata/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	clusterName = "test-cluster"
)

var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	k8sClient client.Client
	domain    = brokercontrollerscommon.Domain.Subdomain("test")
)

const (
	slowSpecThreshold    = 10 * time.Second
	eventuallyTimeout    = 3 * time.Second
	pollingInterval      = 50 * time.Millisecond
	consistentlyDuration = 1 * time.Second
)

func TestCore(t *testing.T) {
	_, reporterConfig := GinkgoConfiguration()
	reporterConfig.SlowSpecThreshold = slowSpecThreshold
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Core Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	var err error
	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "testdata", "config", "crd"),
		},
	}
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	DeferCleanup(testEnv.Stop)

	Expect(testdatav1.AddToScheme(scheme.Scheme)).To(Succeed())

	// Init package-level k8sClient
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
	SetClient(k8sClient)
})

func SetupFooToTypeField(ctx context.Context, cluster brokercluster.Cluster, referredType client.Object) (string, error) {
	referredGVK, err := apiutil.GVKForObject(referredType, cluster.GetScheme())
	if err != nil {
		return "", err
	}

	field := strings.ToLower(referredGVK.GroupVersion().String()) + "-name"

	return field, cluster.GetFieldIndexer().IndexField(ctx, &testdatav1.Foo{}, field, func(object client.Object) []string {
		foo := object.(*testdatav1.Foo)
		ref := foo.Spec.Ref
		if ref == nil {
			return []string{""}
		}

		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			return nil
		}

		if gv == referredGVK.GroupVersion() && ref.Kind == referredGVK.Kind {
			return []string{ref.Name}
		}
		return nil
	})
}

func SetupTest(ctx context.Context) (*corev1.Namespace, provider.Provider) {
	var (
		cancel context.CancelFunc
	)
	ns := &corev1.Namespace{}
	providerRegistry := &provider.Registry{}

	BeforeEach(func() {
		var mgrCtx context.Context
		mgrCtx, cancel = context.WithCancel(ctx)

		*ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-ns-",
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed(), "failed to create test namespace")

		targetCluster, err := brokercluster.New(cfg, func(opts *cluster.Options) { opts.Scheme = scheme.Scheme })
		Expect(err).NotTo(HaveOccurred())

		k8sManager, err := broker.NewManager(cfg, targetCluster, broker.Options{
			Scheme:             scheme.Scheme,
			Host:               "127.0.0.1",
			MetricsBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred())

		fooSecretField, err := SetupFooToTypeField(ctx, k8sManager, &corev1.Secret{})
		Expect(err).NotTo(HaveOccurred())

		// Setup provider
		*providerRegistry = *provider.NewRegistry(scheme.Scheme)

		// register reconciler here
		namespaceReconciler := &core.NamespaceReconciler{
			Client:          k8sManager.GetClient(),
			APIReader:       k8sManager.GetAPIReader(),
			TargetClient:    k8sManager.GetClient(),
			TargetAPIReader: k8sManager.GetAPIReader(),
			Scheme:          k8sManager.GetScheme(),
			NamespacePrefix: "target-",
			ClusterName:     clusterName,
			Domain:          domain,
			ResyncPeriod:    1 * time.Second,
		}
		namespaceReconciler.Dependent(&testdatav1.Foo{})
		Expect(namespaceReconciler.SetupWithManager(k8sManager)).To(Succeed())
		Expect(providerRegistry.Register(&corev1.Namespace{}, namespaceReconciler)).To(Succeed())

		secretReconciler := &core.SecretReconciler{
			Provider:     providerRegistry,
			Client:       k8sManager.GetBrokerClient(),
			APIReader:    k8sManager.GetAPIReader(),
			TargetClient: k8sManager.GetBrokerClient(),
			Scheme:       k8sManager.GetScheme(),
			ClusterName:  clusterName,
			Domain:       domain,
		}
		Expect(secretReconciler.SetupWithManager(k8sManager)).To(Succeed())
		Expect(providerRegistry.Register(&corev1.Secret{}, secretReconciler)).To(Succeed())
		secretReconciler.Dependent(&testdatav1.Foo{}, fooSecretField)

		go func() {
			defer GinkgoRecover()
			Expect(k8sManager.Start(mgrCtx)).To(Succeed(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		cancel()
		Expect(k8sClient.Delete(ctx, ns)).To(Succeed(), "failed to delete test namespace")

		namespaceList := &corev1.NamespaceList{}
		Expect(k8sClient.List(ctx, namespaceList)).To(Succeed())
		for _, namespace := range namespaceList.Items {
			brokerCtrl := brokermeta.GetBrokerControllerOf(&namespace)
			if brokerCtrl == nil {
				continue
			}

			base := namespace.DeepCopy()
			namespace.Finalizers = nil
			Expect(client.IgnoreNotFound(k8sClient.Patch(ctx, &namespace, client.MergeFrom(base)))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &namespace))).To(Succeed())
		}
	})

	return ns, providerRegistry
}
