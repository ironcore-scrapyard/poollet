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

package manager

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	brokerclient "github.com/onmetal/poollet/broker/client"
	brokercluster "github.com/onmetal/poollet/broker/cluster"
	managerinject "github.com/onmetal/poollet/broker/manager/inject"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/config/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

type Manager interface {
	manager.Manager
	brokercluster.Cluster
	GetCluster() brokercluster.Cluster
	GetTarget() brokercluster.Cluster
	SetTargetFields(interface{}) error
}

type Options struct {
	// Scheme is the scheme used to resolve runtime.Objects to GroupVersionKinds / Resources.
	// Defaults to the kubernetes/client-go scheme.Scheme, but it's almost always better
	// to pass your own scheme in. See the documentation in pkg/scheme for more information.
	Scheme *runtime.Scheme

	// MapperProvider provides the rest mapper used to map go types to Kubernetes APIs
	MapperProvider func(c *rest.Config) (meta.RESTMapper, error)

	// SyncPeriod determines the minimum frequency at which watched resources are
	// reconciled. A lower period will correct entropy more quickly, but reduce
	// responsiveness to change if there are many watched resources. Change this
	// value only if you know what you are doing. Defaults to 10 hours if unset.
	// there will a 10 percent jitter between the SyncPeriod of all controllers
	// so that all controllers will not send list requests simultaneously.
	//
	// This applies to all controllers.
	//
	// A period sync happens for two reasons:
	// 1. To insure against a bug in the controller that causes an object to not
	// be requeued, when it otherwise should be requeued.
	// 2. To insure against an unknown bug in controller-runtime, or its dependencies,
	// that causes an object to not be requeued, when it otherwise should be
	// requeued, or to be removed from the queue, when it otherwise should not
	// be removed.
	//
	// If you want
	// 1. to insure against missed watch events, or
	// 2. to poll services that cannot be watched,
	// then we recommend that, instead of changing the default period, the
	// controller requeue, with a constant duration `t`, whenever the controller
	// is "done" with an object, and would otherwise not requeue it, i.e., we
	// recommend the `Reconcile` function return `reconcile.Result{RequeueAfter: t}`,
	// instead of `reconcile.Result{}`.
	SyncPeriod *time.Duration

	// Logger is the logger that should be used by this manager.
	// If none is set, it defaults to log.Log global logger.
	Logger logr.Logger

	// LeaderElection determines whether or not to use leader election when
	// starting the manager.
	LeaderElection bool

	// LeaderElectionResourceLock determines which resource lock to use for leader election,
	// defaults to "configmapsleases". Change this value only if you know what you are doing.
	// Otherwise, users of your controller might end up with multiple running instances that
	// each acquired leadership through different resource locks during upgrades and thus
	// act on the same resources concurrently.
	// If you want to migrate to the "leases" resource lock, you might do so by migrating to the
	// respective multilock first ("configmapsleases" or "endpointsleases"), which will acquire a
	// leader lock on both resources. After all your users have migrated to the multilock, you can
	// go ahead and migrate to "leases". Please also keep in mind, that users might skip versions
	// of your controller.
	//
	// Note: before controller-runtime version v0.7, the resource lock was set to "configmaps".
	// Please keep this in mind, when planning a proper migration path for your controller.
	LeaderElectionResourceLock string

	// LeaderElectionNamespace determines the namespace in which the leader
	// election resource will be created.
	LeaderElectionNamespace string

	// LeaderElectionID determines the name of the resource that leader election
	// will use for holding the leader lock.
	LeaderElectionID string

	// LeaderElectionConfig can be specified to override the default configuration
	// that is used to build the leader election client.
	LeaderElectionConfig *rest.Config

	// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
	// when the Manager ends. This requires the binary to immediately end when the
	// Manager is stopped, otherwise this setting is unsafe. Setting this significantly
	// speeds up voluntary leader transitions as the new leader doesn't have to wait
	// LeaseDuration time first.
	LeaderElectionReleaseOnCancel bool

	// LeaseDuration is the duration that non-leader candidates will
	// wait to force acquire leadership. This is measured against time of
	// last observed ack. Default is 15 seconds.
	LeaseDuration *time.Duration
	// RenewDeadline is the duration that the acting controlplane will retry
	// refreshing leadership before giving up. Default is 10 seconds.
	RenewDeadline *time.Duration
	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions. Default is 2 seconds.
	RetryPeriod *time.Duration

	// Namespace, if specified, restricts the manager's cache to watch objects in
	// the desired namespace. Defaults to all namespaces.
	//
	// Note: If a namespace is specified, controllers can still Watch for a
	// cluster-scoped resource (e.g Node). For namespaced resources, the cache
	// will only hold objects from the desired namespace.
	Namespace string

	// MetricsBindAddress is the TCP address that the controller should bind to
	// for serving prometheus metrics.
	// It can be set to "0" to disable the metrics serving.
	MetricsBindAddress string

	// HealthProbeBindAddress is the TCP address that the controller should bind to
	// for serving health probes
	HealthProbeBindAddress string

	// Readiness probe endpoint name, defaults to "readyz"
	ReadinessEndpointName string

	// Liveness probe endpoint name, defaults to "healthz"
	LivenessEndpointName string

	// Port is the port that the webhook server serves at.
	// It is used to set webhook.Server.Port if WebhookServer is not set.
	Port int
	// Host is the hostname that the webhook server binds to.
	// It is used to set webhook.Server.Host if WebhookServer is not set.
	Host string

	// CertDir is the directory that contains the server key and certificate.
	// If not set, webhook server would look up the server key and certificate in
	// {TempDir}/k8s-webhook-server/serving-certs. The server key and certificate
	// must be named tls.key and tls.crt, respectively.
	// It is used to set webhook.Server.CertDir if WebhookServer is not set.
	CertDir string

	// WebhookServer is an externally configured webhook.Server. By default,
	// a Manager will create a default server using Port, Host, and CertDir;
	// if this is set, the Manager will use this server instead.
	WebhookServer *webhook.Server

	// Functions to allow for a user to customize values that will be injected.

	// NewCache is the function that will create the cache to be used
	// by the manager. If not set this will use the default new cache function.
	NewCache cache.NewCacheFunc

	// NewClient is the func that creates the client to be used by the manager.
	// If not set this will create the default DelegatingClient that will
	// use the cache for reads and the client for writes.
	NewClient cluster.NewClientFunc

	// BaseContext is the function that provides Context values to Runnables
	// managed by the Manager. If a BaseContext function isn't provided, Runnables
	// will receive a new Background Context instead.
	BaseContext manager.BaseContextFunc

	// ClientDisableCacheFor tells the client that, if any cache is used, to bypass it
	// for the given objects.
	ClientDisableCacheFor []client.Object

	// DryRunClient specifies whether the client should be configured to enforce
	// dryRun mode.
	DryRunClient bool

	// EventBroadcaster records Events emitted by the manager and sends them to the Kubernetes API
	// Use this to customize the event correlator and spam filter
	//
	// Deprecated: using this may cause goroutine leaks if the lifetime of your manager or controllers
	// is shorter than the lifetime of your process.
	EventBroadcaster record.EventBroadcaster

	// GracefulShutdownTimeout is the duration given to runnable to stop before the manager actually returns on stop.
	// To disable graceful shutdown, set to time.Duration(0)
	// To use graceful shutdown without timeout, set to a negative duration, e.G. time.Duration(-1)
	// The graceful shutdown is skipped for safety reasons in case the leader election lease is lost.
	GracefulShutdownTimeout *time.Duration

	// Controller contains global configuration options for controllers
	// registered within this manager.
	// +optional
	Controller v1alpha1.ControllerConfigurationSpec
}

