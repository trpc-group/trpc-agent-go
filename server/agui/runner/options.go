//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

// options holds the options for the runner.
type options struct {
	userIDResolver UserIDResolver
}

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{
		userIDResolver: func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
			return "user", nil
		},
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*options)

// UserIDResolver is a function that derives the user identifier for an AG-UI run.
type UserIDResolver func(ctx context.Context, input *adapter.RunAgentInput) (string, error)

// WithUserIDResolver sets the user ID resolver.
func WithUserIDResolver(resolver UserIDResolver) Option {
	return func(r *options) {
		r.userIDResolver = resolver
	}
}
