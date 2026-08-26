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
	stateInitMu       sync.Mutex
	warning           string
	excludedToolNames map[string]struct{}
}

type detectorState struct {
	mu sync.Mutex

	seenFirstRequest  bool
	warnedFingerprint string

	pendingFingerprint   string
	pendingRequest       *model.Request
	pendingMessageIndex  int
	pendingBaselineCount int
}

// New returns an opt-in plugin that adds a temporary user-role instruction to
// the next model request when its two trailing complete tool rounds are
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
	r.AfterModel(p.afterModel)
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

	state := p.detectorStateFor(invocation)
	state.mu.Lock()
	defer state.mu.Unlock()

	if !state.seenFirstRequest {
		state.seenFirstRequest = true
		state.clearPending()
		return nil, nil
	}

	messages := state.messagesForDetection(args.Request, p.warning)
	fingerprint, matched := matchingTrailingRoundFingerprint(
		messages,
		p.excludedToolNames,
	)
	if !matched {
		state.rearm()
		return nil, nil
	}
	if state.warnedFingerprint == fingerprint {
		state.clearPending()
		return nil, nil
	}

	if state.pendingWarningStillPresent(args.Request, p.warning) {
		return nil, nil
	}
	state.pendingFingerprint = fingerprint
	state.pendingRequest = args.Request
	state.pendingMessageIndex = len(args.Request.Messages)
	state.pendingBaselineCount = countWarningMessages(
		args.Request.Messages,
		p.warning,
	)
	args.Request.Messages = append(
		args.Request.Messages,
		model.NewUserMessage(p.warning),
	)
	return nil, nil
}

func (p *toolLoopWarningPlugin) afterModel(
	ctx context.Context,
	args *model.AfterModelArgs,
) (*model.AfterModelResult, error) {
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
	if state.pendingRequest != args.Request {
		return nil, nil
	}
	if countWarningMessages(args.Request.Messages, p.warning) >
		state.pendingBaselineCount {
		state.warnedFingerprint = state.pendingFingerprint
	}
	state.clearPending()
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

func (p *toolLoopWarningPlugin) detectorStateFor(
	invocation *agent.Invocation,
) *detectorState {
	state, ok := agent.GetStateValue[*detectorState](invocation, stateKey)
	if ok && state != nil {
		return state
	}
	p.stateInitMu.Lock()
	defer p.stateInitMu.Unlock()
	state, ok = agent.GetStateValue[*detectorState](invocation, stateKey)
	if ok && state != nil {
		return state
	}
	state = &detectorState{}
	invocation.SetState(stateKey, state)
	return state
}

func (s *detectorState) messagesForDetection(
	request *model.Request,
	warning string,
) []model.Message {
	if s == nil || request == nil ||
		s.pendingRequest != request ||
		s.pendingMessageIndex < 0 ||
		s.pendingMessageIndex >= len(request.Messages) {
		return request.Messages
	}
	if !isWarningMessage(request.Messages[s.pendingMessageIndex], warning) {
		return request.Messages
	}
	messages := make([]model.Message, 0, len(request.Messages)-1)
	messages = append(messages, request.Messages[:s.pendingMessageIndex]...)
	messages = append(messages, request.Messages[s.pendingMessageIndex+1:]...)
	return messages
}

func (s *detectorState) pendingWarningStillPresent(
	request *model.Request,
	warning string,
) bool {
	return s != nil && request != nil &&
		s.pendingRequest == request &&
		s.pendingMessageIndex >= 0 &&
		s.pendingMessageIndex < len(request.Messages) &&
		isWarningMessage(request.Messages[s.pendingMessageIndex], warning)
}

func (s *detectorState) rearm() {
	s.warnedFingerprint = ""
	s.clearPending()
}

func (s *detectorState) clearPending() {
	s.pendingFingerprint = ""
	s.pendingRequest = nil
	s.pendingMessageIndex = -1
	s.pendingBaselineCount = 0
}

func countWarningMessages(messages []model.Message, warning string) int {
	count := 0
	for _, message := range messages {
		if isWarningMessage(message, warning) {
			count++
		}
	}
	return count
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
