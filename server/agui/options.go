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
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	defaultBasePath              = "/"
	defaultPath                  = "/"
	defaultServiceFactory        = sse.New
	defaultMessagesSnapshotPath  = "/history"
	defaultMessagesSnapshotState = false
)

// options holds the options for the AG-UI server.
type options struct {
	basePath                string
	path                    string
	serviceFactory          ServiceFactory
	aguiRunnerOptions       []aguirunner.Option
	messagesSnapshotPath    string
	messagesSnapshotEnabled bool
	appName                 string
	sessionService          session.Service
}

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{
		basePath:                defaultBasePath,
		path:                    defaultPath,
		serviceFactory:          defaultServiceFactory,
		messagesSnapshotPath:    defaultMessagesSnapshotPath,
		messagesSnapshotEnabled: defaultMessagesSnapshotState,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*options)

// WithBasePath sets the base path for service listening, "/" in default.
func WithBasePath(basePath string) Option {
	return func(o *options) {
		o.basePath = basePath
	}
}

// WithPath sets the chat message endpoint path for AG-UI service, "/" in default.
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

// WithMessagesSnapshotPath sets the HTTP path for the messages snapshot handler, "/history" in default.
func WithMessagesSnapshotPath(p string) Option {
	return func(o *options) {
		o.messagesSnapshotPath = p
	}
}

// WithMessagesSnapshotEnabled enables the MessagesSnapshotHandler.
func WithMessagesSnapshotEnabled(e bool) Option {
	return func(o *options) {
		o.messagesSnapshotEnabled = e
	}
}

// WithAppName sets the app name.
func WithAppName(n string) Option {
	return func(o *options) {
		o.appName = n
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithAppName(n))
	}
}

// WithSessionService sets the session service.
func WithSessionService(service session.Service) Option {
	return func(o *options) {
		o.sessionService = service
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithSessionService(service))
	}
}
