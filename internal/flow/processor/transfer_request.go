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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TransferRequestProcessor performs agent transfer before any LLM call when a transfer has been requested.
// This prevents unnecessary extra LLM calls and ensures event authors are correct.
type TransferRequestProcessor struct{}

// NewTransferRequestProcessor creates a new transfer request processor.
func NewTransferRequestProcessor() *TransferRequestProcessor { return &TransferRequestProcessor{} }

// ProcessRequest checks for pending transfer and performs the handoff immediately, forwarding all target agent events.
func (p *TransferRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	_ *model.Request,
	ch chan<- *event.Event,
) {
	if invocation == nil || invocation.TransferInfo == nil {
		return
	}

	transferInfo := invocation.TransferInfo
	targetAgentName := transferInfo.TargetAgentName

	// Resolve target agent
	var targetAgent agent.Agent
	if invocation.Agent != nil {
		targetAgent = invocation.Agent.FindSubAgent(targetAgentName)
	}

	log.Debugf("TransferRequestProcessor: pre-LLM transfer detected to '%s' (agent=%s, invocation=%s)",
		targetAgentName, invocation.AgentName, invocation.InvocationID)

	if targetAgent == nil {
		log.Errorf("Target agent '%s' not found in sub-agents (pre-LLM transfer)", targetAgentName)
		// Emit error and end the invocation to avoid a redundant LLM call.
		errEvt := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: target agent '"+targetAgentName+"' not found",
		)
		select {
		case ch <- errEvt:
		case <-ctx.Done():
		}
		invocation.TransferInfo = nil
		invocation.EndInvocation = true
		return
	}

	// Notify about the handoff.
	transferEvent := event.New(invocation.InvocationID, invocation.AgentName)
	transferEvent.Object = model.ObjectTypeTransfer
	transferEvent.Response = &model.Response{
		ID:        "transfer-pre-llm",
		Object:    model.ObjectTypeTransfer,
		Created:   time.Now().Unix(),
		Model:     "",
		Timestamp: time.Now(),
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{Role: model.RoleAssistant,
				Content: "Transferring control to agent: " + targetAgent.Info().Name,
			},
		}},
	}
	select {
	case ch <- transferEvent:
	case <-ctx.Done():
		return
	}

	// Build target invocation.
	targetInvocation := &agent.Invocation{
		Agent:                targetAgent,
		AgentName:            targetAgent.Info().Name,
		InvocationID:         invocation.InvocationID,
		EndInvocation:        transferInfo.EndInvocation,
		Session:              invocation.Session,
		Model:                invocation.Model,
		EventCompletionCh:    invocation.EventCompletionCh,
		RunOptions:           invocation.RunOptions,
		TransferInfo:         nil,
		AgentCallbacks:       invocation.AgentCallbacks,
		ModelCallbacks:       invocation.ModelCallbacks,
		ToolCallbacks:        invocation.ToolCallbacks,
		StructuredOutput:     invocation.StructuredOutput,
		StructuredOutputType: invocation.StructuredOutputType,
		ArtifactService:      invocation.ArtifactService,
	}

	if transferInfo.Message != "" {
		targetInvocation.Message = model.Message{Role: model.RoleUser, Content: transferInfo.Message}
	} else {
		targetInvocation.Message = invocation.Message
	}

	// Execute target agent with proper invocation in context so tools can read it.
	log.Debugf("TransferRequestProcessor: starting target agent '%s' (pre-LLM)", targetAgent.Info().Name)
	targetCtx := agent.NewInvocationContext(ctx, targetInvocation)
	targetEventCh, err := targetAgent.Run(targetCtx, targetInvocation)
	if err != nil {
		log.Errorf("Failed to run target agent '%s' (pre-LLM transfer): %v", targetAgent.Info().Name, err)
		errEvt := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: "+err.Error(),
		)
		select {
		case ch <- errEvt:
		case <-ctx.Done():
		}
		// End the original invocation regardless to avoid proceeding with LLM.
		invocation.TransferInfo = nil
		invocation.EndInvocation = true
		return
	}

	// Forward target agent events
	for evt := range targetEventCh {
		select {
		case ch <- evt:
		case <-ctx.Done():
			return
		}
	}

	// Clear transfer and end the original invocation to prevent any further LLM call.
	log.Debugf("TransferRequestProcessor: target agent '%s' completed; ending original invocation",
		targetAgent.Info().Name)
	invocation.TransferInfo = nil
	invocation.EndInvocation = true
}
