//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package unsafeintent

import "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/unsafeintent/review"

const defaultPluginName = "unsafeintent"

// Option configures the unsafe intent plugin.
type Option func(*options)

type options struct {
	name     string
	reviewer review.Reviewer
}

func newOptions(opts ...Option) *options {
	options := &options{
		name: defaultPluginName,
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

// WithReviewer sets the mandatory reviewer used for unsafe intent decisions.
func WithReviewer(reviewer review.Reviewer) Option {
	return func(opts *options) {
		opts.reviewer = reviewer
	}
}
