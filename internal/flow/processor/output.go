//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// OutputResponseProcessor processes final responses and handles output_key and output_schema functionality.
type OutputResponseProcessor struct {
	outputKey    string
	outputSchema map[string]interface{}
}

// NewOutputResponseProcessor creates a new instance of OutputResponseProcessor.
func NewOutputResponseProcessor(
	outputKey string,
	outputSchema map[string]interface{},
) *OutputResponseProcessor {
	return &OutputResponseProcessor{
		outputKey:    outputKey,
		outputSchema: outputSchema,
	}
}

// ProcessResponse processes the model response and handles output_key and output_schema functionality.
// This mimics the behavior of adk-python's output processing using event.actions.state_delta pattern.
func (p *OutputResponseProcessor) ProcessResponse(
	ctx context.Context, invocation *agent.Invocation, rsp *model.Response, ch chan<- *event.Event) {

	// Only process complete (non-partial) responses.
	if rsp.IsPartial {
		return
	}

	// Check if output_key or output_schema is configured.
	if p.outputKey == "" && p.outputSchema == nil {
		return
	}

	// Extract text content from the response.
	if len(rsp.Choices) == 0 || rsp.Choices[0].Message.Content == "" {
		return
	}

	content := rsp.Choices[0].Message.Content

	// Handle output_key functionality.
	if p.outputKey != "" {
		result := content

		// If output_schema is also present, validate and parse as JSON.
		if p.outputSchema != nil {
			// Skip empty or whitespace-only content.
			if strings.TrimSpace(content) == "" {
				return
			}

			// Validate JSON against schema (basic validation).
			var parsedJSON interface{}
			if err := json.Unmarshal([]byte(content), &parsedJSON); err != nil {
				log.Warnf("Failed to parse output as JSON for output_schema validation: %v", err)
				return
			}

			// Convert parsed JSON back to map for storage.
			result = content // Store the original JSON string.
		}

		// Create a state delta event instead of directly modifying session.
		stateDelta := map[string][]byte{
			p.outputKey: []byte(result),
		}

		// Create and emit an event with state delta for the runner to process.
		stateEvent := event.New(invocation.InvocationID, invocation.AgentName,
			event.WithObject(model.ObjectTypeStateUpdate),
			event.WithStateDelta(stateDelta),
		)
		stateEvent.RequiresCompletion = true

		// Send the state update event.
		select {
		case ch <- stateEvent:
			log.Debugf("Emitted state delta event with key '%s': %s", p.outputKey, result)
		case <-ctx.Done():
			return
		}
		select {
		case completionID := <-invocation.EventCompletionCh:
			if completionID == stateEvent.ID {
				log.Debugf("State delta event %s completed, proceeding with next LLM call", completionID)
			}
		case <-ctx.Done():
			return
		}
	}
}
