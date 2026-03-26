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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
)

// Plugin is the top-level guardrail plugin facade.
type Plugin struct {
	name     string
	approval *approval.Plugin
}

// New creates a new guardrail plugin.
func New(options ...Option) (*Plugin, error) {
	opts := newOptions(options...)
	if opts.approval == nil {
		return nil, fmt.Errorf("newing guardrail plugin: no guardrail capability configured")
	}
	return &Plugin{
		name:     opts.name,
		approval: opts.approval,
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
	if p.approval != nil {
		p.approval.Register(r)
	}
}

// Close implements plugin.Closer when sub-capabilities need cleanup.
func (p *Plugin) Close(ctx context.Context) error {
	if p.approval == nil {
		return nil
	}
	closer, ok := any(p.approval).(plugin.Closer)
	if !ok {
		return nil
	}
	return closer.Close(ctx)
}
