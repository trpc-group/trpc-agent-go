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
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// DefaultAddress is the default listen address.
	DefaultAddress = "0.0.0.0:8080"
	// DefaultPath is the default HTTP path the SSE handler is mounted on.
	DefaultPath = "/agui"
)

// options holds the options for the AG-UI server.
type options struct {
	address        string
	path           string
	service        service.Service
	sessionService session.Service
	runnerOptions  []aguirunner.Option
}

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{
		address: DefaultAddress,
		path:    DefaultPath,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*options)

// WithService sets the service.
func WithService(s service.Service) Option {
	return func(o *options) {
		o.service = s
	}
}

// WithSessionService sets the session service.
func WithSessionService(svc session.Service) Option {
	return func(o *options) {
		o.sessionService = svc
	}
}

// WithRunnerOptions sets the runner options.
func WithRunnerOptions(runnerOpts ...aguirunner.Option) Option {
	return func(o *options) {
		o.runnerOptions = append(o.runnerOptions, runnerOpts...)
	}
}

// WithAddress sets the address for service listening.
func WithAddress(addr string) Option {
	return func(o *options) {
		o.address = addr
	}
}

// WithPath sets the path for service listening.
func WithPath(path string) Option {
	return func(o *options) {
		o.path = path
	}
}
