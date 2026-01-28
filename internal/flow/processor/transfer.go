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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	swarmTeamNameKey          = "swarm_team_name"
	swarmActiveAgentKeyPrefix = "swarm_active_agent:"
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

	// Forward all events from the target agent.
	for targetEvent := range targetEventChan {
		if targetEvent != nil && targetEvent.Response != nil && targetEvent.Response.Done {
			p.saveActiveAgent(ctx, invocation, targetAgent, targetEvent)
		}
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

// saveActiveAgent saves the target agent to session state for Swarm cross-request transfer
// by attaching a StateDelta to the final response event.
func (p *TransferResponseProcessor) saveActiveAgent(
	ctx context.Context,
	invocation *agent.Invocation,
	targetAgent agent.Agent,
	targetEvent *event.Event,
) {
	if invocation == nil || invocation.Session == nil || targetEvent == nil {
		return
	}

	// Check if this session belongs to a Swarm Team with cross-request transfer enabled.
	// In Swarm mode with cross-request transfer, Team.runSwarm() sets SwarmTeamNameKey in session state.
	// This works for both direct Team transfers and member-to-member transfers.
	teamNameBytes, ok := invocation.Session.GetState(swarmTeamNameKey)
	if !ok || len(teamNameBytes) == 0 {
		// Not a Swarm team session with cross-request transfer enabled, skip.
		return
	}
	teamName := string(teamNameBytes)

	if targetEvent.StateDelta == nil {
		targetEvent.StateDelta = make(map[string][]byte)
	}
	// Save the target agent name for cross-request transfer.
	// Next user message will start from this agent.
	targetEvent.StateDelta[swarmActiveAgentKey(teamName)] = []byte(targetAgent.Info().Name)
}

func swarmActiveAgentKey(teamName string) string {
	if teamName == "" {
		return swarmActiveAgentKeyPrefix
	}
	return swarmActiveAgentKeyPrefix + teamName
}