func (o *Options) options() manager.Options {
	return manager.Options{
		Scheme:                        o.Scheme,
		MapperProvider:                o.MapperProvider,
		SyncPeriod:                    o.SyncPeriod,
		Logger:                        o.Logger,
		LeaderElection:                o.LeaderElection,
		LeaderElectionResourceLock:    o.LeaderElectionResourceLock,
		LeaderElectionNamespace:       o.LeaderElectionNamespace,
		LeaderElectionID:              o.LeaderElectionID,
		LeaderElectionConfig:          o.LeaderElectionConfig,
		LeaderElectionReleaseOnCancel: o.LeaderElectionReleaseOnCancel,
		LeaseDuration:                 o.LeaseDuration,
		RenewDeadline:                 o.RenewDeadline,
		RetryPeriod:                   o.RetryPeriod,
		Namespace:                     o.Namespace,
		MetricsBindAddress:            o.MetricsBindAddress,
		HealthProbeBindAddress:        o.HealthProbeBindAddress,
		ReadinessEndpointName:         o.ReadinessEndpointName,
		LivenessEndpointName:          o.LivenessEndpointName,
		Port:                          o.Port,
		Host:                          o.Host,
		CertDir:                       o.CertDir,
		WebhookServer:                 o.WebhookServer,
		NewCache:                      o.NewCache,
		NewClient:                     o.NewClient,
		BaseContext:                   o.BaseContext,
		ClientDisableCacheFor:         o.ClientDisableCacheFor,
		DryRunClient:                  o.DryRunClient,
		EventBroadcaster:              o.EventBroadcaster,
		GracefulShutdownTimeout:       o.GracefulShutdownTimeout,
		Controller:                    o.Controller,
	}
}

