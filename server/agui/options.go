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
	"trpc.group/trpc-go/trpc-agent-go/runner"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

const (
	// defaultPath is the default path for the AG-UI service.
	defaultPath = "/"
)

// options holds the options for the AG-UI server.
type options struct {
	path              string
	service           service.Service
	runnerOptions     []runner.Option
	aguiRunnerOptions []aguirunner.Option
}

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{
		path: defaultPath,
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

// WithService sets the service.
func WithService(s service.Service) Option {
	return func(o *options) {
		o.service = s
	}
}

// WithRunnerOptions sets the runner options.
func WithRunnerOptions(runnerOpts ...runner.Option) Option {
	return func(o *options) {
		o.runnerOptions = append(o.runnerOptions, runnerOpts...)
	}
}

// WithAGUIRunnerOptions sets the AG-UI runner options.
func WithAGUIRunnerOptions(aguiRunnerOpts ...aguirunner.Option) Option {
	return func(o *options) {
		o.aguiRunnerOptions = append(o.aguiRunnerOptions, aguiRunnerOpts...)
	}
}
