//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package guardrail provides the top-level guardrail plugin facade.
package guardrail

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/unsafeintent"
)

// Plugin is the top-level guardrail plugin facade.
type Plugin struct {
	name            string
	approval        *approval.Plugin
	promptInjection *promptinjection.Plugin
	unsafeIntent    *unsafeintent.Plugin
}

// New creates a new guardrail plugin.
func New(options ...Option) (*Plugin, error) {
	opts := newOptions(options...)
	return &Plugin{
		name:            opts.name,
		approval:        opts.approval,
		promptInjection: opts.promptInjection,
		unsafeIntent:    opts.unsafeIntent,
	}, nil
}

// Name implements plugin.Plugin.
func (p *Plugin) Name() string {
	return p.name
}

// Register implements plugin.Plugin.
func (p *Plugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	if p.unsafeIntent != nil {
		p.unsafeIntent.Register(r)
	}
	if p.promptInjection != nil {
		p.promptInjection.Register(r)
	}
	if p.approval != nil {
		p.approval.Register(r)
	}
}

// Close implements plugin.Closer when sub-capabilities need cleanup.
func (p *Plugin) Close(ctx context.Context) error {
	var closeErr error
	if p.promptInjection != nil {
		closer, ok := any(p.promptInjection).(plugin.Closer)
		if ok {
			closeErr = errors.Join(closeErr, closer.Close(ctx))
		}
	}
	if p.unsafeIntent != nil {
		closer, ok := any(p.unsafeIntent).(plugin.Closer)
		if ok {
			closeErr = errors.Join(closeErr, closer.Close(ctx))
		}
	}
	if p.approval != nil {
		closer, ok := any(p.approval).(plugin.Closer)
		if ok {
			closeErr = errors.Join(closeErr, closer.Close(ctx))
		}
	}
	return closeErr
}
