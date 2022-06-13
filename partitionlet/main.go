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
	"time"

	"github.com/onmetal/controller-utils/configutils"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	networkingv1alpha1 "github.com/onmetal/onmetal-api/apis/networking/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	computeindex "github.com/onmetal/poollet/api/compute/index"
	computefields "github.com/onmetal/poollet/api/compute/index/fields"
	computepredicate "github.com/onmetal/poollet/api/compute/predicate"
	networkingindex "github.com/onmetal/poollet/api/networking/index"
	storageindex "github.com/onmetal/poollet/api/storage/index"
	storagefields "github.com/onmetal/poollet/api/storage/index/fields"
	storagepredicate "github.com/onmetal/poollet/api/storage/predicate"
	"github.com/onmetal/poollet/broker"
	brokercluster "github.com/onmetal/poollet/broker/cluster"
	brokercompute "github.com/onmetal/poollet/broker/controllers/compute"
	"github.com/onmetal/poollet/broker/controllers/core"
	brokernetworking "github.com/onmetal/poollet/broker/controllers/networking"
	brokerstorage "github.com/onmetal/poollet/broker/controllers/storage"
	"github.com/onmetal/poollet/broker/provider"
	"github.com/onmetal/poollet/hash"
	partitionletcontrollerscommon "github.com/onmetal/poollet/partitionlet/controllers/common"
	"github.com/onmetal/poollet/partitionlet/controllers/storage"
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
	utilruntime.Must(computev1alpha1.AddToScheme(scheme))
	utilruntime.Must(networkingv1alpha1.AddToScheme(scheme))
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
	var initVolumePoolLabels map[string]string
	var initVolumePoolAnnotations map[string]string
	var initMachinePoolLabels map[string]string
	var initMachinePoolAnnotations map[string]string
	var targetVolumePoolName string
	var targetVolumePoolLabels map[string]string
	var fallbackVolumePoolName string
	var fallbackVolumePoolLabels map[string]string
	var targetMachinePoolName string
	var targetMachinePoolLabels map[string]string

	var namespaceResyncPeriod time.Duration
	var clusterName string

	flag.StringVar(&leaderElectionID, "leader-election-id", "", "Leader election id to use. If empty, defaulted to the domain + hash of pool names.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", "", "The namespace to do leader election in.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	flag.StringVar(&targetKubeconfig, "target-kubeconfig", "", "Path pointing to the target kubeconfig.")

	flag.StringVar(&poolName, "pool-name", hostName, "Name of the machine and volume pool to announce.")

	flag.StringVar(&providerID, "provider-id", "", "Provider id of the machine and volume pool to announce (usually <provider-type>://<id>).")

	flag.StringToStringVar(&initVolumePoolLabels, "init-volume-pool-labels", nil, "Labels to initialize the volume pool with.")
	flag.StringToStringVar(&initVolumePoolAnnotations, "init-volume-pool-annotations", nil, "Annotations to initialize the volume pool with.")

	flag.StringToStringVar(&initMachinePoolLabels, "init-machine-pool-labels", nil, "Labels to initialize the machine pool with.")
	flag.StringToStringVar(&initMachinePoolAnnotations, "init-machine-pool-annotations", nil, "Annotations to initialize the machine pool with.")

	flag.StringVar(&targetMachinePoolName, "target-machine-pool-name", "", "Name of the target pool to schedule machines on.")
	flag.StringToStringVar(&targetMachinePoolLabels, "target-machine-pool-labels", nil, "Labels to select the target pools to schedule machines on.")

	flag.StringVar(&targetVolumePoolName, "target-volume-pool-name", "", "Name of the target pool to schedule volumes on")
	flag.StringToStringVar(&targetVolumePoolLabels, "target-volume-pool-labels", nil, "Labels to select the target volume pool to schedule volumes on.")

	flag.StringVar(&fallbackVolumePoolName, "fallback-volume-pool-name", "", "Name of the fallback pool to schedule volumes on")
	flag.StringToStringVar(&fallbackVolumePoolLabels, "fallback-volume-pool-labels", nil, "Labels to select the fallback volume pool to schedule volumes on.")

	flag.DurationVar(&namespaceResyncPeriod, "namespace-resync-period", 10*time.Second, "Time to resync namespaces in.")
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
		leaderElectionID = partitionletcontrollerscommon.Domain.Subdomain(hash.FNV32A(poolName)).String()
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
	if err := computeindex.AddToIndexer(context.TODO(), mgr.GetFieldIndexer()); err != nil {
		logErrAndExit(err, "unable to index fields", "group", "compute")
	}
	if err := computeindex.AddToIndexer(context.TODO(), mgr.GetTarget().GetFieldIndexer()); err != nil {
		logErrAndExit(err, "unable to index fields", "group", "compute")
	}
	if err := networkingindex.AddToIndexer(context.TODO(), mgr.GetFieldIndexer()); err != nil {
		logErrAndExit(err, "unable to index fields", "group", "networking")
	}
	if err := networkingindex.AddToIndexer(context.TODO(), mgr.GetTarget().GetFieldIndexer()); err != nil {
		logErrAndExit(err, "unable to index fields", "group", "networking")
	}

	prov := provider.NewRegistry(scheme)

	namespaceReconciler := &core.NamespaceReconciler{
		Client:          mgr.GetClient(),
		APIReader:       mgr.GetAPIReader(),
		TargetClient:    mgr.GetTarget().GetClient(),
		TargetAPIReader: mgr.GetTarget().GetAPIReader(),
		Scheme:          scheme,
		NamespacePrefix: "partitionlet-",
		ClusterName:     clusterName,
		PoolName:        poolName,
		Domain:          partitionletcontrollerscommon.Domain,
		ResyncPeriod:    namespaceResyncPeriod,
	}
	namespaceReconciler.Dependent(&storagev1alpha1.Volume{}, storagepredicate.VolumeRunsInVolumePoolPredicate(poolName))
	namespaceReconciler.Dependent(&computev1alpha1.Machine{}, computepredicate.MachineRunsInMachinePoolPredicate(poolName))
	if err = namespaceReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "Namespace")
	}
	if err = prov.Register(&corev1.Namespace{}, namespaceReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "type", "Namespace")
	}

	secretReconciler := &core.SecretReconciler{
		Provider:     prov,
		Client:       mgr.GetBrokerClient(),
		APIReader:    mgr.GetAPIReader(),
		TargetClient: mgr.GetTarget().GetBrokerClient(),
		Scheme:       scheme,
		ClusterName:  clusterName,
		PoolName:     poolName,
		Domain:       partitionletcontrollerscommon.Domain,
	}
	secretReconciler.Dependent(&storagev1alpha1.Volume{}, storagefields.VolumeSpecSecretNamesField, storagepredicate.VolumeRunsInVolumePoolPredicate(poolName))
	secretReconciler.Dependent(&computev1alpha1.Machine{}, computefields.MachineSpecSecretNames, computepredicate.MachineRunsInMachinePoolPredicate(poolName))
	if err = secretReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "Secret")
	}
	if err = prov.Register(&corev1.Secret{}, secretReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "Secret")
	}

	configMapReconciler := &core.ConfigMapReconciler{
		Provider:     prov,
		Client:       mgr.GetBrokerClient(),
		APIReader:    mgr.GetAPIReader(),
		TargetClient: mgr.GetTarget().GetBrokerClient(),
		Scheme:       scheme,
		ClusterName:  clusterName,
		PoolName:     poolName,
		Domain:       partitionletcontrollerscommon.Domain,
	}
	configMapReconciler.Dependent(&computev1alpha1.Machine{}, computefields.MachineSpecConfigMapNames, computepredicate.MachineRunsInMachinePoolPredicate(poolName))
	if err = configMapReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "ConfigMap")
	}
	if err = prov.Register(&corev1.ConfigMap{}, configMapReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "ConfigMap")
	}

	volumeReconciler := &storage.MixedVolumeReconciler{
		Provider:           prov,
		Client:             mgr.GetClient(),
		APIReader:          mgr.GetAPIReader(),
		TargetClient:       mgr.GetTarget().GetClient(),
		PoolName:           poolName,
		TargetPoolName:     targetVolumePoolName,
		TargetPoolLabels:   targetVolumePoolLabels,
		FallbackPoolName:   fallbackVolumePoolName,
		FallbackPoolLabels: fallbackVolumePoolLabels,
		ClusterName:        clusterName,
	}
	if err = volumeReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "Volume")
	}
	if err = prov.Register(&storagev1alpha1.Volume{}, volumeReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "Volume")
	}

	if err = (&brokerstorage.VolumePoolReconciler{
		Client:              mgr.GetClient(),
		Target:              mgr.GetTarget().GetClient(),
		PoolName:            poolName,
		ProviderID:          providerID,
		InitPoolLabels:      initVolumePoolLabels,
		InitPoolAnnotations: initVolumePoolAnnotations,
		TargetPoolLabels:    targetVolumePoolLabels,
		TargetPoolName:      targetVolumePoolName,
		ClusterName:         clusterName,
		Domain:              partitionletcontrollerscommon.Domain,
	}).SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "VolumePool")
	}

	if err = (&brokercompute.MachinePoolReconciler{
		Client:              mgr.GetClient(),
		TargetClient:        mgr.GetTarget().GetClient(),
		PoolName:            poolName,
		ProviderID:          providerID,
		InitPoolLabels:      initMachinePoolLabels,
		InitPoolAnnotations: initMachinePoolAnnotations,
		TargetPoolLabels:    targetMachinePoolLabels,
		TargetPoolName:      targetMachinePoolName,
		ClusterName:         clusterName,
		Domain:              partitionletcontrollerscommon.Domain,
	}).SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "MachinePool")
	}

	networkReconciler := &brokernetworking.NetworkReconciler{
		Provider:        prov,
		Client:          mgr.GetClient(),
		APIReader:       mgr.GetAPIReader(),
		TargetClient:    mgr.GetTarget().GetClient(),
		ClusterName:     clusterName,
		MachinePoolName: poolName,
		Domain:          partitionletcontrollerscommon.Domain,
	}
	if err = networkReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "Network")
	}
	if err = prov.Register(&networkingv1alpha1.Network{}, networkReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "Network")
	}

	networkInterfaceReconciler := &brokernetworking.NetworkInterfaceReconciler{
		Provider:        prov,
		Client:          mgr.GetClient(),
		APIReader:       mgr.GetAPIReader(),
		TargetClient:    mgr.GetTarget().GetClient(),
		ClusterName:     clusterName,
		MachinePoolName: poolName,
		Domain:          partitionletcontrollerscommon.Domain,
	}
	if err = networkInterfaceReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "NetworkInterface")
	}
	if err = prov.Register(&networkingv1alpha1.NetworkInterface{}, networkInterfaceReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "NetworkInterface")
	}

	aliasPrefixReconciler := &brokernetworking.AliasPrefixReconciler{
		Provider:        prov,
		Client:          mgr.GetClient(),
		APIReader:       mgr.GetAPIReader(),
		TargetClient:    mgr.GetTarget().GetClient(),
		Scheme:          mgr.GetScheme(),
		ClusterName:     clusterName,
		MachinePoolName: poolName,
		Domain:          partitionletcontrollerscommon.Domain,
	}
	if err = aliasPrefixReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "AliasPrefix")
	}
	if err = prov.Register(&networkingv1alpha1.AliasPrefix{}, aliasPrefixReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "AliasPrefix")
	}

	virtualIPReconciler := &brokernetworking.VirtualIPReconciler{
		Provider:        prov,
		Client:          mgr.GetClient(),
		APIReader:       mgr.GetAPIReader(),
		TargetClient:    mgr.GetTarget().GetClient(),
		ClusterName:     clusterName,
		MachinePoolName: poolName,
		Domain:          partitionletcontrollerscommon.Domain,
	}
	if err = virtualIPReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "VirtualIP")
	}
	if err = prov.Register(&networkingv1alpha1.VirtualIP{}, virtualIPReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "VirtualIP")
	}

	machineReconciler := &brokercompute.MachineReconciler{
		Provider:         prov,
		Client:           mgr.GetClient(),
		APIReader:        mgr.GetAPIReader(),
		Scheme:           mgr.GetScheme(),
		TargetClient:     mgr.GetTarget().GetClient(),
		PoolName:         poolName,
		TargetPoolLabels: targetMachinePoolLabels,
		TargetPoolName:   targetMachinePoolName,
		ClusterName:      clusterName,
		Domain:           partitionletcontrollerscommon.Domain,
	}
	if err := machineReconciler.SetupWithManager(mgr); err != nil {
		logErrAndExit(err, "unable to set up controller", "controller", "Machine")
	}
	if err = prov.Register(&computev1alpha1.Machine{}, machineReconciler); err != nil {
		logErrAndExit(err, "unable to set up provider", "provider", "Machine")
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
