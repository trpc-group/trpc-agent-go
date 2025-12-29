//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package plugin

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultLoggingPluginName = "logging"
)

type (
	agentStartTimeKey struct{}
	modelStartTimeKey struct{}
	toolStartTimeKey  struct{}
)

// Logging logs high-level lifecycle events for agents, tools, and models.
type Logging struct {
	name string
}

// NewLogging creates a Logging plugin with a default name.
func NewLogging() *Logging {
	return NewNamedLogging(defaultLoggingPluginName)
}

// NewNamedLogging creates a Logging plugin with a custom name.
func NewNamedLogging(name string) *Logging {
	if name == "" {
		name = defaultLoggingPluginName
	}
	return &Logging{name: name}
}

// Name implements Plugin.
func (p *Logging) Name() string { return p.name }

// Register implements Plugin.
func (p *Logging) Register(r *Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeAgent(p.beforeAgent)
	r.AfterAgent(p.afterAgent)
	r.BeforeModel(p.beforeModel)
	r.AfterModel(p.afterModel)
	r.BeforeTool(p.beforeTool)
	r.AfterTool(p.afterTool)
}

func (p *Logging) beforeAgent(
	ctx context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return nil, nil
	}
	start := time.Now()
	log.DebugfContext(
		ctx,
		"plugin=%s agent=%s phase=start",
		p.name,
		args.Invocation.AgentName,
	)
	return &agent.BeforeAgentResult{
		Context: context.WithValue(ctx, agentStartTimeKey{}, start),
	}, nil
}

func (p *Logging) afterAgent(
	ctx context.Context,
	args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return nil, nil
	}
	start, _ := ctx.Value(agentStartTimeKey{}).(time.Time)
	duration := time.Since(start)
	errText := ""
	if args.Error != nil {
		errText = args.Error.Error()
	}
	log.DebugfContext(
		ctx,
		"plugin=%s agent=%s phase=end duration=%s err=%s",
		p.name,
		args.Invocation.AgentName,
		duration,
		errText,
	)
	return nil, nil
}

func (p *Logging) beforeModel(
	ctx context.Context,
	_ *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	inv, _ := agent.InvocationFromContext(ctx)
	start := time.Now()
	modelName := ""
	if inv != nil && inv.Model != nil {
		modelName = inv.Model.Info().Name
	}
	agentName := ""
	if inv != nil {
		agentName = inv.AgentName
	}
	log.DebugfContext(
		ctx,
		"plugin=%s agent=%s model=%s phase=start",
		p.name,
		agentName,
		modelName,
	)
	return &model.BeforeModelResult{
		Context: context.WithValue(ctx, modelStartTimeKey{}, start),
	}, nil
}

func (p *Logging) afterModel(
	ctx context.Context,
	args *model.AfterModelArgs,
) (*model.AfterModelResult, error) {
	start, _ := ctx.Value(modelStartTimeKey{}).(time.Time)
	duration := time.Since(start)
	errText := ""
	if args != nil && args.Error != nil {
		errText = args.Error.Error()
	}
	log.DebugfContext(
		ctx,
		"plugin=%s phase=end duration=%s err=%s",
		p.name,
		duration,
		errText,
	)
	return nil, nil
}

func (p *Logging) beforeTool(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if args == nil {
		return nil, nil
	}
	start := time.Now()
	callID, _ := tool.ToolCallIDFromContext(ctx)
	log.DebugfContext(
		ctx,
		"plugin=%s tool=%s call_id=%s phase=start",
		p.name,
		args.ToolName,
		callID,
	)
	return &tool.BeforeToolResult{
		Context: context.WithValue(ctx, toolStartTimeKey{}, start),
	}, nil
}

func (p *Logging) afterTool(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	start, _ := ctx.Value(toolStartTimeKey{}).(time.Time)
	duration := time.Since(start)
	callID, _ := tool.ToolCallIDFromContext(ctx)
	errText := ""
	if args != nil && args.Error != nil {
		errText = args.Error.Error()
	}
	log.DebugfContext(
		ctx,
		"plugin=%s tool=%s call_id=%s phase=end duration=%s err=%s",
		p.name,
		args.ToolName,
		callID,
		duration,
		errText,
	)
	return nil, nil
}
