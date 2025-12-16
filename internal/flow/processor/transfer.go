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
		// Send error event.
		agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: target agent '"+targetAgentName+"' not found",
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

	// Send transfer event.
	if err := agent.EmitEvent(ctx, invocation, ch, transferEvent); err != nil {
		return
	}

	// Create new invocation for the target agent.
	// Do NOT propagate EndInvocation from the coordinator.
	// end_invocation is intended to end the current (parent) invocation
	// after transfer, not the target agent's invocation.
	targetInvocation := invocation.Clone(
		agent.WithInvocationAgent(targetAgent),
	)

	// Set the message for the target agent.
	if transferInfo.Message != "" {
		targetInvocation.Message = model.Message{
			Role:    model.RoleUser,
			Content: transferInfo.Message,
		}
		// Always emit a transfer message echo for visibility and traceability.
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
	targetEventChan, err := targetAgent.Run(targetCtx, targetInvocation)
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

	// Forward all events from the target agent.
	for targetEvent := range targetEventChan {
		if err := event.EmitEvent(ctx, ch, targetEvent); err != nil {
			return
		}
		log.DebugfContext(
			ctx,
			"Transfer response processor: forwarded event from "+
				"target agent %s",
			targetAgent.Info().Name,
		)
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
