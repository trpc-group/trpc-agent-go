//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package processor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TransferResponseProcessor handles agent transfer operations after LLM responses.
type TransferResponseProcessor struct{}

// NewTransferResponseProcessor creates a new transfer response processor.
func NewTransferResponseProcessor() *TransferResponseProcessor {
	return &TransferResponseProcessor{}
}

// ProcessResponse implements the flow.ResponseProcessor interface.
// It checks for transfer requests and handles agent handoffs by actually calling
// the target agent's Run method.
func (p *TransferResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if rsp == nil {
		log.Errorf("Transfer response processor: response is nil")
		return
	}

	log.Debugf("Transfer response processor: processing response for agent %s", invocation.AgentName)

	// Check if there's a pending transfer in the invocation.
	if invocation.TransferInfo == nil {
		// No transfer requested, continue normally.
		return
	}

	transferInfo := invocation.TransferInfo
	targetAgentName := transferInfo.TargetAgentName

	// Look up the target agent from the current agent's sub-agents.
	var targetAgent agent.Agent
	if invocation.Agent != nil {
		targetAgent = invocation.Agent.FindSubAgent(targetAgentName)
	}

	if targetAgent == nil {
		log.Errorf("Target agent '%s' not found in sub-agents", targetAgentName)
		// Send error event.
		errorEvent := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: target agent '"+targetAgentName+"' not found",
		)
		select {
		case ch <- errorEvent:
		case <-ctx.Done():
		}
		return
	}

	// Create transfer event to notify about the handoff.
	transferEvent := event.New(invocation.InvocationID, invocation.AgentName)
	transferEvent.Object = model.ObjectTypeTransfer
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

	// Send transfer event.
	select {
	case ch <- transferEvent:
		log.Debugf("Transfer response processor: sent transfer event")
	case <-ctx.Done():
		log.Debugf("Transfer response processor: context cancelled")
		return
	}

	// Create new invocation for the target agent.
	targetInvocation := &agent.Invocation{
		Agent:             targetAgent,
		AgentName:         targetAgent.Info().Name,
		InvocationID:      invocation.InvocationID, // Keep same invocation ID for continuity
		EndInvocation:     transferInfo.EndInvocation,
		Session:           invocation.Session,
		Model:             invocation.Model,
		EventCompletionCh: invocation.EventCompletionCh,
		RunOptions:        invocation.RunOptions,
		TransferInfo:      nil, // Clear transfer info for target agent
	}

	// Set the message for the target agent.
	if transferInfo.Message != "" {
		targetInvocation.Message = model.Message{
			Role:    model.RoleUser,
			Content: transferInfo.Message,
		}
	} else {
		// Use the original message if no specific message for target agent.
		targetInvocation.Message = invocation.Message
	}

	// Actually call the target agent's Run method.
	targetEventChan, err := targetAgent.Run(ctx, targetInvocation)
	if err != nil {
		log.Errorf("Failed to run target agent '%s': %v", targetAgent.Info().Name, err)
		// Send error event.
		errorEvent := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: "+err.Error(),
		)
		select {
		case ch <- errorEvent:
		case <-ctx.Done():
		}
		return
	}

	// Forward all events from the target agent.
	for targetEvent := range targetEventChan {
		select {
		case ch <- targetEvent:
			log.Debugf("Transfer response processor: forwarded event from target agent %s", targetAgent.Info().Name)
		case <-ctx.Done():
			return
		}
	}

	// Clear the transfer info from the original invocation.
	invocation.TransferInfo = nil

	// Update the original invocation to reflect the transfer.
	invocation.Agent = targetAgent
	invocation.AgentName = targetAgent.Info().Name
	// Always end the original invocation after transfer.
	invocation.EndInvocation = true
}
