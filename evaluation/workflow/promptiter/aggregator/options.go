//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package aggregator normalizes and aggregates per-surface gradients before optimization.
package aggregator

import (
	"context"
	"reflect"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// options stores optional aggregation behavior.
type options struct {
	runOptions        []agent.RunOption
	messageBuilder    MessageBuilder
	userIDSupplier    UserIDSupplier
	sessionIDSupplier SessionIDSupplier
}

// Option mutates aggregator options during construction.
type Option func(*options)

// newOptions applies all aggregator options and returns final constructor state.
func newOptions(opt ...Option) *options {
	opts := &options{
		runOptions: []agent.RunOption{
			aggregatedGradientStructuredOutput(),
		},
		messageBuilder:    defaultMessageBuilder(),
		userIDSupplier:    defaultUserIDSupplier(),
		sessionIDSupplier: defaultSessionIDSupplier(),
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithRunOptions appends runner options for aggregation invocations.
func WithRunOptions(runOptions ...agent.RunOption) Option {
	return func(opts *options) {
		opts.runOptions = append(opts.runOptions, runOptions...)
	}
}

func aggregatedGradientStructuredOutput() agent.RunOption {
	return func(opts *agent.RunOptions) {
		agent.WithStructuredOutputJSONSchema(
			"AggregatedGradientProposal",
			aggregatedGradientProposalSchema(),
			true,
			"One aggregated PromptIter gradient proposal.",
		)(opts)
		opts.StructuredOutputType = reflect.TypeOf((*aggregatedGradientProposal)(nil))
	}
}

func aggregatedGradientProposalSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"Gradients": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"Severity": map[string]any{
							"type": "string",
							"enum": []string{
								string(promptiter.LossSeverityP0),
								string(promptiter.LossSeverityP1),
								string(promptiter.LossSeverityP2),
								string(promptiter.LossSeverityP3),
							},
						},
						"Gradient": map[string]any{
							"type": "string",
						},
					},
					"required":             []string{"Severity", "Gradient"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"Gradients"},
		"additionalProperties": false,
	}
}

// WithMessageBuilder overrides how aggregation requests are encoded for the runner.
func WithMessageBuilder(builder MessageBuilder) Option {
	return func(opts *options) {
		opts.messageBuilder = builder
	}
}

// UserIDSupplier provides a user ID for one aggregation runner invocation.
type UserIDSupplier func(ctx context.Context) string

func defaultUserIDSupplier() UserIDSupplier {
	return func(ctx context.Context) string {
		return uuid.NewString()
	}
}

// WithUserIDSupplier overrides how aggregation runner user IDs are generated.
func WithUserIDSupplier(supplier UserIDSupplier) Option {
	return func(o *options) {
		o.userIDSupplier = supplier
	}
}

// SessionIDSupplier provides a session ID for one aggregation runner invocation.
type SessionIDSupplier func(ctx context.Context) string

func defaultSessionIDSupplier() SessionIDSupplier {
	return func(ctx context.Context) string {
		return uuid.NewString()
	}
}

// WithSessionIDSupplier overrides how aggregation runner session IDs are generated.
func WithSessionIDSupplier(supplier SessionIDSupplier) Option {
	return func(o *options) {
		o.sessionIDSupplier = supplier
	}
}
