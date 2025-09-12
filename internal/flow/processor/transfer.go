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
	req *model.Request,
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
	targetInvocation := invocation.Clone(
		agent.WithInvocationAgent(targetAgent),
		agent.WithInvocationEndInvocation(transferInfo.EndInvocation),
		agent.WithInvocationBranch(invocation.Branch),
	)

	// Set the message for the target agent.
	if transferInfo.Message != "" {
		targetInvocation.Message = model.Message{
			Role:    model.RoleUser,
			Content: transferInfo.Message,
		}
	}

	// Actually call the target agent's Run method with the target invocation in context
	// so tools can correctly access agent.InvocationFromContext(ctx).
	log.Debugf("Transfer response processor: starting target agent '%s'", targetAgent.Info().Name)
	targetCtx := agent.NewInvocationContext(ctx, targetInvocation)
	targetEventChan, err := targetAgent.Run(targetCtx, targetInvocation)
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

	// Clear the transfer info and end the original invocation to stop further LLM calls.
	// Do NOT mutate Agent/AgentName here to avoid author mismatches for any in-flight LLM stream.
	log.Debugf("Transfer response processor: target agent '%s' completed; ending original invocation", targetAgent.Info().Name)
	invocation.TransferInfo = nil
	invocation.EndInvocation = true
}
