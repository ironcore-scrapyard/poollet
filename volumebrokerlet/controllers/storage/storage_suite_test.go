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
	"context"
	"testing"
	"time"

	"github.com/onmetal/controller-utils/buildutils"
	"github.com/onmetal/controller-utils/modutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/envtestutils"
	"github.com/onmetal/onmetal-api/envtestutils/apiserver"
	storagedependent "github.com/onmetal/poollet/api/storage/dependent"
	storageindex "github.com/onmetal/poollet/api/storage/index"
	"github.com/onmetal/poollet/broker"
	brokercluster "github.com/onmetal/poollet/broker/cluster"
	"github.com/onmetal/poollet/broker/controllers/core"
	"github.com/onmetal/poollet/broker/provider"
	volumebrokerletcontrollerscommon "github.com/onmetal/poollet/volumebrokerlet/controllers/common"
	"github.com/onmetal/poollet/volumebrokerlet/controllers/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	poolName    = "test-pool"
	clusterName = "test"
)

var (
	targetPoolSelector = map[string]string{
		"target-pool-key": "target-pool-value",
	}
)

var (
	cfg        *rest.Config
	testEnv    *envtest.Environment
	testEnvExt *envtestutils.EnvironmentExtensions
	k8sClient  client.Client
	domain     = volumebrokerletcontrollerscommon.Domain
)

const (
	slowSpecThreshold    = 10 * time.Second
	eventuallyTimeout    = 3 * time.Second
	pollingInterval      = 50 * time.Millisecond
	consistentlyDuration = 1 * time.Second
	apiServiceTimeout    = 5 * time.Minute
)

func TestStorage(t *testing.T) {
	_, reporterConfig := GinkgoConfiguration()
	reporterConfig.SlowSpecThreshold = slowSpecThreshold
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Storage Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	var err error
	By("bootstrapping test environment")
	testEnv = &envtest.Environment{}
	testEnvExt = &envtestutils.EnvironmentExtensions{
		APIServiceDirectoryPaths: []string{
			modutils.Dir("github.com/onmetal/onmetal-api", "config", "apiserver", "apiservice", "bases"),
		},
		ErrorIfAPIServicePathIsMissing: true,
	}

	cfg, err = envtestutils.StartWithExtensions(testEnv, testEnvExt)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	DeferCleanup(envtestutils.StopWithExtensions, testEnv, testEnvExt)

	Expect(storagev1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())

	// Init package-level k8sClient
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
	SetClient(k8sClient)

	apiSrv, err := apiserver.New(cfg, apiserver.Options{
		MainPath:     "github.com/onmetal/onmetal-api/cmd/apiserver",
		BuildOptions: []buildutils.BuildOption{buildutils.ModModeMod},
		ETCDServers:  []string{testEnv.ControlPlane.Etcd.URL.String()},
		Host:         testEnvExt.APIServiceInstallOptions.LocalServingHost,
		Port:         testEnvExt.APIServiceInstallOptions.LocalServingPort,
		CertDir:      testEnvExt.APIServiceInstallOptions.LocalServingCertDir,
	})
	Expect(err).NotTo(HaveOccurred())

	Expect(apiSrv.Start()).To(Succeed())
	DeferCleanup(apiSrv.Stop)

	Expect(envtestutils.WaitUntilAPIServicesReadyWithTimeout(apiServiceTimeout, testEnvExt, k8sClient, scheme.Scheme)).To(Succeed())
})

func SetupTest(ctx context.Context) (*corev1.Namespace, provider.Provider) {
	var (
		cancel context.CancelFunc
	)
	ns := &corev1.Namespace{}
	prov := &provider.Registry{}

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

		// Setup field indexers
		Expect(storageindex.AddToIndexer(mgrCtx, k8sManager.GetFieldIndexer())).To(Succeed())
		Expect(storageindex.AddToIndexer(mgrCtx, k8sManager.GetTarget().GetFieldIndexer())).To(Succeed())

		// Setup provider
		*prov = *provider.NewRegistry(scheme.Scheme)

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
		}
		Expect(storagedependent.SetupVolumeToNamespace(namespaceReconciler, poolName)).To(Succeed())
		Expect(namespaceReconciler.SetupWithManager(k8sManager)).To(Succeed())
		Expect(prov.Register(&corev1.Namespace{}, namespaceReconciler)).To(Succeed())

		secretReconciler := &core.SecretReconciler{
			Provider:     prov,
			Client:       k8sManager.GetBrokerClient(),
			APIReader:    k8sManager.GetAPIReader(),
			TargetClient: k8sManager.GetBrokerClient(),
			Scheme:       k8sManager.GetScheme(),
			ClusterName:  clusterName,
			Domain:       domain,
		}
		Expect(storagedependent.SetupVolumeToSecret(secretReconciler, poolName)).To(Succeed())
		Expect(secretReconciler.SetupWithManager(k8sManager)).To(Succeed())
		Expect(prov.Register(&corev1.Secret{}, secretReconciler))

		volumeReconciler := &storage.VolumeReconciler{
			Provider:         prov,
			Client:           k8sManager.GetClient(),
			TargetClient:     k8sManager.GetClient(),
			APIReader:        k8sManager.GetAPIReader(),
			Scheme:           k8sManager.GetScheme(),
			PoolName:         poolName,
			TargetPoolLabels: targetPoolSelector,
			TargetPoolName:   "",
			ClusterName:      clusterName,
		}
		Expect(volumeReconciler.SetupWithManager(k8sManager)).To(Succeed())
		Expect(prov.Register(&storagev1alpha1.Volume{}, volumeReconciler)).To(Succeed())

		go func() {
			defer GinkgoRecover()
			Expect(k8sManager.Start(mgrCtx)).To(Succeed(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		defer cancel()
		Expect(k8sClient.Delete(ctx, ns)).To(Succeed(), "failed to delete test namespace")
		Expect(k8sClient.DeleteAllOf(ctx, &storagev1alpha1.VolumePool{})).To(Succeed())
	})

	return ns, prov
}
