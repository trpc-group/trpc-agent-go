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

// TransferTag is the tag for transfer events.
const TransferTag = "transfer"

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

	log.Debugf("Transfer response processor: processing response for agent %s", invocation.AgentName)

	// Check if there's a pending transfer in the invocation.
	if invocation.TransferInfo == nil {
		// No transfer requested, continue normally.
		return
	}

	transferInfo := invocation.TransferInfo
	targetAgentName := transferInfo.TargetAgentName

	// Ensure the transfer tool.response has been persisted before proceeding.
	if err := p.waitForEventPersistence(ctx, invocation, transferInfo.ToolResponseEventID); err != nil {
		log.Errorf("Transfer response processor: transfer tool response not persisted: %v", err)
		agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: waiting for tool response persistence timed out",
		))
		return
	}

	// Look up the target agent from the current agent's sub-agents.
	var targetAgent agent.Agent
	if invocation.Agent != nil {
		targetAgent = invocation.Agent.FindSubAgent(targetAgentName)
	}

	if targetAgent == nil {
		log.Errorf("Target agent '%s' not found in sub-agents", targetAgentName)
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
		event.WithTag(TransferTag),
	)
	transferEvent.RequiresCompletion = true
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
	agentNoticeKey := agent.GetAppendEventNoticeKey(transferEvent.ID)
	invocation.AddNoticeChannel(ctx, agentNoticeKey)

	// Send transfer event.
	if err := agent.EmitEvent(ctx, invocation, ch, transferEvent); err != nil {
		return
	}
	if err := p.waitForEventPersistence(ctx, invocation, transferEvent.ID); err != nil {
		log.Errorf("Transfer response processor: transfer event not persisted: %v", err)
		agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			"Transfer failed: waiting for transfer event persistence timed out",
		))
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
		echoEvent := event.NewResponseEvent(
			targetInvocation.InvocationID,
			targetAgent.Info().Name,
			&model.Response{Choices: []model.Choice{{Message: targetInvocation.Message}}},
			event.WithTag(TransferTag),
		)
		echoEvent.RequiresCompletion = true
		echoNoticeKey := agent.GetAppendEventNoticeKey(echoEvent.ID)
		invocation.AddNoticeChannel(ctx, echoNoticeKey)
		if err := agent.EmitEvent(ctx, targetInvocation, ch, echoEvent); err != nil {
			return
		}
		if err := p.waitForEventPersistence(ctx, invocation, echoEvent.ID); err != nil {
			log.Errorf("Transfer response processor: transfer echo not persisted: %v", err)
			agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
				invocation.InvocationID,
				invocation.AgentName,
				model.ErrorTypeFlowError,
				"Transfer failed: waiting for transfer echo persistence timed out",
			))
			return
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
		log.Debugf("Transfer response processor: forwarded event from target agent %s", targetAgent.Info().Name)
	}

	// Clear the transfer info and end the original invocation to stop further LLM calls.
	// Do NOT mutate Agent/AgentName here to avoid author mismatches for any in-flight LLM stream.
	log.Debugf("Transfer response processor: target agent '%s' completed; ending original invocation", targetAgent.Info().Name)
	invocation.TransferInfo = nil
	invocation.EndInvocation = p.endInvocationAfterTransfer
}

// waitForEventPersistence waits for the runner to append the specified event to the session.
func (p *TransferResponseProcessor) waitForEventPersistence(
	ctx context.Context, inv *agent.Invocation, eventID string,
) error {
	if inv == nil || eventID == "" {
		return nil
	}
	timeout := agent.WaitNoticeWithoutTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return ctx.Err()
		}
	}
	return inv.AddNoticeChannelAndWait(
		ctx,
		agent.GetAppendEventNoticeKey(eventID),
		timeout,
	)
}
