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
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const (
	pluginName = "tool-loop-warning"
	stateKey   = "plugin:toolloopwarning"
	warning    = "The same tool-call loop has repeated. Please change your approach or stop calling the same tools with the same inputs."
)

type toolLoopWarningPlugin struct {
	detector detector
	mu       sync.Mutex
}

// New creates a plugin that warns the model once when an identical complete
// tool round repeats. The plugin is disabled unless installed on a Runner.
func New() plugin.Plugin {
	return &toolLoopWarningPlugin{}
}

// Name implements plugin.Plugin.
func (p *toolLoopWarningPlugin) Name() string {
	return pluginName
}

// Register implements plugin.Plugin.
func (p *toolLoopWarningPlugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeAgent(p.beforeAgent)
	r.AfterToolRound(p.afterToolRound)
	r.BeforeModel(p.beforeModel)
	r.AfterAgent(p.afterAgent)
}

func (p *toolLoopWarningPlugin) beforeAgent(
	_ context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	args.Invocation.DeleteState(stateKey)
	return nil, nil
}

func (p *toolLoopWarningPlugin) afterToolRound(
	_ context.Context,
	args *plugin.AfterToolRoundArgs,
) error {
	if p == nil || args == nil || args.Invocation == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state, _ := agent.GetStateValue[detectorState](args.Invocation, stateKey)
	state = p.detector.observe(
		state,
		args.ToolCallResponse,
		args.ToolResultMessages,
		args.Complete,
	)
	args.Invocation.SetState(stateKey, state)
	return nil
}

func (p *toolLoopWarningPlugin) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if p == nil || args == nil || args.Request == nil {
		return nil, nil
	}
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := agent.GetStateValue[detectorState](invocation, stateKey)
	if !ok || !state.Pending {
		return nil, nil
	}
	args.Request.Messages = append(args.Request.Messages, model.Message{
		Role:    model.RoleUser,
		Content: warning,
	})
	state.Pending = false
	invocation.SetState(stateKey, state)
	return nil, nil
}

func (p *toolLoopWarningPlugin) afterAgent(
	_ context.Context,
	args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	args.Invocation.DeleteState(stateKey)
	return nil, nil
}
