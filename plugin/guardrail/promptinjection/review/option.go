//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

const (
	reviewerUserIDPrefix    = "promptinjection-reviewer-user:"
	reviewerSessionIDPrefix = "promptinjection-reviewer-session:"
)

// Option configures the built-in prompt injection reviewer.
type Option func(*options)

// UserIDSupplier returns the user ID used for the internal reviewer run.
type UserIDSupplier func(ctx context.Context, req *Request) (string, error)

// SessionIDSupplier returns the session ID used for the internal reviewer run.
type SessionIDSupplier func(ctx context.Context, req *Request) (string, error)

type options struct {
	systemPrompt      string
	userIDSupplier    UserIDSupplier
	sessionIDSupplier SessionIDSupplier
}

func newOptions(opts ...Option) *options {
	options := &options{
		systemPrompt:      defaultSystemPromptText,
		userIDSupplier:    defaultUserIDSupplier,
		sessionIDSupplier: defaultSessionIDSupplier,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}
	return options
}

// WithSystemPrompt overrides the built-in reviewer system prompt.
func WithSystemPrompt(prompt string) Option {
	return func(opts *options) {
		opts.systemPrompt = prompt
	}
}

// WithUserIDSupplier overrides the user ID supplier for reviewer runs.
func WithUserIDSupplier(supplier UserIDSupplier) Option {
	return func(opts *options) {
		opts.userIDSupplier = supplier
	}
}

// WithSessionIDSupplier overrides the session ID supplier for reviewer runs.
func WithSessionIDSupplier(supplier SessionIDSupplier) Option {
	return func(opts *options) {
		opts.sessionIDSupplier = supplier
	}
}

func defaultUserIDSupplier(ctx context.Context, req *Request) (string, error) {
	invocation, ok := agent.InvocationFromContext(ctx)
	if ok && invocation != nil && invocation.Session != nil && invocation.Session.UserID != "" {
		return reviewerUserIDPrefix + invocation.Session.UserID, nil
	}
	return uuid.New().String(), nil
}

func defaultSessionIDSupplier(ctx context.Context, req *Request) (string, error) {
	invocation, ok := agent.InvocationFromContext(ctx)
	if ok && invocation != nil && invocation.Session != nil && invocation.Session.ID != "" {
		return reviewerSessionIDPrefix + invocation.Session.ID, nil
	}
	return uuid.New().String(), nil
}