type brokerManager struct {
	manager manager.Manager
	target  brokercluster.Cluster
	cluster brokercluster.Cluster

	stop <-chan struct{}
}

func (b *brokerManager) GetConfig() *rest.Config {
	return b.cluster.GetConfig()
}

func (b *brokerManager) GetScheme() *runtime.Scheme {
	return b.cluster.GetScheme()
}

func (b *brokerManager) GetClient() client.Client {
	return b.cluster.GetClient()
}

func (b *brokerManager) GetBrokerClient() brokerclient.Client {
	return b.cluster.GetBrokerClient()
}

func (b *brokerManager) GetCache() cache.Cache {
	return b.cluster.GetCache()
}

func (b *brokerManager) GetFieldIndexer() client.FieldIndexer {
	return b.cluster.GetFieldIndexer()
}

func (b *brokerManager) GetEventRecorderFor(name string) record.EventRecorder {
	return b.cluster.GetEventRecorderFor(name)
}

func (b *brokerManager) GetRESTMapper() meta.RESTMapper {
	return b.cluster.GetRESTMapper()
}

func (b *brokerManager) GetAPIReader() client.Reader {
	return b.cluster.GetAPIReader()
}

func (b *brokerManager) Start(ctx context.Context) error {
	return b.manager.Start(ctx)
}

func (b *brokerManager) Add(runnable manager.Runnable) error {
	return b.manager.Add(runnable)
}

func (b *brokerManager) Elected() <-chan struct{} {
	return b.manager.Elected()
}

func (b *brokerManager) AddMetricsExtraHandler(path string, handler http.Handler) error {
	return b.manager.AddMetricsExtraHandler(path, handler)
}

func (b *brokerManager) AddHealthzCheck(name string, check healthz.Checker) error {
	return b.manager.AddHealthzCheck(name, check)
}

func (b *brokerManager) AddReadyzCheck(name string, check healthz.Checker) error {
	return b.manager.AddReadyzCheck(name, check)
}

func (b *brokerManager) GetWebhookServer() *webhook.Server {
	return b.manager.GetWebhookServer()
}

func (b *brokerManager) GetLogger() logr.Logger {
	return b.manager.GetLogger()
}

func (b *brokerManager) GetControllerOptions() v1alpha1.ControllerConfigurationSpec {
	return b.manager.GetControllerOptions()
}

func (b *brokerManager) GetCluster() brokercluster.Cluster {
	return b.cluster
}

func (b *brokerManager) GetTarget() brokercluster.Cluster {
	return b.target
}

func (b *brokerManager) setManagerFields(i interface{}) error {
	if _, err := inject.InjectorInto(b.SetFields, i); err != nil {
		return err
	}
	if _, err := inject.StopChannelInto(b.stop, i); err != nil {
		return err
	}
	if _, err := inject.LoggerInto(b.GetLogger(), i); err != nil {
		return err
	}
	if _, err := managerinject.ClusterInto(b.GetCluster(), i); err != nil {
		return err
	}
	if _, err := managerinject.TargetInto(b.GetTarget(), i); err != nil {
		return err
	}
	return nil
}

func (b *brokerManager) SetFields(i interface{}) error {
	if err := b.cluster.SetFields(i); err != nil {
		return err
	}
	if err := b.setManagerFields(i); err != nil {
		return err
	}
	return nil
}

func (b *brokerManager) SetTargetFields(i interface{}) error {
	if err := b.target.SetFields(i); err != nil {
		return err
	}
	if err := b.setManagerFields(i); err != nil {
		return err
	}
	return nil
}

type stopChanInject <-chan struct{}

func (s *stopChanInject) InjectStopChannel(c <-chan struct{}) error {
	*s = c
	return nil
}

func getStopChan(mgr manager.Manager) (<-chan struct{}, error) {
	var s stopChanInject
	if err := mgr.SetFields(&s); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("did not inject stop channel")
	}
	return s, nil
}

func New(config *rest.Config, targetCluster brokercluster.Cluster, opts Options) (Manager, error) {
	if targetCluster == nil {
		return nil, fmt.Errorf("must specify target cluster")
	}

	mgr, err := manager.New(config, opts.options())
	if err != nil {
		return nil, err
	}

	stop, err := getStopChan(mgr)
	if err != nil {
		return nil, err
	}

	self := brokercluster.WrapCluster(mgr, brokerclient.NewFieldExtractorRegistry(mgr.GetScheme()))

	if err := mgr.Add(targetCluster); err != nil {
		return nil, err
	}

	return &brokerManager{
		manager: mgr,
		cluster: self,
		target:  targetCluster,
		stop:    stop,
	}, nil
}
