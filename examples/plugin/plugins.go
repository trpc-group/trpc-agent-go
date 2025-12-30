//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	demoPluginName = "demo_plugin"
	demoTag        = "plugin_demo"
	denyKeyword    = "/deny"

	assistantPrefix = "[plugin] "
	denyText        = "Blocked by Runner plugin (BeforeModel short-circuit)."
)

type demoPlugin struct {
	debug bool
}

func newDemoPlugin(debug bool) plugin.Plugin {
	return &demoPlugin{debug: debug}
}

func (p *demoPlugin) Name() string {
	return demoPluginName
}

func (p *demoPlugin) Register(r *plugin.Registry) {
	r.BeforeAgent(p.beforeAgent)
	r.AfterAgent(p.afterAgent)
	r.BeforeModel(p.beforeModel)
	r.BeforeTool(p.beforeTool)
	r.AfterTool(p.afterTool)
	r.OnEvent(p.onEvent)
}

func (p *demoPlugin) beforeAgent(
	ctx context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if !p.debug || args == nil || args.Invocation == nil {
		return nil, nil
	}
	fmt.Printf(
		"[plugin] before_agent agent=%s\n",
		args.Invocation.AgentName,
	)
	return nil, nil
}

func (p *demoPlugin) afterAgent(
	ctx context.Context,
	args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
	if !p.debug || args == nil || args.Invocation == nil {
		return nil, nil
	}
	errText := ""
	if args.Error != nil {
		errText = args.Error.Error()
	}
	fmt.Printf(
		"[plugin] after_agent agent=%s err=%s\n",
		args.Invocation.AgentName,
		errText,
	)
	return nil, nil
}

func (p *demoPlugin) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}

	if !requestHasUserKeyword(args.Request, denyKeyword) {
		return nil, nil
	}

	if p.debug {
		fmt.Printf("[plugin] before_model short_circuit keyword=%s\n",
			denyKeyword,
		)
	}

	return &model.BeforeModelResult{
		CustomResponse: denyResponse(),
	}, nil
}

func requestHasUserKeyword(req *model.Request, keyword string) bool {
	if req == nil {
		return false
	}
	for _, msg := range req.Messages {
		if msg.Role != model.RoleUser {
			continue
		}
		if strings.Contains(msg.Content, keyword) {
			return true
		}
	}
	return false
}

func denyResponse() *model.Response {
	return &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(denyText),
		}},
	}
}

func (p *demoPlugin) beforeTool(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if !p.debug || args == nil {
		return nil, nil
	}
	callID, _ := tool.ToolCallIDFromContext(ctx)
	fmt.Printf(
		"[plugin] before_tool tool=%s call_id=%s\n",
		args.ToolName,
		callID,
	)
	return nil, nil
}

func (p *demoPlugin) afterTool(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if !p.debug || args == nil {
		return nil, nil
	}
	callID, _ := tool.ToolCallIDFromContext(ctx)
	errText := ""
	if args.Error != nil {
		errText = args.Error.Error()
	}
	fmt.Printf(
		"[plugin] after_tool tool=%s call_id=%s err=%s\n",
		args.ToolName,
		callID,
		errText,
	)
	return nil, nil
}

func (p *demoPlugin) onEvent(
	_ context.Context,
	_ *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	if e == nil {
		return nil, nil
	}

	addTag(e, demoTag)
	addAssistantPrefix(e, assistantPrefix)

	return nil, nil
}

func addTag(e *event.Event, tag string) {
	if e == nil || tag == "" {
		return
	}
	if e.ContainsTag(tag) {
		return
	}
	if e.Tag == "" {
		e.Tag = tag
		return
	}
	e.Tag = e.Tag + event.TagDelimiter + tag
}

func addAssistantPrefix(e *event.Event, prefix string) {
	if e == nil || prefix == "" {
		return
	}
	if e.Response == nil || e.IsPartial || len(e.Response.Choices) == 0 {
		return
	}
	msg := e.Response.Choices[0].Message
	if msg.Role != model.RoleAssistant || msg.Content == "" {
		return
	}
	if strings.HasPrefix(msg.Content, prefix) {
		return
	}
	msg.Content = prefix + msg.Content
	e.Response.Choices[0].Message = msg
}
