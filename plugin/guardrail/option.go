//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package guardrail

import (
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/unsafeintent"
)

const defaultPluginName = "guardrail"

// Option configures the guardrail plugin.
type Option func(*options)

type options struct {
	name            string
	approval        *approval.Plugin
	promptInjection *promptinjection.Plugin
	unsafeIntent    *unsafeintent.Plugin
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

// WithApproval attaches the approval capability.
func WithApproval(approvalPlugin *approval.Plugin) Option {
	return func(opts *options) {
		opts.approval = approvalPlugin
	}
}

// WithPromptInjection attaches the prompt injection capability.
func WithPromptInjection(promptInjectionPlugin *promptinjection.Plugin) Option {
	return func(opts *options) {
		opts.promptInjection = promptInjectionPlugin
	}
}

// WithUnsafeIntent attaches the unsafe intent capability.
func WithUnsafeIntent(unsafeIntentPlugin *unsafeintent.Plugin) Option {
	return func(opts *options) {
		opts.unsafeIntent = unsafeIntentPlugin
	}
}
