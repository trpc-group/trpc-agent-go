//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagent

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Option configures a tRPC-Agent API server.
type Option func(*options)

type options struct {
	basePath string
	timeout  time.Duration
	appName  string
	agent    agent.Agent
	runner   runner.Runner
}

// WithBasePath sets the tRPC-Agent API base path.
func WithBasePath(path string) Option {
	return func(opts *options) {
		opts.basePath = path
	}
}

// WithTimeout sets an optional per-request timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(opts *options) {
		opts.timeout = timeout
	}
}

// WithAppName sets the app name matched in request paths.
func WithAppName(appName string) Option {
	return func(opts *options) {
		opts.appName = appName
	}
}

// WithAgent sets the root agent used for structure export.
func WithAgent(ag agent.Agent) Option {
	return func(opts *options) {
		opts.agent = ag
	}
}

// WithRunner sets the runner used to execute requests.
func WithRunner(r runner.Runner) Option {
	return func(opts *options) {
		opts.runner = r
	}
}

func newOptions(opts ...Option) options {
	options := options{
		basePath: defaultBasePath,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}
