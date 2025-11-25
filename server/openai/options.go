//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Option configures the OpenAI server.
type Option func(*options)

// options holds the configuration for the OpenAI server.
type options struct {
	basePath       string // basePath is the base path for the service.
	path           string // path is the chat completions endpoint path.
	sessionService session.Service
	agent          agent.Agent
	runner         runner.Runner
	modelName      string
	appName        string
}

// WithBasePath sets the base path for the server.
// Default is "/v1".
func WithBasePath(path string) Option {
	return func(opts *options) {
		opts.basePath = path
	}
}

// WithPath sets the chat completions endpoint path.
// Default is "/chat/completions".
func WithPath(path string) Option {
	return func(opts *options) {
		opts.path = path
	}
}

// WithSessionService sets the session service.
// If not provided, an in-memory session service will be used.
func WithSessionService(svc session.Service) Option {
	return func(opts *options) {
		opts.sessionService = svc
	}
}

// WithAgent sets the agent to use.
// Either WithAgent or WithRunner must be provided.
func WithAgent(ag agent.Agent) Option {
	return func(opts *options) {
		opts.agent = ag
	}
}

// WithRunner sets the runner to use.
// If not provided, a runner will be created from the agent.
func WithRunner(r runner.Runner) Option {
	return func(opts *options) {
		opts.runner = r
	}
}

// WithModelName sets the model name to return in responses.
// Default is "gpt-3.5-turbo".
func WithModelName(name string) Option {
	return func(opts *options) {
		opts.modelName = name
	}
}

// WithAppName sets the app name for the runner.
// Default is "openai-server".
func WithAppName(name string) Option {
	return func(opts *options) {
		opts.appName = name
	}
}
