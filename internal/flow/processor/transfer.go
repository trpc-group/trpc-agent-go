//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
	itransfer "trpc.group/trpc-go/trpc-agent-go/internal/transfer"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	swarmTeamNameKey    = "swarm_team_name"
	swarmTraceNodeIDKey = "__swarm_trace_node_id__"
)

// TransferResponseProcessor handles agent transfer operations after LLM responses.
type TransferResponseProcessor struct {
	// endInvocationAfterTransfer controls whether to end the current agent invocation after transfer.
	// If true, the current agent will end the invocation after transfer, else the current agent will continue to run
	// when the transfer is complete. Defaults to true.
	endInvocationAfterTransfer bool
}

// NewTransferResponseProcessor creates a new transfer response processor.
func NewTransferResponseProcessor(endInvocation bool) *TransferResponseProcessor {
	return &TransferResponseProcessor{
		endInvocationAfterTransfer: endInvocation,
	}
}

// ProcessResponse implements the flow.ResponseProcessor interface.
// It checks for transfer requests and handles agent handoffs by actually calling
// the target agent's Run method.
func (p *TransferResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation == nil || rsp == nil || rsp.IsPartial {
		return
	}

	log.DebugfContext(
		ctx,
		"Transfer response processor: processing response for agent %s",
		invocation.AgentName,
	)

	// Check if there's a pending transfer in the invocation.
	if invocation.TransferInfo == nil {
		// No transfer requested, continue normally.
		return
	}

	transferInfo := invocation.TransferInfo
	targetAgentName := transferInfo.TargetAgentName
	var nodeTimeout time.Duration
	var transferCustomizer itransfer.InvocationCustomizer
	var transferCompletionHandler itransfer.CompletionObserver
	var transferTerminalErrorHandler itransfer.TerminalErrorObserver

	// Look up the target agent from the current agent's sub-agents.
	var targetAgent agent.Agent
	if invocation.Agent != nil {
		targetAgent = invocation.Agent.FindSubAgent(targetAgentName)
	}

	if targetAgent == nil {
		log.ErrorfContext(
			ctx,
			"Target agent '%s' not found in sub-agents",
			targetAgentName,
		)
		invocation.TransferInfo = nil
		// Send error event.
		agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: target agent '"+targetAgentName+"' not found",
		))
		return
	}

	if controller, ok := agent.GetRuntimeStateValue[agent.TransferController](
		&invocation.RunOptions,
		agent.RuntimeStateKeyTransferController,
	); ok && controller != nil {
		targetTimeout, err := controller.OnTransfer(
			ctx,
			invocation.AgentName,
			targetAgentName,
		)
		if err != nil {
			invocation.TransferInfo = nil
			agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
				invocation.InvocationID,
				invocation.AgentName,
				model.ErrorTypeFlowError,
				fmt.Sprintf(
					"Transfer rejected: %v",
					err,
				),
			))
			return
		}
		nodeTimeout = targetTimeout
		transferCustomizer = transferInvocationCustomizerFor(controller)
		transferCompletionHandler = transferCompletionHandlerFor(controller)
		transferTerminalErrorHandler = transferTerminalErrorHandlerFor(controller)
	}

	targetInvocation, targetMessageBeforeCustomize, err := prepareTransferTargetInvocation(
		ctx,
		invocation,
		targetAgent,
		transferInfo,
		transferCustomizer,
	)
	if err != nil {
		invocation.TransferInfo = nil
		agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			fmt.Sprintf("Transfer customization rejected: %v", err),
		))
		return
	}
	// Create transfer event to notify about the handoff.
	transferEvent := event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypeTransfer),
		event.WithTag(event.TransferTag),
	)
	transferEvent.Response = &model.Response{
		ID:        "transfer-" + rsp.ID,
		Object:    model.ObjectTypeTransfer,
		Created:   rsp.Created,
		Model:     rsp.Model,
		Timestamp: rsp.Timestamp,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Transferring control to agent: " + targetAgent.Info().Name,
				},
			},
		},
	}
	// Send transfer event after customization succeeds.
	if err := agent.EmitEvent(ctx, invocation, ch, transferEvent); err != nil {
		return
	}
	if shouldEmitTransferMessageEcho(
		transferInfo.Message != "",
		targetMessageBeforeCustomize,
		targetInvocation.Message,
	) {
		// Emit explicit transfer input for visibility and traceability.
		// Use tag so UIs can filter internal delegation messages without breaking event alignment.
		agent.EmitEvent(ctx, targetInvocation, ch, event.NewResponseEvent(
			targetInvocation.InvocationID,
			targetAgent.Info().Name,
			&model.Response{Choices: []model.Choice{{Message: targetInvocation.Message}}},
			event.WithTag(event.TransferTag),
		))
	}

	// Actually call the target agent's Run method with the target invocation in context
	// so tools can correctly access agent.InvocationFromContext(ctx).
	log.DebugfContext(
		ctx,
		"Transfer response processor: starting target agent '%s'",
		targetAgent.Info().Name,
	)
	targetCtx := agent.NewInvocationContext(ctx, targetInvocation)

	var runCtx context.Context = targetCtx
	if nodeTimeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, nodeTimeout)
		defer cancel()
	}

	targetEventChan, err := agent.RunWithPlugins(
		runCtx,
		targetInvocation,
		targetAgent,
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Failed to run target agent '%s': %v",
			targetAgent.Info().Name,
			err,
		)
		// Send error event.
		agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: "+err.Error(),
		))
		return
	}

	if !forwardTransferTargetEvents(
		ctx,
		invocation,
		targetInvocation,
		targetAgent,
		targetEventChan,
		ch,
		transferCompletionHandler,
		transferTerminalErrorHandler,
	) {
		return
	}

	// Clear the transfer info and end the original invocation to stop further LLM calls.
	// Do NOT mutate Agent/AgentName here to avoid author mismatches for any in-flight LLM stream.
	log.DebugfContext(
		ctx,
		"Transfer response processor: target agent '%s' completed; "+
			"ending original invocation",
		targetAgent.Info().Name,
	)
	invocation.TransferInfo = nil
	invocation.EndInvocation = p.endInvocationAfterTransfer
}

