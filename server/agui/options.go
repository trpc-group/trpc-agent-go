//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agui

import (
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service/sse"
)

var (
	defaultPath           = "/"
	defaultServiceFactory = sse.New
)

// options holds the options for the AG-UI server.
type options struct {
	path              string
	serviceFactory    ServiceFactory
	aguiRunnerOptions []aguirunner.Option
}

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{
		path:           defaultPath,
		serviceFactory: defaultServiceFactory,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*options)

// WithPath sets the path for service listening.
func WithPath(path string) Option {
	return func(o *options) {
		o.path = path
	}
}

// ServiceFactory is a function that creates AG-UI service.
type ServiceFactory func(runner aguirunner.Runner, opt ...service.Option) service.Service

// WithServiceFactory sets the service factory, sse.New in default.
func WithServiceFactory(f ServiceFactory) Option {
	return func(o *options) {
		o.serviceFactory = f
	}
}

// WithAGUIRunnerOptions sets the AG-UI runner options.
func WithAGUIRunnerOptions(aguiRunnerOpts ...aguirunner.Option) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguiRunnerOpts...)
	}
}
