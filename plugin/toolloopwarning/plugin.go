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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/finalevent"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const (
	pluginName     = "tool_loop_warning"
	stateKey       = "plugin:toolloopwarning"
	warningSource  = "plugin/toolloopwarning"
	defaultWarning = "The same tool-call loop has repeated. Please change your approach or stop calling the same tools with the same inputs."
)

type toolLoopWarningPlugin struct {
	stateInitMu       sync.Mutex
	warning           string
	excludedToolNames map[string]struct{}
}

// New returns an opt-in plugin that persists a synthetic user-role instruction
// when two consecutive complete tool rounds are identical. It warns once per
// unchanged streak and never stops or retries the invocation.
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
	r.AfterToolMessages(p.afterToolMessages)
}

func (p *toolLoopWarningPlugin) beforeAgent(
	_ context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	state := &detectorState{}
	args.Invocation.SetState(stateKey, state)
	steer.RegisterConsumptionObserver(
		args.Invocation,
		pluginName,
		func(message steer.QueuedMessage) {
			if message.Source != warningSource {
				state.reset()
			}
		},
	)
	return nil, nil
}

func (p *toolLoopWarningPlugin) afterToolMessages(
	_ context.Context,
	args *plugin.AfterToolMessagesArgs,
) (*plugin.AfterToolMessagesResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	invocation := args.Invocation
	state := p.detectorStateFor(invocation)
	if args.ToolResultEvent == nil || args.ToolResultEvent.ID == "" ||
		len(args.ToolCalls) == 0 || p.containsExcludedTool(args.ToolCalls) {
		state.reset()
		return nil, nil
	}
	toolCalls := cloneToolCalls(args.ToolCalls)
	registered := finalevent.Register(
		invocation,
		args.ToolResultEvent.ID,
		func(ctx context.Context, ev *event.Event) {
			if !state.observeToolMessages(
				toolCalls,
				toolResultMessagesFromEvent(ev),
			) {
				return
			}
			if !steer.IsAttached(invocation) {
				steer.Attach(invocation, steer.NewQueue())
			}
			if !steer.EnqueueWithSource(
				invocation,
				model.NewUserMessage(p.warning),
				warningSource,
			) {
				log.DebugfContext(
					ctx,
					"[%s] skip warning because no open user-message queue is attached",
					pluginName,
				)
			}
		},
	)
	if !registered {
		state.reset()
	}
	return nil, nil
}

func (p *toolLoopWarningPlugin) containsExcludedTool(
	toolCalls []model.ToolCall,
) bool {
	for _, toolCall := range toolCalls {
		if _, excluded := p.excludedToolNames[toolCall.Function.Name]; excluded {
			return true
		}
	}
	return false
}

func toolResultMessagesFromEvent(ev *event.Event) []model.Message {
	if ev == nil || ev.Response == nil ||
		ev.Response.Object != model.ObjectTypeToolResponse {
		return nil
	}
	messages := make([]model.Message, 0, len(ev.Response.Choices))
	for _, choice := range ev.Response.Choices {
		message := choice.Message
		if message.ToolID == "" && choice.Delta.ToolID != "" {
			message = choice.Delta
		}
		if message.ToolID == "" || !model.HasPayload(message) {
			continue
		}
		messages = append(messages, message)
	}
	return messages
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
