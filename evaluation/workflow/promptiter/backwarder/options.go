//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package backwarder computes backward propagation outputs from trace and gradient data.
package backwarder

import (
	"context"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// options stores optional backwarder behavior toggles.
type options struct {
	runOptions        []agent.RunOption
	messageBuilder    MessageBuilder
	userIDSupplier    UserIDSupplier
	sessionIDSupplier SessionIDSupplier
}

// Option mutates backwarder options during construction.
type Option func(*options)

// newOptions applies all backwarder options and returns a configured options set.
func newOptions(opt ...Option) *options {
	opts := &options{
		messageBuilder:    defaultMessageBuilder(),
		userIDSupplier:    defaultUserIDSupplier(),
		sessionIDSupplier: defaultSessionIDSupplier(),
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithRunOptions appends runner options for backward invocations.
func WithRunOptions(runOptions ...agent.RunOption) Option {
	return func(opts *options) {
		opts.runOptions = append(opts.runOptions, runOptions...)
	}
}

// WithMessageBuilder overrides how backward requests are encoded for the runner.
func WithMessageBuilder(builder MessageBuilder) Option {
	return func(opts *options) {
		opts.messageBuilder = builder
	}
}

// UserIDSupplier provides a user ID for one backward runner invocation.
type UserIDSupplier func(ctx context.Context) string

func defaultUserIDSupplier() UserIDSupplier {
	return func(ctx context.Context) string {
		return uuid.NewString()
	}
}

// WithUserIDSupplier overrides how backward runner user IDs are generated.
func WithUserIDSupplier(supplier UserIDSupplier) Option {
	return func(o *options) {
		o.userIDSupplier = supplier
	}
}

// SessionIDSupplier provides a session ID for one backward runner invocation.
type SessionIDSupplier func(ctx context.Context) string

func defaultSessionIDSupplier() SessionIDSupplier {
	return func(ctx context.Context) string {
		return uuid.NewString()
	}
}

// WithSessionIDSupplier overrides how backward runner session IDs are generated.
func WithSessionIDSupplier(supplier SessionIDSupplier) Option {
	return func(o *options) {
		o.sessionIDSupplier = supplier
	}
}
