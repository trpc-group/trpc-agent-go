//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package messagemerger provides a runner-scoped plugin that merges
// consecutive messages with the same role.
package messagemerger

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

// messageMergerPlugin merges consecutive system, user, and assistant messages before a
// model request is sent.
//
// Tool messages are intentionally preserved one-by-one because their ToolID and
// ToolName fields carry per-call semantics that must not be collapsed.
type messageMergerPlugin struct {
	name      string
	separator string
}

// New creates a new message merger plugin.
func New(options ...Option) plugin.Plugin {
	opts := newOptions(options...)
	if opts.name == "" {
		opts.name = defaultPluginName
	}
	return &messageMergerPlugin{
		name:      opts.name,
		separator: opts.separator,
	}
}

// Name implements plugin.Plugin.
func (p *messageMergerPlugin) Name() string {
	return p.name
}

// Register implements plugin.Plugin.
func (p *messageMergerPlugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeModel(p.beforeModel)
}

func (p *messageMergerPlugin) beforeModel(
	_ context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if p == nil || args == nil || args.Request == nil {
		return nil, nil
	}
	args.Request.Messages = mergeConsecutiveMessages(
		args.Request.Messages,
		p.separator,
	)
	return nil, nil
}
