//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package aggregator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Option configures an Aggregator.
type Option func(*options)

type options struct {
	userIDSupplier    func(context.Context) string
	sessionIDSupplier func(context.Context) string
	runOptions        []agent.RunOption
	messageBuilder    func(context.Context, []issue.IssueRecord) (model.Message, error)
}

func newOptions(opt ...Option) *options {
	opts := &options{
		userIDSupplier: func(context.Context) string {
			return uuid.NewString()
		},
		sessionIDSupplier: func(context.Context) string {
			return uuid.NewString()
		},
		messageBuilder: func(ctx context.Context, issues []issue.IssueRecord) (model.Message, error) {
			payload, err := json.Marshal(map[string]any{"issues": issues})
			if err != nil {
				return model.Message{}, fmt.Errorf("marshal issues: %w", err)
			}
			return model.NewUserMessage(string(payload)), nil
		},
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithUserIDSupplier sets the user ID supplier used for runner invocations.
func WithUserIDSupplier(supplier func(context.Context) string) Option {
	return func(o *options) {
		o.userIDSupplier = supplier
	}
}

// WithSessionIDSupplier sets the session ID supplier used for runner invocations.
func WithSessionIDSupplier(supplier func(context.Context) string) Option {
	return func(o *options) {
		o.sessionIDSupplier = supplier
	}
}

// WithRunOptions appends run options for runner invocations.
func WithRunOptions(runOptions ...agent.RunOption) Option {
	return func(o *options) {
		o.runOptions = append(o.runOptions, runOptions...)
	}
}

// WithMessageBuilder overrides how raw issues are turned into a runner message.
func WithMessageBuilder(builder func(context.Context, []issue.IssueRecord) (model.Message, error)) Option {
	return func(o *options) {
		o.messageBuilder = builder
	}
}
