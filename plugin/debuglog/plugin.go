//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package debuglog

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type debugLogPlugin struct {
	name                        string
	eventEnabled                bool
	modelPartialResponseEnabled bool
	seq                         atomic.Uint64
}

// New creates a debug log plugin.
func New(opts ...Option) plugin.Plugin {
	o := newOptions(opts...)
	return &debugLogPlugin{
		name:                        o.name,
		eventEnabled:                o.eventEnabled,
		modelPartialResponseEnabled: o.modelPartialResponseEnabled,
	}
}

// Name implements plugin.Plugin.
func (p *debugLogPlugin) Name() string {
	return p.name
}

// Register implements plugin.Plugin.
func (p *debugLogPlugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeAgent(p.beforeAgent)
	r.AfterAgent(p.afterAgent)
	r.BeforeModel(p.beforeModel)
	r.AfterModel(p.afterModel)
	r.BeforeTool(p.beforeTool)
	r.AfterTool(p.afterTool)
	r.OnEvent(p.onEvent)
}

func (p *debugLogPlugin) beforeAgent(
	ctx context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return nil, nil
	}
	payload := map[string]any{
		"invocation": invocationSnapshot(args.Invocation),
	}
	ent := p.newEntry("before_agent", args.Invocation)
	ent.Payload = payload
	p.writeEntry(ctx, ent)
	return nil, nil
}

func (p *debugLogPlugin) afterAgent(
	ctx context.Context,
	args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return nil, nil
	}
	payload := map[string]any{
		"invocation": invocationSnapshot(args.Invocation),
	}
	addFullResponseEventSnapshot(payload, args.FullResponseEvent)
	ent := p.newEntry("after_agent", args.Invocation)
	ent.Error = errorString(args.Error)
	ent.Payload = payload
	p.writeEntry(ctx, ent)
	return nil, nil
}

func (p *debugLogPlugin) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil {
		return nil, nil
	}
	payload := map[string]any{}
	addRequestSnapshot(payload, args.Request)
	ent := p.newEntry("before_model", invocationFromContext(ctx))
	ent.Payload = payload
	p.writeEntry(ctx, ent)
	return nil, nil
}

func (p *debugLogPlugin) afterModel(
	ctx context.Context,
	args *model.AfterModelArgs,
) (*model.AfterModelResult, error) {
	if args == nil {
		return nil, nil
	}
	if args.Response != nil && args.Response.IsPartial && !p.modelPartialResponseEnabled {
		return nil, nil
	}
	payload := map[string]any{}
	addRequestSnapshot(payload, args.Request)
	addJSONSnapshot(payload, "response", "response_encode_error", args.Response)
	ent := p.newEntry("after_model", invocationFromContext(ctx))
	ent.Error = errorString(args.Error)
	ent.Payload = payload
	p.writeEntry(ctx, ent)
	return nil, nil
}

func (p *debugLogPlugin) beforeTool(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if args == nil {
		return nil, nil
	}
	payload := map[string]any{}
	addToolDeclarationSnapshot(payload, args.Declaration)
	addToolArgumentsSnapshot(payload, args.Arguments)
	addJSONSnapshot(payload, "resume_value", "resume_value_encode_error", args.ResumeValue)
	addJSONMapSnapshot(payload, "resume_map", "resume_map_errors", args.ResumeMap)
	ent := p.newEntry("before_tool", invocationFromContext(ctx))
	ent.ToolName = args.ToolName
	ent.ToolCallID = toolCallID(ctx, args.ToolCallID)
	ent.Payload = payload
	p.writeEntry(ctx, ent)
	return nil, nil
}

func (p *debugLogPlugin) afterTool(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if args == nil {
		return nil, nil
	}
	payload := map[string]any{}
	addToolDeclarationSnapshot(payload, args.Declaration)
	addToolArgumentsSnapshot(payload, args.Arguments)
	addJSONSnapshot(payload, "result", "result_encode_error", args.Result)
	addJSONMapSnapshot(payload, "meta", "meta_errors", args.Meta)
	ent := p.newEntry("after_tool", invocationFromContext(ctx))
	ent.ToolName = args.ToolName
	ent.ToolCallID = toolCallID(ctx, args.ToolCallID)
	ent.Error = errorString(args.Error)
	ent.Payload = payload
	p.writeEntry(ctx, ent)
	return nil, nil
}

func (p *debugLogPlugin) onEvent(
	ctx context.Context,
	inv *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	if !p.eventEnabled || e == nil {
		return nil, nil
	}
	if inv == nil {
		inv = invocationFromContext(ctx)
	}
	payload := map[string]any{}
	addEventSnapshot(payload, e)
	ent := p.newEntry("event", inv)
	ent.Payload = payload
	p.writeEntry(ctx, ent)
	return nil, nil
}

func (p *debugLogPlugin) newEntry(
	phase string,
	inv *agent.Invocation,
) *entry {
	ent := &entry{
		Time:     time.Now(),
		Sequence: p.seq.Add(1),
		Plugin:   p.name,
		Phase:    phase,
	}
	applyInvocationFields(ent, inv)
	return ent
}

func (p *debugLogPlugin) writeEntry(ctx context.Context, ent *entry) {
	raw, err := json.Marshal(ent)
	if err != nil {
		log.WarnfContext(ctx, "plugin=%s debuglog marshal entry failed: %v", p.name, err)
		return
	}
	log.DebugfContext(ctx, "%s", raw)
}

func invocationFromContext(ctx context.Context) *agent.Invocation {
	inv, _ := agent.InvocationFromContext(ctx)
	return inv
}

func toolCallID(ctx context.Context, argID string) string {
	if argID != "" {
		return argID
	}
	callID, _ := tool.ToolCallIDFromContext(ctx)
	return callID
}
