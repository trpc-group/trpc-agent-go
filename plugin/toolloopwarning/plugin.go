//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/toolresultround"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const (
	pluginName    = "tool_loop_warning"
	stateKey      = "plugin:toolloopwarning"
	warningSource = "plugin/toolloopwarning"
	warning       = "The same tool-call loop has repeated. Please change your approach or stop calling the same tools with the same inputs."
)

type toolLoopWarningPlugin struct{}

// New returns an opt-in plugin that persists a synthetic user-role instruction
// when two consecutive complete tool rounds are identical. The detector resets
// after each match and never stops or retries the invocation.
func New() plugin.Plugin {
	return &toolLoopWarningPlugin{}
}

// Name implements plugin.Plugin.
func (p *toolLoopWarningPlugin) Name() string {
	if p == nil {
		return ""
	}
	return pluginName
}

// Register implements plugin.Plugin.
func (p *toolLoopWarningPlugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeAgent(p.beforeAgent)
	r.AfterToolMessages(p.afterToolMessages)
	r.OnEvent(p.onEvent)
	r.AfterAgent(p.afterAgent)
}

func (p *toolLoopWarningPlugin) beforeAgent(
	_ context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	args.Invocation.SetState(stateKey, &detectorState{})
	return nil, nil
}

func (p *toolLoopWarningPlugin) afterToolMessages(
	_ context.Context,
	args *plugin.AfterToolMessagesArgs,
) (*plugin.AfterToolMessagesResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	state := detectorStateFor(args.Invocation)
	if !state.observeToolMessages(args.ToolCalls, args.ToolResultMessages) {
		return nil, nil
	}
	steer.EnqueueWithSource(
		args.Invocation,
		model.NewUserMessage(warning),
		warningSource,
	)
	return nil, nil
}

func (p *toolLoopWarningPlugin) onEvent(
	_ context.Context,
	invocation *agent.Invocation,
	ev *event.Event,
) (*event.Event, error) {
	if p == nil || invocation == nil || ev == nil {
		return ev, nil
	}
	if toolresultround.HasMarker(ev) &&
		(ev.Response == nil ||
			ev.Response.Object != model.ObjectTypeToolResponse) {
		detectorStateFor(invocation).reset()
	}
	return ev, nil
}

func detectorStateFor(invocation *agent.Invocation) *detectorState {
	state, ok := agent.GetStateValue[*detectorState](invocation, stateKey)
	if ok && state != nil {
		return state
	}
	state = &detectorState{}
	invocation.SetState(stateKey, state)
	return state
}

func (p *toolLoopWarningPlugin) afterAgent(
	_ context.Context,
	args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	args.Invocation.DeleteState(stateKey)
	return nil, nil
}
