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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
	"trpc.group/trpc-go/trpc-agent-go/planner/builtin"
)

// PlanningRequestProcessor implements planning request processing logic.
type PlanningRequestProcessor struct {
	// Planner is the planner to use for generating planning instructions.
	Planner planner.Planner
}

// NewPlanningRequestProcessor creates a new planning request processor.
func NewPlanningRequestProcessor(p planner.Planner) *PlanningRequestProcessor {
	return &PlanningRequestProcessor{
		Planner: p,
	}
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It generates planning instructions and removes thought markers from requests.
func (p *PlanningRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.ErrorfContext(
			ctx,
			"Planning request processor: request is nil",
		)
		return
	}
	if p.Planner == nil {
		log.DebugContext(
			ctx,
			"Planning request processor: no planner configured",
		)
		return
	}

	log.DebugfContext(
		ctx,
		"Planning request processor: processing request for agent %s",
		invocation.AgentName,
	)

	// Apply thinking configuration for built-in planners.
	if builtinPlanner, ok := p.Planner.(*builtin.Planner); ok {
		// For built-in planners, just apply thinking config and return.
		_ = builtinPlanner.BuildPlanningInstruction(ctx, invocation, req)
		return
	}

	// Generate planning instruction.
	planningInstruction := p.Planner.BuildPlanningInstruction(ctx, invocation, req)
	if planningInstruction != "" {
		// Check if planning instruction already exists to avoid duplication.
		if !hasSystemMessage(req.Messages, planningInstruction) {
			instructionMsg := model.NewSystemMessage(planningInstruction)
			req.Messages = append([]model.Message{instructionMsg}, req.Messages...)
			log.DebugContext(
				ctx,
				"Planning request processor: added planning instruction",
			)
		}
	}

	if invocation == nil {
		return
	}

	log.DebugContext(
		ctx,
		"Planning request processor: sent preprocessing event",
	)

	if err := agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePreprocessingPlanning),
	)); err != nil {
		log.DebugContext(
			ctx,
			"Planning request processor: context cancelled",
		)
	}
}

// hasSystemMessage checks if a system message with the given content already exists.
// It compares only the first few characters of the content for performance reasons,
// as this is usually sufficient to determine content similarity.
func hasSystemMessage(messages []model.Message, content string) bool {
	// Maximum length of content prefix to compare for performance optimization.
	const maxContentPrefixLength = 100
	// Use content prefix for comparison to avoid performance issues with long content.
	contentPrefix := content[:min(maxContentPrefixLength, len(content))]
	for _, msg := range messages {
		if msg.Role == model.RoleSystem && strings.Contains(msg.Content, contentPrefix) {
			return true
		}
	}
	return false
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// PlanningResponseProcessor implements planning response processing logic.
type PlanningResponseProcessor struct {
	// Planner is the planner to use for processing planning responses.
	Planner planner.Planner
}

// NewPlanningResponseProcessor creates a new planning response processor.
func NewPlanningResponseProcessor(p planner.Planner) *PlanningResponseProcessor {
	return &PlanningResponseProcessor{
		Planner: p,
	}
}

// ProcessResponse implements the flow.ResponseProcessor interface.
// It processes planning responses using the configured planner.
func (p *PlanningResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation == nil || rsp == nil || rsp.IsPartial {
		return
	}
	if p.Planner == nil {
		log.DebugContext(
			ctx,
			"Planning response processor: no planner configured",
		)
		return
	}
	if len(rsp.Choices) == 0 {
		log.DebugContext(
			ctx,
			"Planning response processor: no choices in response",
		)
		return
	}

	log.DebugfContext(
		ctx,
		"Planning response processor: processing response for agent %s",
		invocation.AgentName,
	)

	// Process the response using the planner.
	processedResponse := p.Planner.ProcessPlanningResponse(ctx, invocation, rsp)
	if processedResponse != nil {
		// Update the original response with processed content.
		*rsp = *processedResponse
		log.DebugContext(
			ctx,
			"Planning response processor: processed response successfully",
		)
	}

	log.DebugContext(
		ctx,
		"Planning response processor: sent postprocessing event",
	)

	if err := agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePostprocessingPlanning),
	)); err != nil {
		log.DebugContext(
			ctx,
			"Planning response processor: context cancelled",
		)
	}
}
