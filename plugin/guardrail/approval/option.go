//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
)

const defaultPluginName = "approval"

// Option configures the approval plugin.
type Option func(*options)

type options struct {
	name              string
	reviewer          review.Reviewer
	defaultToolPolicy ToolPolicy
	toolPolicies      map[string]ToolPolicy
	contextProvider   func(context.Context, *review.Request) (review.Supplement, error)
}

func newOptions(opts ...Option) *options {
	options := &options{
		name:              defaultPluginName,
		defaultToolPolicy: ToolPolicyRequireApproval,
		toolPolicies:      make(map[string]ToolPolicy),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}
	return options
}

// WithName sets the plugin name.
func WithName(name string) Option {
	return func(opts *options) {
		opts.name = name
	}
}

// WithReviewer sets the reviewer used for approval-required tool calls.
func WithReviewer(reviewer review.Reviewer) Option {
	return func(opts *options) {
		opts.reviewer = reviewer
	}
}

// WithDefaultToolPolicy sets the default policy used when no explicit tool policy exists.
func WithDefaultToolPolicy(policy ToolPolicy) Option {
	return func(opts *options) {
		opts.defaultToolPolicy = policy
	}
}

// WithToolPolicy sets the policy for a single tool name.
func WithToolPolicy(name string, policy ToolPolicy) Option {
	return func(opts *options) {
		if opts.toolPolicies == nil {
			opts.toolPolicies = make(map[string]ToolPolicy)
		}
		opts.toolPolicies[name] = policy
	}
}

// WithContextProvider supplies application evidence for PermissionPolicy reviews.
// It is not called by the legacy BeforeTool plugin or for skipped/denied actions.
// The provider receives an isolated request snapshot and may return only a
// Supplement; it cannot rewrite the reviewed action or framework provenance.
// Nil disables supplementation. Errors prevent execution. Calls may run
// concurrently and must honor context cancellation; the provider owns any I/O
// resources. It must not perform the pending action while collecting evidence.
//
// Review instructions are limited to 16000 runes, with at most 32 facts and
// 1024 runes per source label. Exceeding these limits returns an error. Each fact
// is limited to 4000 runes using head/tail truncation with Truncated set.
func WithContextProvider(provider func(context.Context, *review.Request) (review.Supplement, error)) Option {
	return func(opts *options) {
		opts.contextProvider = provider
	}
}
