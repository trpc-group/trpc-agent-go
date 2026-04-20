//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	promptitermanager "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/manager"
)

const (
	defaultBasePath      = "/promptiter/v1/apps"
	defaultStructurePath = "/structure"
	defaultRunsPath      = "/runs"
	defaultAsyncRunsPath = "/async-runs"
)

// Option configures the PromptIter server.
type Option func(*options)

type options struct {
	appName       string
	basePath      string
	structurePath string
	runsPath      string
	asyncRunsPath string
	timeout       time.Duration
	engine        engine.Engine
	manager       promptitermanager.Manager
}

func newOptions(opt ...Option) *options {
	opts := &options{
		basePath:      defaultBasePath,
		structurePath: defaultStructurePath,
		runsPath:      defaultRunsPath,
		asyncRunsPath: defaultAsyncRunsPath,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithAppName sets the app name exposed by the PromptIter server.
func WithAppName(name string) Option {
	return func(opts *options) {
		opts.appName = name
	}
}

// WithBasePath sets the base collection path used by the PromptIter server.
func WithBasePath(path string) Option {
	return func(opts *options) {
		opts.basePath = path
	}
}

// WithStructurePath sets the structure endpoint path relative to BasePath/appName.
func WithStructurePath(path string) Option {
	return func(opts *options) {
		opts.structurePath = path
	}
}

// WithRunsPath sets the runs endpoint path relative to BasePath/appName.
func WithRunsPath(path string) Option {
	return func(opts *options) {
		opts.runsPath = path
	}
}

// WithAsyncRunsPath sets the asynchronous runs endpoint path relative to BasePath/appName.
func WithAsyncRunsPath(path string) Option {
	return func(opts *options) {
		opts.asyncRunsPath = path
	}
}

// WithTimeout sets the maximum execution time for a PromptIter run request.
func WithTimeout(timeout time.Duration) Option {
	return func(opts *options) {
		opts.timeout = timeout
	}
}

// WithEngine sets the PromptIter engine used by the server.
func WithEngine(promptIterEngine engine.Engine) Option {
	return func(opts *options) {
		opts.engine = promptIterEngine
	}
}

// WithManager sets the PromptIter manager used by the server.
func WithManager(promptIterManager promptitermanager.Manager) Option {
	return func(opts *options) {
		opts.manager = promptIterManager
	}
}
