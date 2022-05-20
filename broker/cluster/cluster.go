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

package cluster

import (
	brokerclient "github.com/onmetal/poollet/broker/client"
	clusterinject "github.com/onmetal/poollet/broker/cluster/inject"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
)

type Cluster interface {
	cluster.Cluster
	GetBrokerClient() brokerclient.Client
}

func New(cfg *rest.Config, opts ...cluster.Option) (Cluster, error) {
	c, err := cluster.New(cfg, opts...)
	if err != nil {
		return nil, err
	}

	return WrapCluster(c, brokerclient.NewFieldExtractorRegistry(c.GetScheme())), nil
}

func WrapCluster(cluster cluster.Cluster, registry *brokerclient.FieldExtractorRegistry) Cluster {
	return &brokerCluster{
		Cluster:  cluster,
		Registry: registry,
	}
}

type brokerCluster struct {
	cluster.Cluster
	Registry *brokerclient.FieldExtractorRegistry
}

func (c *brokerCluster) GetFieldIndexer() client.FieldIndexer {
	return brokerclient.FieldIndexerRegistryOverlay{
		FieldIndexer: c.Cluster.GetFieldIndexer(),
		Registry:     c.Registry,
	}
}

func (c *brokerCluster) GetClient() client.Client {
	return c.GetBrokerClient()
}

func (c *brokerCluster) GetBrokerClient() brokerclient.Client {
	return brokerclient.FieldExtractorClient{
		Client:                 c.Cluster.GetClient(),
		FieldExtractorRegistry: c.Registry,
	}
}

func (c *brokerCluster) SetFields(i interface{}) error {
	if _, err := inject.ConfigInto(c.GetConfig(), i); err != nil {
		return err
	}
	if _, err := inject.ClientInto(c.GetClient(), i); err != nil {
		return err
	}
	if _, err := clusterinject.BrokerClientInto(c.GetBrokerClient(), i); err != nil {
		return err
	}
	if _, err := inject.APIReaderInto(c.GetAPIReader(), i); err != nil {
		return err
	}
	if _, err := inject.SchemeInto(c.GetScheme(), i); err != nil {
		return err
	}
	if _, err := inject.CacheInto(c.GetCache(), i); err != nil {
		return err
	}
	if _, err := inject.MapperInto(c.GetRESTMapper(), i); err != nil {
		return err
	}
	return nil
}