type transferForwardResult struct {
	completed       bool
	delegated       bool
	terminalErrored bool
}

func forwardTransferTargetEvents(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
	targetAgent agent.Agent,
	events <-chan *event.Event,
	out chan<- *event.Event,
	completionHandler itransfer.CompletionObserver,
	terminalErrorHandler itransfer.TerminalErrorObserver,
) bool {
	result := transferForwardResult{}
	for targetEvent := range events {
		result.observe(targetEvent, target.InvocationID)
		if result.shouldNotifyTerminalError(
			terminalErrorHandler,
			targetEvent,
			target.InvocationID,
		) {
			terminalErrorHandler.OnTransferTerminalError(
				ctx,
				source,
				target,
				targetEvent,
			)
		}
		if result.shouldNotifyCompletion(completionHandler, targetEvent, target.InvocationID) {
			result.completed = true
			completionHandler.OnTransferComplete(ctx, source, target, targetEvent)
		}
		if err := event.EmitEvent(ctx, out, targetEvent); err != nil {
			return false
		}
		log.DebugfContext(
			ctx,
			"Transfer response processor: forwarded event from "+
				"target agent %s",
			targetAgent.Info().Name,
		)
	}
	if completionHandler != nil && result.needsSyntheticCompletion() {
		completionHandler.OnTransferComplete(
			ctx,
			source,
			target,
			syntheticTransferCompletionEvent(target),
		)
	}
	return true
}

func (r *transferForwardResult) observe(evt *event.Event, invocationID string) {
	if isTransferDelegationEvent(evt) {
		r.delegated = true
	}
	if isTransferTerminalErrorEvent(evt, invocationID) {
		r.terminalErrored = true
	}
}

func (r transferForwardResult) shouldNotifyCompletion(
	handler itransfer.CompletionObserver,
	evt *event.Event,
	invocationID string,
) bool {
	return handler != nil &&
		evt != nil &&
		evt.InvocationID == invocationID &&
		evt.Response != nil &&
		!r.terminalErrored &&
		evt.Response.Done
}

func (r transferForwardResult) shouldNotifyTerminalError(
	handler itransfer.TerminalErrorObserver,
	evt *event.Event,
	invocationID string,
) bool {
	return handler != nil &&
		r.terminalErrored &&
		isTransferTerminalErrorEvent(evt, invocationID)
}

func (r transferForwardResult) needsSyntheticCompletion() bool {
	return !r.completed && !r.delegated && !r.terminalErrored
}

