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
	"strings"

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
	// Only process complete (non-partial) responses.
	// Extract text content from the response.
	content, ok := p.extractFinalContent(rsp)
	if !ok {
		return
	}

	// 1) Emit typed structured output payload if configured.
	p.emitTypedStructuredOutput(ctx, invocation, content, ch)

	// 2) Handle output_key functionality (raw persistence, optional schema validation).
	p.handleOutputKey(ctx, invocation, content, ch)
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
	ctx context.Context, invocation *agent.Invocation, content string, ch chan<- *event.Event,
) {
	if invocation == nil || invocation.StructuredOutputType == nil {
		return
	}
	contentTrim := strings.TrimSpace(content)
	candidate := contentTrim
	if !strings.HasPrefix(contentTrim, "{") {
		if obj, ok := extractFirstJSONObject(contentTrim); ok {
			candidate = obj
			log.Debugf("Extracted JSON object candidate for structured output.")
		}
	}
	if !strings.HasPrefix(candidate, "{") {
		return
	}
	var instance any
	if invocation.StructuredOutputType.Kind() == reflect.Pointer {
		instance = reflect.New(invocation.StructuredOutputType.Elem()).Interface()
	} else {
		instance = reflect.New(invocation.StructuredOutputType).Interface()
	}
	if err := json.Unmarshal([]byte(candidate), instance); err != nil {
		log.Errorf("Structured output unmarshal failed: %v", err)
		return
	}
	typedEvt := event.New(invocation.InvocationID, invocation.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStructuredOutputPayload(instance),
	)
	typedEvt.RequiresCompletion = true
	select {
	case ch <- typedEvt:
		log.Debugf("Emitted typed structured output payload event.")
	case <-ctx.Done():
		return
	}
}

// handleOutputKey validates and emits state delta for output_key/output_schema cases.
func (p *OutputResponseProcessor) handleOutputKey(
	ctx context.Context, invocation *agent.Invocation, content string, ch chan<- *event.Event,
) {
	if p.outputKey == "" && p.outputSchema == nil {
		return
	}
	result := content
	// If output_schema is present, ensure content is JSON.
	if p.outputSchema != nil {
		if strings.TrimSpace(content) == "" {
			return
		}
		var parsedJSON any
		if err := json.Unmarshal([]byte(content), &parsedJSON); err != nil {
			log.Warnf("Failed to parse output as JSON for output_schema validation: %v", err)
			return
		}
		// Store the original JSON string.
		result = content
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
	select {
	case ch <- stateEvent:
		log.Debugf("Emitted state delta event with key '%s'.", p.outputKey)
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

// extractFirstJSONObject tries to extract the first balanced top-level JSON object from s.
func extractFirstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '{' {
			depth++
			continue
		}
		if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
