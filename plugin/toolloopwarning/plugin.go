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
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/toolresultround"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const (
	pluginName    = "tool_loop_warning"
	stateKey      = "plugin:toolloopwarning"
	ownedQueueKey = "plugin:toolloopwarning:owned_queue"
	warningSource = "plugin/toolloopwarning"
	warning       = "The same tool-call loop has repeated. Please change your approach or stop calling the same tools with the same inputs."
)

type toolLoopWarningPlugin struct {
	stateInitMu sync.Mutex
	invocations map[string]invocationDetector
}

type invocationDetector struct {
	invocation *agent.Invocation
	state      *detectorState
}

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
	r.AfterEvent(p.afterEvent)
	r.AfterAgent(p.afterAgent)
}

func (p *toolLoopWarningPlugin) beforeAgent(
	_ context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	ownedQueue := false
	if !steer.IsAttached(args.Invocation) {
		steer.Attach(args.Invocation, steer.NewQueue())
		args.Invocation.SetState(ownedQueueKey, true)
		ownedQueue = true
	}
	p.stateInitMu.Lock()
	defer p.stateInitMu.Unlock()
	state := &detectorState{}
	args.Invocation.SetState(stateKey, state)
	if !ownedQueue {
		args.Invocation.DeleteState(ownedQueueKey)
	}
	p.rememberInvocationLocked(args.Invocation, state)
	return nil, nil
}

func (p *toolLoopWarningPlugin) afterToolMessages(
	_ context.Context,
	args *plugin.AfterToolMessagesArgs,
) (*plugin.AfterToolMessagesResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	state := p.detectorStateFor(args.Invocation)
	if args.ToolResultEvent == nil ||
		!state.stageEvent(args.ToolResultEvent.ID, args.ToolCalls) {
		state.reset()
	}
	return nil, nil
}

func (p *toolLoopWarningPlugin) afterEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	ev *event.Event,
) {
	if p == nil || invocation == nil || ev == nil {
		return
	}
	target, state := p.detectorForEvent(invocation, ev)
	if state == nil || target == nil {
		return
	}
	if isConsumedQueuedUserMessage(ev) {
		state.reset()
		return
	}
	warn, staged := state.observeStagedEvent(
		ev.ID,
		toolResultMessagesFromEvent(ev),
	)
	if staged {
		if warn && !steer.EnqueueWithSource(
			target,
			model.NewUserMessage(warning),
			warningSource,
		) {
			log.DebugfContext(
				ctx,
				"[%s] skip warning because no open user-message queue is attached",
				pluginName,
			)
		}
		return
	}
	if toolresultround.HasMarker(ev) {
		state.reset()
	}
}

func (p *toolLoopWarningPlugin) detectorForEvent(
	invocation *agent.Invocation,
	ev *event.Event,
) (*agent.Invocation, *detectorState) {
	if invocation == nil || ev == nil {
		return nil, nil
	}
	if ev.InvocationID == "" || ev.InvocationID == invocation.InvocationID {
		state, ok := agent.GetStateValue[*detectorState](invocation, stateKey)
		if !ok || state == nil {
			return nil, nil
		}
		return invocation, state
	}
	p.stateInitMu.Lock()
	defer p.stateInitMu.Unlock()
	entry, ok := p.invocations[ev.InvocationID]
	if !ok {
		return nil, nil
	}
	return entry.invocation, entry.state
}

func (p *toolLoopWarningPlugin) rememberInvocationLocked(
	invocation *agent.Invocation,
	state *detectorState,
) {
	if invocation == nil || state == nil || invocation.InvocationID == "" {
		return
	}
	if p.invocations == nil {
		p.invocations = make(map[string]invocationDetector)
	}
	p.invocations[invocation.InvocationID] = invocationDetector{
		invocation: invocation,
		state:      state,
	}
}

func isConsumedQueuedUserMessage(ev *event.Event) bool {
	metadata, ok, err := event.GetExtension[steer.QueuedUserMessageMetadata](
		ev,
		steer.ExtensionKeyQueuedUserMessage,
	)
	return err == nil && ok &&
		metadata.Status == steer.QueuedUserMessageStatusConsumed
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
		p.stateInitMu.Lock()
		p.rememberInvocationLocked(invocation, state)
		p.stateInitMu.Unlock()
		return state
	}
	p.stateInitMu.Lock()
	defer p.stateInitMu.Unlock()
	state, ok = agent.GetStateValue[*detectorState](invocation, stateKey)
	if ok && state != nil {
		p.rememberInvocationLocked(invocation, state)
		return state
	}
	state = &detectorState{}
	invocation.SetState(stateKey, state)
	p.rememberInvocationLocked(invocation, state)
	return state
}

func (p *toolLoopWarningPlugin) afterAgent(
	_ context.Context,
	args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
	if p == nil || args == nil || args.Invocation == nil {
		return nil, nil
	}
	ownedQueue, _ := agent.GetStateValue[bool](args.Invocation, ownedQueueKey)
	p.stateInitMu.Lock()
	if args.Invocation.InvocationID != "" {
		delete(p.invocations, args.Invocation.InvocationID)
	}
	p.stateInitMu.Unlock()
	args.Invocation.DeleteState(stateKey)
	args.Invocation.DeleteState(ownedQueueKey)
	if ownedQueue {
		steer.Clear(args.Invocation)
	}
	return nil, nil
}