func prepareTransferTargetInvocation(
	ctx context.Context,
	invocation *agent.Invocation,
	targetAgent agent.Agent,
	transferInfo *agent.TransferInfo,
	customizer itransfer.InvocationCustomizer,
) (*agent.Invocation, model.Message, error) {
	// Do NOT propagate EndInvocation from the coordinator.
	// end_invocation is intended to end the current invocation.
	targetInvocation := invocation.Clone(
		agent.WithInvocationAgent(targetAgent),
		agent.WithInvocationTraceNodeID(
			transferTargetTraceNodeID(invocation, targetAgent),
		),
		agent.WithInvocationEntryPredecessorStepIDs(
			agent.NextExecutionTracePredecessors(invocation),
		),
		func(inv *agent.Invocation) {
			if surfaceRootNodeID := transferTargetSurfaceRootNodeID(invocation, targetAgent); surfaceRootNodeID != "" {
				agent.SetInvocationSurfaceRootNodeID(inv, surfaceRootNodeID)
			}
		},
	)
	if transferInfo.Message != "" {
		targetInvocation.Message = model.Message{
			Role:    model.RoleUser,
			Content: transferInfo.Message,
		}
	}
	beforeCustomize := targetInvocation.Message
	if customizer == nil {
		return targetInvocation, beforeCustomize, nil
	}
	customizeCtx := itransfer.ContextWithTransferMessage(ctx, transferInfo.Message)
	if err := customizer.CustomizeTransferInvocation(customizeCtx, invocation, targetInvocation); err != nil {
		return nil, model.Message{}, err
	}
	return targetInvocation, beforeCustomize, nil
}

func shouldEmitTransferMessageEcho(
	hasTransferMessage bool,
	beforeCustomize model.Message,
	afterCustomize model.Message,
) bool {
	if !model.HasPayload(afterCustomize) {
		return false
	}
	return hasTransferMessage || !reflect.DeepEqual(beforeCustomize, afterCustomize)
}

func transferTargetTraceNodeID(invocation *agent.Invocation, targetAgent agent.Agent) string {
	if invocation == nil || targetAgent == nil {
		return ""
	}
	if root := agent.InvocationTeamMemberTraceRoot(invocation); root != "" {
		return istructure.JoinNodeID(root, targetAgent.Info().Name)
	}
	if invocation.Session != nil {
		if traceRootBytes, ok := invocation.Session.GetState(swarmTraceNodeIDKey); ok && len(traceRootBytes) > 0 {
			return istructure.JoinNodeID(string(traceRootBytes), targetAgent.Info().Name)
		}
		if teamNameBytes, ok := invocation.Session.GetState(swarmTeamNameKey); ok && len(teamNameBytes) > 0 {
			if mountedRoot := parentTraceNodeID(agent.InvocationTraceNodeID(invocation)); mountedRoot != "" {
				return istructure.JoinNodeID(mountedRoot, targetAgent.Info().Name)
			}
			return istructure.JoinNodeID(istructure.JoinNodeID("", string(teamNameBytes)), targetAgent.Info().Name)
		}
	}
	return istructure.JoinNodeID(agent.InvocationTraceNodeID(invocation), targetAgent.Info().Name)
}

func transferTargetSurfaceRootNodeID(invocation *agent.Invocation, targetAgent agent.Agent) string {
	if invocation == nil || targetAgent == nil {
		return ""
	}
	if mountedRoot := parentTraceNodeID(agent.InvocationSurfaceRootNodeID(invocation)); mountedRoot != "" {
		return istructure.JoinNodeID(mountedRoot, targetAgent.Info().Name)
	}
	return transferTargetTraceNodeID(invocation, targetAgent)
}

func isTransferDelegationEvent(evt *event.Event) bool {
	if evt == nil {
		return false
	}
	return evt.Object == model.ObjectTypeTransfer || evt.ContainsTag(event.TransferTag)
}

func isTransferTerminalErrorEvent(evt *event.Event, invocationID string) bool {
	return evt != nil && evt.InvocationID == invocationID && evt.IsTerminalError()
}

func syntheticTransferCompletionEvent(invocation *agent.Invocation) *event.Event {
	invocationID := ""
	author := ""
	if invocation != nil {
		invocationID = invocation.InvocationID
		author = invocation.AgentName
	}
	evt := event.NewResponseEvent(invocationID, author, &model.Response{Done: true})
	itransfer.MarkSyntheticCompletionEvent(evt)
	return evt
}

func parentTraceNodeID(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	lastSlash := strings.LastIndex(nodeID, "/")
	if lastSlash <= 0 {
		return ""
	}
	return nodeID[:lastSlash]
}

func transferInvocationCustomizerFor(
	controller agent.TransferController,
) itransfer.InvocationCustomizer {
	customizer, ok := controller.(itransfer.InvocationCustomizer)
	if !ok {
		return nil
	}
	return customizer
}

func transferCompletionHandlerFor(
	controller agent.TransferController,
) itransfer.CompletionObserver {
	handler, ok := controller.(itransfer.CompletionObserver)
	if !ok {
		return nil
	}
	return handler
}

func transferTerminalErrorHandlerFor(
	controller agent.TransferController,
) itransfer.TerminalErrorObserver {
	handler, ok := controller.(itransfer.TerminalErrorObserver)
	if !ok {
		return nil
	}
	return handler
}
