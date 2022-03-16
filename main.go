/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	goflag "flag"
	"fmt"
	"os"
	"strings"

	"github.com/onmetal/controller-utils/configutils"
	"github.com/onmetal/partitionlet/controllers/shared"
	"github.com/onmetal/partitionlet/strategy"
	flag "github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiserver/pkg/server/egressselector"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/onmetal/controller-utils/cmdutils/switches"
	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"
	networkv1alpha1 "github.com/onmetal/onmetal-api/apis/network/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/partitionlet/controllers/compute"
	"github.com/onmetal/partitionlet/controllers/storage"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	hostName string
)

const (
	machineController     = "machine"
	volumeController      = "volume"
	machinePoolController = "machinepool"
	storagePoolController = "storagepool"
)

func init() {
	hostName, _ = os.Hostname()
	hostName = strings.ToLower(hostName)

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(computev1alpha1.AddToScheme(scheme))
	utilruntime.Must(networkv1alpha1.AddToScheme(scheme))
	utilruntime.Must(storagev1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var leaderElectionID string
	var leaderElectionNamespace string
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	var egressSelectorFile string

	var parentKubeconfig string

	var namespace string
	var broker bool

	var machinePoolName string
	var machinePoolProviderID string
	var machinePoolLabels map[string]string
	var machinePoolAnnotations map[string]string
	var sourceMachinePoolSelector map[string]string
	var sourceMachinePoolName string

	var storagePoolName string
	var storagePoolProviderID string
	var storagePoolLabels map[string]string
	var storagePoolAnnotations map[string]string
	var sourceStoragePoolName string
	var sourceStoragePoolSelector map[string]string

	flag.StringVar(&leaderElectionID, "leader-election-id", "ba861938.onmetal.de", "Leader election id to use.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", "", "The namespace to do leader election in.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	flag.StringVar(&egressSelectorFile, "egress-selector", "", "Value supplying egress selector configuration.")

	flag.StringVar(&parentKubeconfig, "parent-kubeconfig", "", "Path pointing to a parent kubeconfig.")

	flag.StringVar(&namespace, "namespace", corev1.NamespaceDefault, "Namespace to sync machines to.")
	flag.BoolVar(&broker, "broker", false, "Whether to start the partitionlet in broker-mode or not.")

	flag.StringVar(&machinePoolName, "machine-pool-name", hostName, "MachinePool to announce in the parent cluster.")
	flag.StringVar(&machinePoolProviderID, "machine-pool-provider-id", "", "Provider ID (usually <provider-type>://<id>) of the announced MachinePool.")
	flag.StringToStringVar(&machinePoolLabels, "machine-pool-labels", nil, "Labels to apply to the machine pool upon startup.")
	flag.StringToStringVar(&machinePoolAnnotations, "machine-pool-annotations", nil, "Annotations to apply to the machine pool upon startup.")
	flag.StringToStringVar(&sourceMachinePoolSelector, "source-machine-pool-selector", nil, "Selector of source machine pools.")
	flag.StringVar(&sourceMachinePoolName, "source-machine-pool-name", "", "Name of the source machine pool.")

	flag.StringVar(&storagePoolName, "storage-pool-name", hostName, "StoragePool to announce in the parent cluster.")
	flag.StringVar(&storagePoolProviderID, "storage-pool-provider-id", "", "Provider ID (usually <provider-type>://<id>) of the announced StoragePool.")
	flag.StringVar(&sourceStoragePoolName, "source-storage-pool-name", "", "Name of the source storage pool.")
	flag.StringToStringVar(&sourceStoragePoolSelector, "source-storage-pool-selector", nil, "Selector of source storage pools.")
	flag.StringToStringVar(&storagePoolLabels, "storage-pool-labels", nil, "Labels to apply to the storage pool upon startup.")
	flag.StringToStringVar(&storagePoolAnnotations, "storage-pool-annotations", nil, "Annotations to apply to the storage pool upon startup.")

	controllers := switches.New(
		machineController, machinePoolController, volumeController, switches.Disable(storagePoolController),
	)
	flag.Var(controllers, "controllers", fmt.Sprintf("Controllers to enable. All controllers: %v. Disabled-by-default controllers: %v", controllers.All(), controllers.DisabledByDefault()))

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(goflag.CommandLine)
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	var egressSelector *egressselector.EgressSelector
	if egressSelectorFile != "" {
		egressSelectorCfg, err := egressselector.ReadEgressSelectorConfiguration(egressSelectorFile)
		if err != nil {
			setupLog.Error(err, "Could not read egress selector configuration")
			os.Exit(1)
		}

		egressSelector, err = egressselector.NewEgressSelector(egressSelectorCfg)
		if err != nil {
			setupLog.Error(err, "Could not create egress selector")
			os.Exit(1)
		}
	}

	if (controllers.Enabled(machinePoolController) || controllers.Enabled(machineController)) && machinePoolName == "" {
		err := fmt.Errorf("machine pool name needs to be set")
		setupLog.Error(err, "Machine pool name is not defined")
		os.Exit(1)
	}
	if controllers.Enabled(storagePoolController) && storagePoolName == "" {
		err := fmt.Errorf("storage pool name needs to be set")
		setupLog.Error(err, "Storage pool name is not defined")
		os.Exit(1)
	}

	parentCfg, err := configutils.GetConfig(configutils.Kubeconfig(parentKubeconfig))
	if err != nil {
		setupLog.Error(err, "unable to load parent kubeconfig")
		os.Exit(1)
	}

	if egressSelector != nil {
		dialFunc, err := egressSelector.Lookup(egressselector.NetworkContext{
			EgressSelectionName: egressselector.Cluster,
		})
		if err != nil {
			setupLog.Error(err, "Error getting egress selector for parent")
			os.Exit(1)
		}
		if dialFunc != nil {
			parentCfg.Dial = dialFunc
		}
	}

	strat := strategy.Strategy(&strategy.Simple{
		Namespace: namespace,
	})
	if broker {
		strat = strategy.Broker{Fallback: strat}
	}

	cfg, err := configutils.GetConfig()
	if err != nil {
		setupLog.Error(err, "unable to load kubeconfig")
		os.Exit(1)
	}

	if egressSelector != nil {
		dialFunc, err := egressSelector.Lookup(egressselector.NetworkContext{
			EgressSelectionName: egressselector.ControlPlane,
		})
		if err != nil {
			setupLog.Error(err, "Error getting egress selector for cluster")
			os.Exit(1)
		}
		if dialFunc != nil {
			cfg.Dial = dialFunc
		}
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  scheme,
		MetricsBindAddress:      metricsAddr,
		Port:                    9443,
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        leaderElectionID,
		LeaderElectionNamespace: leaderElectionNamespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	parentCluster, err := cluster.New(parentCfg, func(o *cluster.Options) {
		o.Scheme = scheme
	})
	if err != nil {
		setupLog.Error(err, "could not create target cluster")
		os.Exit(1)
	}

	if err := mgr.Add(parentCluster); err != nil {
		setupLog.Error(err, "could not add target cluster to manager")
		os.Exit(1)
	}

	sharedParentFieldIndexer := shared.NewParentFieldIndexer(machinePoolName, parentCluster.GetFieldIndexer(), mgr.GetScheme())

	if controllers.Enabled(machinePoolController) {
		if machinePoolProviderID == "" {
			err := fmt.Errorf("machine pool provider id needs to be set")
			setupLog.Error(err, "Machine pool provider id is not defined")
			os.Exit(1)
		}
		if err := (&compute.MachinePoolReconciler{
			Client:                    mgr.GetClient(),
			ParentClient:              parentCluster.GetClient(),
			ParentCache:               parentCluster.GetCache(),
			MachinePoolName:           machinePoolName,
			ProviderID:                machinePoolProviderID,
			MachinePoolLabels:         machinePoolLabels,
			MachinePoolAnnotations:    machinePoolAnnotations,
			SourceMachinePoolSelector: sourceMachinePoolSelector,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MachinePool")
			os.Exit(1)
		}
	}
	if controllers.Enabled(machineController) {
		if err = (&compute.MachineReconciler{
			Client:                    mgr.GetClient(),
			Scheme:                    mgr.GetScheme(),
			ParentClient:              parentCluster.GetClient(),
			ParentCache:               parentCluster.GetCache(),
			ParentFieldIndexer:        parentCluster.GetFieldIndexer(),
			SharedParentFieldIndexer:  sharedParentFieldIndexer,
			Strategy:                  strat,
			MachinePoolName:           machinePoolName,
			SourceMachinePoolName:     sourceMachinePoolName,
			SourceMachinePoolSelector: sourceMachinePoolSelector,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Machine")
			os.Exit(1)
		}
	}
	if controllers.Enabled(volumeController) {
		if err := (&storage.VolumeReconciler{
			Client:                    mgr.GetClient(),
			FieldIndexer:              mgr.GetFieldIndexer(),
			Scheme:                    mgr.GetScheme(),
			ParentClient:              parentCluster.GetClient(),
			ParentCache:               parentCluster.GetCache(),
			ParentFieldIndexer:        parentCluster.GetFieldIndexer(),
			SharedParentFieldIndexer:  sharedParentFieldIndexer,
			Strategy:                  strat,
			StoragePoolName:           storagePoolName,
			MachinePoolName:           machinePoolName,
			SourceStoragePoolName:     sourceStoragePoolName,
			SourceStoragePoolSelector: sourceStoragePoolSelector,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Volume")
		}
	}
	if controllers.Enabled(storagePoolController) {
		if storagePoolProviderID == "" {
			err := fmt.Errorf("storage pool provider id needs to be set")
			setupLog.Error(err, "Storage pool provider id is not defined")
			os.Exit(1)
		}
		if err := (&storage.StoragePoolReconciler{
			Client:                    mgr.GetClient(),
			ParentClient:              parentCluster.GetClient(),
			ParentCache:               parentCluster.GetCache(),
			StoragePoolName:           storagePoolName,
			ProviderID:                storagePoolProviderID,
			StoragePoolLabels:         storagePoolLabels,
			StoragePoolAnnotations:    storagePoolAnnotations,
			SourceStoragePoolSelector: sourceStoragePoolSelector,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "StoragePool")
			os.Exit(1)
		}
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
