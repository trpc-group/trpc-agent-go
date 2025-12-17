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
	"encoding/json"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// OutputResponseProcessor processes final responses and handles output_key and output_schema functionality.
type OutputResponseProcessor struct {
	outputKey    string
	outputSchema map[string]any
}

// NewOutputResponseProcessor creates a new instance of OutputResponseProcessor.
func NewOutputResponseProcessor(
	outputKey string,
	outputSchema map[string]any,
) *OutputResponseProcessor {
	return &OutputResponseProcessor{
		outputKey:    outputKey,
		outputSchema: outputSchema,
	}
}

// ProcessResponse processes the model response and handles output_key and output_schema functionality.
// This mimics the behavior of adk-python's output processing using event.actions.state_delta pattern.
func (p *OutputResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation == nil || rsp == nil || rsp.IsPartial ||
		(invocation.StructuredOutputType == nil && p.outputKey == "" && p.outputSchema == nil) {
		return
	}
	// Only process complete (non-partial) responses.
	// Extract text content from the response.
	content, ok := p.extractFinalContent(rsp)
	if !ok {
		return
	}
	jsonObject, ok := extractFirstJSONObject(content)

	if ok {
		// 1) Emit typed structured output payload if configured.
		p.emitTypedStructuredOutput(ctx, invocation, jsonObject, ch)
	}

	// 2) Handle output_key functionality (raw persistence, optional schema validation).
	p.handleOutputKey(ctx, invocation, content, jsonObject, ch)
}

// extractFinalContent returns the final text content if response is complete.
func (p *OutputResponseProcessor) extractFinalContent(rsp *model.Response) (string, bool) {
	if rsp == nil || rsp.IsPartial {
		return "", false
	}
	if len(rsp.Choices) == 0 || rsp.Choices[0].Message.Content == "" {
		return "", false
	}
	return rsp.Choices[0].Message.Content, true
}

// emitTypedStructuredOutput emits a typed payload event when StructuredOutputType is set.
func (p *OutputResponseProcessor) emitTypedStructuredOutput(
	ctx context.Context, invocation *agent.Invocation, jsonObject string, ch chan<- *event.Event,
) {
	if invocation.StructuredOutputType == nil {
		return
	}
	var instance any
	if invocation.StructuredOutputType.Kind() == reflect.Pointer {
		instance = reflect.New(invocation.StructuredOutputType.Elem()).Interface()
	} else {
		instance = reflect.New(invocation.StructuredOutputType).Interface()
	}
	if err := json.Unmarshal([]byte(jsonObject), instance); err != nil {
		log.ErrorfContext(
			ctx,
			"Structured output unmarshal failed: %v",
			err,
		)
		return
	}
	typedEvt := event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStructuredOutputPayload(instance),
	)

	log.DebugContext(ctx, "Emitted typed structured output payload event.")
	agent.EmitEvent(ctx, invocation, ch, typedEvt)
}

// handleOutputKey validates and emits state delta for output_key/output_schema cases.
func (p *OutputResponseProcessor) handleOutputKey(ctx context.Context, invocation *agent.Invocation, content string,
	jsonObject string, ch chan<- *event.Event) {
	if p.outputKey == "" && p.outputSchema == nil {
		return
	}
	result := content
	// If output_schema is present, ensure content is JSON.
	if p.outputSchema != nil {
		if jsonObject == "" {
			return
		}
		var parsedJSON any
		if err := json.Unmarshal([]byte(jsonObject), &parsedJSON); err != nil {
			log.WarnfContext(
				ctx,
				"Failed to parse output as JSON for output_schema "+
					"validation: %v",
				err,
			)
			return
		}
		// Store the original JSON string.
		result = jsonObject
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

	log.DebugfContext(
		ctx,
		"Emitted state delta event with key '%s'.",
		p.outputKey,
	)
	if err := agent.EmitEvent(ctx, invocation, ch, stateEvent); err != nil {
		return
	}

	// Ensure that the state delta is synchronized to the local session before executing the next agent.
	// maybe the next agent need to use delta state before executing the flow.
	completionID := agent.GetAppendEventNoticeKey(stateEvent.ID)
	if err := invocation.AddNoticeChannelAndWait(ctx, completionID,
		agent.WaitNoticeWithoutTimeout); err != nil {
		log.WarnfContext(
			ctx,
			"Failed to add notice channel for completion ID %s: %v",
			completionID,
			err,
		)
	}
}

// extractFirstJSONObject tries to extract the first balanced top-level JSON object from s.
func extractFirstJSONObject(s string) (string, bool) {
	start := findJSONStart(s)
	if start == -1 {
		return "", false
	}
	return scanBalancedJSON(s, start)
}

// findJSONStart finds the index of the first opening bracket in s.
func findJSONStart(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			return i
		}
	}
	return -1
}

// scanBalancedJSON scans a string for a balanced JSON object.
func scanBalancedJSON(s string, start int) (string, bool) {
	stack := make([]byte, 0, 8)
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		c := s[i]

		if escaped {
			escaped = false
			continue
		}

		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			default:
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 {
				return "", false
			}
			top := stack[len(stack)-1]
			if (top == '{' && c == '}') || (top == '[' && c == ']') {
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return s[start : i+1], true
				}
			} else {
				return "", false
			}
		default:
		}
	}
	return "", false
}
