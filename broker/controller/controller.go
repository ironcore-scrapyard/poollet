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

package controller

import (
	"time"

	"github.com/go-logr/logr"
	brokerinject "github.com/onmetal/poollet/broker/inject"
	"github.com/onmetal/poollet/broker/manager"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/ratelimiter"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type Controller interface {
	controller.Controller
	WatchTarget(src source.Source, hdl handler.EventHandler, prct ...predicate.Predicate) error
}

type Options struct {
	// MaxConcurrentReconciles is the maximum number of concurrent Reconciles which can be run. Defaults to 1.
	MaxConcurrentReconciles int

	// Reconciler reconciles an object
	Reconciler reconcile.Reconciler

	// RateLimiter is used to limit how frequently requests may be queued.
	// Defaults to MaxOfRateLimiter which has both overall and per-item rate limiting.
	// The overall is a token bucket and the per-item is exponential.
	RateLimiter ratelimiter.RateLimiter

	// LogConstructor is used to construct a logger used for this controller and passed
	// to each reconciliation via the context field.
	LogConstructor func(request *reconcile.Request) logr.Logger

	// CacheSyncTimeout refers to the time limit set to wait for syncing caches.
	// Defaults to 2 minutes if not set.
	CacheSyncTimeout time.Duration

	// RecoverPanic indicates whether the panic caused by reconcile should be recovered.
	RecoverPanic bool
}

func (o *Options) controllerOptions() controller.Options {
	return controller.Options{
		MaxConcurrentReconciles: o.MaxConcurrentReconciles,
		Reconciler:              o.Reconciler,
		RateLimiter:             o.RateLimiter,
		LogConstructor:          o.LogConstructor,
		CacheSyncTimeout:        o.CacheSyncTimeout,
		RecoverPanic:            o.RecoverPanic,
	}
}

type brokerController struct {
	controller.Controller
	SetTargetFields inject.Func
}

func NewUnmanaged(name string, mgr manager.Manager, opts Options) (Controller, error) {
	c, err := controller.NewUnmanaged(name, mgr, opts.controllerOptions())
	if err != nil {
		return nil, err
	}

	return &brokerController{
		Controller:      c,
		SetTargetFields: mgr.SetTargetFields,
	}, nil
}

func New(name string, mgr manager.Manager, opts Options) (Controller, error) {
	c, err := NewUnmanaged(name, mgr, opts)
	if err != nil {
		return nil, err
	}

	if err := mgr.Add(c); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *brokerController) Watch(src source.Source, evtHandler handler.EventHandler, prct ...predicate.Predicate) error {
	return c.Controller.Watch(src, evtHandler, prct...)
}

func (c *brokerController) WatchTarget(src source.Source, evtHandler handler.EventHandler, prct ...predicate.Predicate) error {
	if err := c.SetTargetFields(src); err != nil {
		return err
	}
	if err := c.SetTargetFields(evtHandler); err != nil {
		return err
	}
	for _, pr := range prct {
		if err := c.SetTargetFields(pr); err != nil {
			return err
		}
	}

	src = brokerinject.ShieldSource(src)
	evtHandler = brokerinject.ShieldEventHandler(evtHandler)
	prct = brokerinject.ShieldPredicates(prct)
	return c.Watch(src, evtHandler, prct...)
}
