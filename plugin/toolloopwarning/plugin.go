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
	pluginName     = "tool_loop_warning"
	stateKey       = "plugin:toolloopwarning"
	defaultWarning = "The same tool-call loop has repeated. Please change your approach or stop calling the same tools with the same inputs."
)

type toolLoopWarningPlugin struct {
	warning           string
	excludedToolNames map[string]struct{}
}

type detectorState struct {
	mu sync.Mutex

	seenFirstRequest bool
	// Flow creates a new request for each LLM cycle, while model retries
	// re-enter callbacks with the same request pointer. Retaining the first
	// pointer keeps retries of the initial request inside the initial-request
	// skip boundary.
	firstRequest *model.Request
}

// New returns an opt-in plugin that adds a temporary user-role instruction to
// each eligible model request when its two trailing complete tool rounds are
// identical. The instruction is request-local: it is not appended to session
// history, and the plugin never stops or retries the invocation.
func New(opts ...Option) plugin.Plugin {
	o := newOptions(opts...)
	return &toolLoopWarningPlugin{
		warning:           o.warning,
		excludedToolNames: o.excludedToolNames,
	}
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
	args.Invocation.SetState(stateKey, &detectorState{})
	return nil, nil
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

	state, ok := agent.GetStateValue[*detectorState](invocation, stateKey)
	if !ok || state == nil {
		return nil, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	if !state.seenFirstRequest {
		state.seenFirstRequest = true
		state.firstRequest = args.Request
		return nil, nil
	}
	if state.firstRequest == args.Request {
		return nil, nil
	}
	state.firstRequest = nil

	if hasTrailingWarning(args.Request.Messages, p.warning) {
		return nil, nil
	}
	_, matched := matchingTrailingRoundFingerprint(
		args.Request.Messages,
		p.excludedToolNames,
	)
	if !matched {
		return nil, nil
	}
	args.Request.Messages = append(
		args.Request.Messages,
		model.NewUserMessage(p.warning),
	)
	return nil, nil
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

func hasTrailingWarning(messages []model.Message, warning string) bool {
	return len(messages) > 0 &&
		isWarningMessage(messages[len(messages)-1], warning)
}

func isWarningMessage(message model.Message, warning string) bool {
	if message.Role != model.RoleUser ||
		len(message.ContentParts) > 0 ||
		message.ToolID != "" ||
		message.ToolName != "" ||
		len(message.ToolCalls) > 0 ||
		message.ReasoningContent != "" ||
		message.ReasoningSignature != "" {
		return false
	}
	return message.Content == warning
}
