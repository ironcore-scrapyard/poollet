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

	flag "github.com/spf13/pflag"

	computev1alpha1 "github.com/onmetal/onmetal-api/apis/compute/v1alpha1"

	"github.com/onmetal/partitionlet/controllers/compute"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
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

	utilruntime.Must(computev1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func LoadRESTConfig(kubeconfig string) (*rest.Config, error) {
	data, err := os.ReadFile(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("could not read kubeconfig %s: %w", kubeconfig, err)
	}

	return clientcmd.RESTConfigFromKubeConfig(data)
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var parentKubeconfig string
	var namespace string
	var machinePoolName string
	var providerID string
	var sourceMachinePoolSelector map[string]string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&parentKubeconfig, "parent-kubeconfig", "", "Path pointing to a parent kubeconfig.")
	flag.StringVar(&namespace, "namespace", corev1.NamespaceDefault, "Namespace to sync machines to.")
	flag.StringVar(&machinePoolName, "machine-pool-name", hostName, "MachinePool to announce in the parent cluster.")
	flag.StringVar(&providerID, "provider-id", "", "Provider ID (usually <provider-type>://<id>) of the announced MachinePool.")
	flag.StringToStringVar(&sourceMachinePoolSelector, "source-machine-pool-selector", nil, "Selector of source machine pools")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(goflag.CommandLine)
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if machinePoolName == "" {
		err := fmt.Errorf("machine pool name needs to be set")
		setupLog.Error(err, "Machine pool name is not defined")
		os.Exit(1)
	}
	if providerID == "" {
		err := fmt.Errorf("provider id needs to be set")
		setupLog.Error(err, "Provider id is not defined")
		os.Exit(1)
	}

	parentCfg, err := LoadRESTConfig(parentKubeconfig)
	if err != nil {
		setupLog.Error(err, "unable to load target kubeconfig")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "ba861938.onmetal.de",
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

	if err := (&compute.MachinePoolReconciler{
		Client:                    mgr.GetClient(),
		ParentClient:              parentCluster.GetClient(),
		ParentCache:               parentCluster.GetCache(),
		MachinePoolName:           machinePoolName,
		ProviderID:                providerID,
		SourceMachinePoolSelector: sourceMachinePoolSelector,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MachinePool")
		os.Exit(1)
	}
	if err = (&compute.MachineReconciler{
		Client:                    mgr.GetClient(),
		ParentClient:              parentCluster.GetClient(),
		ParentCache:               parentCluster.GetCache(),
		ParentFieldIndexer:        parentCluster.GetFieldIndexer(),
		Namespace:                 namespace,
		MachinePoolName:           machinePoolName,
		SourceMachinePoolSelector: sourceMachinePoolSelector,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Machine")
		os.Exit(1)
	}
	if err = (&compute.MachineStatusReconciler{
		Client:          mgr.GetClient(),
		ParentClient:    parentCluster.GetClient(),
		ParentCache:     parentCluster.GetCache(),
		MachinePoolName: machinePoolName,
		Namespace:       namespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MachineStatus")
		os.Exit(1)
	}
	if err = (&compute.ConsoleReconciler{
		Scheme:             mgr.GetScheme(),
		Client:             mgr.GetClient(),
		ParentClient:       parentCluster.GetClient(),
		ParentCache:        parentCluster.GetCache(),
		ParentFieldIndexer: parentCluster.GetFieldIndexer(),
		Namespace:          namespace,
		MachinePoolName:    machinePoolName,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Console")
		os.Exit(1)
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
