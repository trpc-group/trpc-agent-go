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
	"time"

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
	defaultCancelPath            = "/cancel"
	defaultMessagesSnapshotState = false
	defaultCancelState           = false
)

// options holds the options for the AG-UI server.
type options struct {
	basePath                string
	path                    string
	serviceFactory          ServiceFactory
	aguiRunnerOptions       []aguirunner.Option
	messagesSnapshotPath    string
	messagesSnapshotEnabled bool
	cancelPath              string
	cancelEnabled           bool
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
		cancelPath:              defaultCancelPath,
		cancelEnabled:           defaultCancelState,
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

// WithCancelPath sets the cancel endpoint path for AG-UI service, "/cancel" in default.
func WithCancelPath(path string) Option {
	return func(o *options) {
		o.cancelPath = path
	}
}

// WithCancelEnabled enables the cancel handler.
func WithCancelEnabled(e bool) Option {
	return func(o *options) {
		o.cancelEnabled = e
	}
}

// WithCancelOnContextDoneEnabled controls whether an AG-UI run is canceled when the request context is done.
func WithCancelOnContextDoneEnabled(enabled bool) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithCancelOnContextDoneEnabled(enabled))
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

// WithTimeout sets the maximum execution time for a run, 1h in default.
func WithTimeout(d time.Duration) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithTimeout(d))
	}
}

// WithFlushInterval sets how often buffered AG-UI events are flushed for a session.
func WithFlushInterval(d time.Duration) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithFlushInterval(d))
	}
}

// WithGraphNodeLifecycleActivityEnabled controls whether the AG-UI server emits graph node lifecycle activity events.
func WithGraphNodeLifecycleActivityEnabled(enabled bool) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithGraphNodeLifecycleActivityEnabled(enabled))
	}
}

// WithGraphNodeInterruptActivityEnabled controls whether the AG-UI server emits graph interrupt activity events.
func WithGraphNodeInterruptActivityEnabled(enabled bool) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithGraphNodeInterruptActivityEnabled(enabled))
	}
}

// WithGraphNodeInterruptActivityTopLevelOnly controls whether the AG-UI server only emits graph interrupt activity events for the top-level invocation.
func WithGraphNodeInterruptActivityTopLevelOnly(enabled bool) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithGraphNodeInterruptActivityTopLevelOnly(enabled))
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

// WithMessagesSnapshotFollowEnabled controls whether the messages snapshot handler tails persisted track events.
func WithMessagesSnapshotFollowEnabled(enabled bool) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithMessagesSnapshotFollowEnabled(enabled))
	}
}

// WithMessagesSnapshotFollowMaxDuration sets the maximum duration for messages snapshot tailing.
func WithMessagesSnapshotFollowMaxDuration(d time.Duration) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguirunner.WithMessagesSnapshotFollowMaxDuration(d))
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
