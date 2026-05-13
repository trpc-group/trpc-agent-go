//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itransfer "trpc.group/trpc-go/trpc-agent-go/internal/transfer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type swarmRuntime struct {
	mu           sync.Mutex
	cfg          SwarmConfig
	inputBuilder SwarmHandoffInputBuilder
	handoffs     int
	recent       []string
}

func (sr *swarmRuntime) OnTransfer(
	_ context.Context,
	fromAgent string,
	toAgent string,
) (time.Duration, error) {
	_ = fromAgent

	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.handoffs++
	if sr.cfg.MaxHandoffs > 0 && sr.handoffs > sr.cfg.MaxHandoffs {
		return 0, fmt.Errorf(
			"max handoffs exceeded: %d",
			sr.cfg.MaxHandoffs,
		)
	}

	window := sr.cfg.RepetitiveHandoffWindow
	minUnique := sr.cfg.RepetitiveHandoffMinUnique
	if window > 0 && minUnique > 0 {
		sr.recent = append(sr.recent, toAgent)
		if len(sr.recent) > window {
			sr.recent = sr.recent[len(sr.recent)-window:]
		}
		if len(sr.recent) == window && uniqueCount(sr.recent) < minUnique {
			return 0, errRepetitiveHandoff
		}
	}

	return sr.cfg.NodeTimeout, nil
}

func (sr *swarmRuntime) CustomizeTransferInvocation(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
) error {
	if target == nil || sr.inputBuilder == nil {
		return nil
	}
	transferMessage := target.Message.Content
	if rawTransferMessage, ok := itransfer.TransferMessageFromContext(ctx); ok {
		transferMessage = rawTransferMessage
	}
	msg, err := sr.inputBuilder(ctx, SwarmHandoffInputArgs{
		FromAgentName:   sourceAgentName(source),
		ToAgentName:     target.AgentName,
		RootInput:       rootMessage(source),
		ParentInput:     sourceMessage(source),
		TransferMessage: transferMessage,
	})
	if err != nil {
		return err
	}
	target.Message = normalizeHandoffInputMessage(msg)
	return nil
}

func sourceAgentName(source *agent.Invocation) string {
	if source == nil {
		return ""
	}
	return source.AgentName
}

func sourceMessage(source *agent.Invocation) model.Message {
	if source == nil {
		return model.Message{}
	}
	return source.Message
}

func rootMessage(source *agent.Invocation) model.Message {
	msg := sourceMessage(source)
	for current := source; current != nil; current = current.GetParentInvocation() {
		if model.HasPayload(current.Message) {
			msg = current.Message
		}
	}
	return msg
}

func normalizeHandoffInputMessage(msg model.Message) model.Message {
	if msg.Role == "" && model.HasPayload(msg) {
		msg.Role = model.RoleUser
	}
	return msg
}

func ensureSwarmRuntime(
	inv *agent.Invocation,
	cfg SwarmConfig,
	inputBuilder SwarmHandoffInputBuilder,
) {
	if inv == nil {
		return
	}
	cloneRuntimeStateForSwarm(&inv.RunOptions)
	runtime := &swarmRuntime{cfg: cfg, inputBuilder: inputBuilder}
	installSwarmTransferController(&inv.RunOptions, runtime)
}

func cloneRuntimeStateForSwarm(opts *agent.RunOptions) {
	if opts == nil {
		return
	}
	cloned := make(map[string]any, len(opts.RuntimeState)+1)
	for key, value := range opts.RuntimeState {
		cloned[key] = value
	}
	opts.RuntimeState = cloned
}

var (
	errRepetitiveHandoff = errors.New("repetitive handoff detected")
)

func uniqueCount(values []string) int {
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		seen[v] = struct{}{}
	}
	return len(seen)
}

func installSwarmTransferController(opts *agent.RunOptions, next agent.TransferController) {
	if opts == nil || next == nil {
		return
	}
	if opts.RuntimeState == nil {
		opts.RuntimeState = make(map[string]any)
	}
	existing, _ := agent.GetRuntimeStateValue[agent.TransferController](
		opts,
		agent.RuntimeStateKeyTransferController,
	)
	opts.RuntimeState[agent.RuntimeStateKeyTransferController] = composeTransferControllers(
		stripSwarmTransferControllers(existing),
		next,
	)
}

func stripSwarmTransferControllers(controller agent.TransferController) agent.TransferController {
	switch c := controller.(type) {
	case nil:
		return nil
	case *swarmRuntime:
		return nil
	case chainedTransferController:
		return composeTransferControllers(
			stripSwarmTransferControllers(c.first),
			stripSwarmTransferControllers(c.second),
		)
	default:
		return controller
	}
}

func composeTransferControllers(
	first agent.TransferController,
	second agent.TransferController,
) agent.TransferController {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return chainedTransferController{first: first, second: second}
}

type chainedTransferController struct {
	first  agent.TransferController
	second agent.TransferController
}

func (c chainedTransferController) OnTransfer(
	ctx context.Context,
	fromAgent string,
	toAgent string,
) (time.Duration, error) {
	firstTimeout, err := c.first.OnTransfer(ctx, fromAgent, toAgent)
	if err != nil {
		return 0, err
	}
	secondTimeout, err := c.second.OnTransfer(ctx, fromAgent, toAgent)
	if err != nil {
		return 0, err
	}
	return tighterTimeout(firstTimeout, secondTimeout), nil
}

func (c chainedTransferController) CustomizeTransferInvocation(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
) error {
	if first, ok := c.first.(itransfer.InvocationCustomizer); ok && first != nil {
		if err := first.CustomizeTransferInvocation(ctx, source, target); err != nil {
			return err
		}
	}
	if second, ok := c.second.(itransfer.InvocationCustomizer); ok && second != nil {
		return second.CustomizeTransferInvocation(ctx, source, target)
	}
	return nil
}

func tighterTimeout(a time.Duration, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}
