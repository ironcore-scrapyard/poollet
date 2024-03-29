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

package main

import (
	"context"
	goflag "flag"
	"fmt"
	"os"
	"strings"

	"github.com/onmetal/controller-utils/configutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	storagedependent "github.com/onmetal/poollet/api/storage/dependent"
	storageindex "github.com/onmetal/poollet/api/storage/index"
	"github.com/onmetal/poollet/broker"
	brokercluster "github.com/onmetal/poollet/broker/cluster"
	"github.com/onmetal/poollet/broker/controllers/core"
	brokerstorage "github.com/onmetal/poollet/broker/controllers/storage"
	"github.com/onmetal/poollet/broker/provider"
	"github.com/onmetal/poollet/hash"
	proxyvolumebrokerletcontrollerscommon "github.com/onmetal/poollet/proxyvolumebrokerlet/controllers/common"
	"github.com/onmetal/poollet/proxyvolumebrokerlet/controllers/storage"
	flag "github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	hostName string
)

func init() {
	hostName, _ = os.Hostname()
	hostName = strings.ToLower(hostName)

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(storagev1alpha1.AddToScheme(scheme))
}

func logErrAndExit(err error, msg string, keysAndValues ...interface{}) {
	setupLog.Error(err, msg, keysAndValues...)
	os.Exit(1)
}

func main() {
	var leaderElectionID string
	var leaderElectionNamespace string
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	var targetKubeconfig string

	var poolName string
	var providerID string
	var initPoolLabels map[string]string
	var initPoolAnnotations map[string]string
	var targetPoolName string
	var targetPoolLabels map[string]string

	var clusterName string

	flag.StringVar(&leaderElectionID, "leader-election-id", "", "Leader election id to use. If empty, defaulted to the domain + hash of the pool name.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", "", "The namespace to do leader election in.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	flag.StringVar(&targetKubeconfig, "target-kubeconfig", "", "Path pointing to the target kubeconfig.")

	flag.StringVar(&poolName, "pool-name", hostName, "Name of the volume pool to announce.")
	flag.StringVar(&providerID, "provider-id", "", "Provider id of the volume pool to announce (usually <provider-type>://<id>).")
	flag.StringToStringVar(&initPoolLabels, "init-pool-labels", nil, "Labels to initialize the volume pool with.")
	flag.StringToStringVar(&initPoolAnnotations, "init-pool-annotations", nil, "Annotations to initialize the volume pool with.")
	flag.StringVar(&targetPoolName, "target-pool-name", "", "Name of the target pool to schedule volumes on.")
	flag.StringToStringVar(&targetPoolLabels, "target-pool-labels", nil, "Labels to select the target pools to schedule volumes on.")

	flag.StringVar(&clusterName, "cluster-name", "", "Name of the source cluster. Used for cross-cluster owner references / finalizers.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(goflag.CommandLine)
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if clusterName == "" {
		logErrAndExit(fmt.Errorf("empty cluster name"), "cluster-name needs to be specified")
	}

	cfg, err := configutils.GetConfig()
	if err != nil {
		logErrAndExit(err, "unable to load kubeconfig")
	}

	targetCfg, err := configutils.GetConfig(configutils.Kubeconfig(targetKubeconfig))
	if err != nil {
		logErrAndExit(err, "unable to load target kubeconfig")
	}

	targetCluster, err := brokercluster.New(targetCfg,
		func(o *cluster.Options) {
			o.Scheme = scheme
		},
	)
	if err != nil {
		logErrAndExit(err, "could not create target cluster")
	}

	if leaderElectionID == "" {
		leaderElectionID = proxyvolumebrokerletcontrollerscommon.Domain.Subdomain(hash.FNV32A(poolName)).String()
	}

	mgr, err := broker.NewManager(cfg, targetCluster, broker.Options{
		Scheme:                  scheme,
		MetricsBindAddress:      metricsAddr,
		Port:                    9443,
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        leaderElectionID,
		LeaderElectionNamespace: leaderElectionNamespace,
	})
	if err != nil {
		logErrAndExit(err, "unable to start manager")
	}

	if err := storageindex.AddToIndexer(context.TODO(), mgr.GetFieldIndexer()); err != nil {
		logErrAndExit(err, "unable to index fields", "group", "storage")
	}
	if err := storageindex.AddToIndexer(context.TODO(), mgr.GetTarget().GetFieldIndexer()); err != nil {
		logErrAndExit(err, "unable to index fields", "group", "storage")
	}

	// Set up provider
	prov := provider.NewRegistry(scheme)

	namespaceReconciler := &core.NamespaceReconciler{
		Client:          mgr.GetClient(),
		APIReader:       mgr.GetAPIReader(),
		TargetClient:    mgr.GetTarget().GetClient(),
		TargetAPIReader: mgr.GetTarget().GetAPIReader(),
		Scheme:          scheme,
		NamespacePrefix: "proxyvolumebrokerlet-",
		ClusterName:     clusterName,
		PoolName:        poolName,
		Domain:          proxyvolumebrokerletcontrollerscommon.Domain,
	}
	if err := storagedependent.SetupVolumeToNamespace(namespaceReconciler, poolName); err != nil {
		logErrAndExit(err, "unable to set up dependent", "controller", "Namespace", "dependent", "Volume")
	}
	if err = namespaceReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "Namespace")
	}
	if err = prov.Register(&corev1.Namespace{}, namespaceReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "Namespace")
	}

	secretReconciler := &core.SecretReconciler{
		Provider:     prov,
		Client:       mgr.GetBrokerClient(),
		APIReader:    mgr.GetAPIReader(),
		TargetClient: mgr.GetTarget().GetBrokerClient(),
		Scheme:       scheme,
		ClusterName:  clusterName,
		PoolName:     poolName,
		Domain:       proxyvolumebrokerletcontrollerscommon.Domain,
	}
	if err := storagedependent.SetupVolumeToSecret(secretReconciler, poolName); err != nil {
		logErrAndExit(err, "unable to set up dependent", "controller", "Secret", "dependent", "Volume")
	}
	if err = secretReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "Secret")
	}
	if err = prov.Register(&corev1.Secret{}, secretReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "Secret")
	}

	if err = (&brokerstorage.VolumePoolReconciler{
		Client:              mgr.GetClient(),
		Target:              mgr.GetTarget().GetClient(),
		ProviderID:          providerID,
		InitPoolLabels:      initPoolLabels,
		InitPoolAnnotations: initPoolAnnotations,
		TargetPoolLabels:    targetPoolLabels,
		TargetPoolName:      targetPoolName,
		ClusterName:         clusterName,
		PoolName:            poolName,
		Domain:              proxyvolumebrokerletcontrollerscommon.Domain,
	}).SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "VolumePool")
	}

	proxyVolumeReconciler := &storage.ProxyVolumeReconciler{
		Provider:         prov,
		Client:           mgr.GetClient(),
		APIReader:        mgr.GetAPIReader(),
		TargetClient:     mgr.GetTarget().GetClient(),
		PoolName:         poolName,
		TargetPoolName:   targetPoolName,
		TargetPoolLabels: targetPoolLabels,
		ClusterName:      clusterName,
	}
	if err = proxyVolumeReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "Volume")
	}
	if err = prov.Register(&storagev1alpha1.Volume{}, proxyVolumeReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "Volume")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logErrAndExit(err, "unable to set up health check")
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logErrAndExit(err, "unable to set up ready check")
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logErrAndExit(err, "problem running manager")
	}
}
