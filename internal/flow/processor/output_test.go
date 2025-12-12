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
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewOutputResponseProcessor(t *testing.T) {
	// Test with output_key only
	processor1 := NewOutputResponseProcessor("test_key", nil)
	if processor1.outputKey != "test_key" {
		t.Errorf("Expected outputKey to be 'test_key', got '%s'", processor1.outputKey)
	}
	if processor1.outputSchema != nil {
		t.Errorf("Expected outputSchema to be nil, got %v", processor1.outputSchema)
	}

	// Test with output_schema only
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"test": map[string]any{
				"type": "string",
			},
		},
	}
	processor2 := NewOutputResponseProcessor("", schema)
	if processor2.outputKey != "" {
		t.Errorf("Expected outputKey to be empty, got '%s'", processor2.outputKey)
	}
	if processor2.outputSchema == nil {
		t.Errorf("Expected outputSchema to be set, got nil")
	}

	// Test with both
	processor3 := NewOutputResponseProcessor("test_key", schema)
	if processor3.outputKey != "test_key" {
		t.Errorf("Expected outputKey to be 'test_key', got '%s'", processor3.outputKey)
	}
	if processor3.outputSchema == nil {
		t.Errorf("Expected outputSchema to be set, got nil")
	}
}

func TestOutputResponseProcessor_ProcessResponse(t *testing.T) {
	ctx := context.Background()

	// Create a test processor with output_key
	processor := NewOutputResponseProcessor("test_key", nil)

	// Create a test invocation
	invocation := agent.NewInvocation()

	// Create a test response with content
	response := &model.Response{
		IsPartial: false,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Test output content",
				},
			},
		},
	}

	// Create a channel to receive events
	eventCh := make(chan *event.Event, 1)

	// Start processing in a goroutine so we can send completion signals
	go func() {
		processor.ProcessResponse(ctx, invocation, &model.Request{}, response, eventCh)
		close(eventCh)
	}()

	// Wait for the event to be sent and then send completion signal
	var emittedEvent *event.Event
	select {
	case event := <-eventCh:
		emittedEvent = event
		// Send completion signal for the event
		if event.RequiresCompletion {
			invocation.NotifyCompletion(ctx, agent.GetAppendEventNoticeKey(event.ID))
		}
	case <-ctx.Done():
		t.Fatal("Test timed out waiting for event")
	}

	// Collect any remaining events
	var events []*event.Event
	for event := range eventCh {
		events = append(events, event)
	}

	// Verify that an event was emitted
	if emittedEvent == nil {
		t.Fatal("Expected an event to be emitted")
	}

	if emittedEvent.Object != "state.update" {
		t.Errorf("Expected object to be 'state.update', got '%s'", emittedEvent.Object)
	}

	if len(emittedEvent.StateDelta) != 1 {
		t.Errorf("Expected 1 state delta, got %d", len(emittedEvent.StateDelta))
		return
	}

	if value, exists := emittedEvent.StateDelta["test_key"]; !exists {
		t.Errorf("Expected state delta to contain 'test_key'")
	} else if string(value) != "Test output content" {
		t.Errorf("Expected state delta value to be 'Test output content', got '%s'", string(value))
	}

	// Verify no additional events
	if len(events) != 0 {
		t.Errorf("Expected 0 additional events, got %d", len(events))
	}
}

func TestOutputResponseProcessor_ProcessResponse_NoOutputKey(t *testing.T) {
	ctx := context.Background()

	// Create a test processor without output_key
	processor := NewOutputResponseProcessor("", nil)

	// Create a test invocation
	invocation := agent.NewInvocation()

	// Create a test response with content
	response := &model.Response{
		IsPartial: false,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Test output content",
				},
			},
		},
	}

	// Create a channel to receive events
	eventCh := make(chan *event.Event, 1)

	// Process the response
	processor.ProcessResponse(ctx, invocation, &model.Request{}, response, eventCh)

	// Close the channel and collect events
	close(eventCh)
	var events []*event.Event
	for event := range eventCh {
		events = append(events, event)
	}

	// Verify that no events were emitted
	if len(events) != 0 {
		t.Errorf("Expected 0 events, got %d", len(events))
	}
}

func TestOutputResponseProcessor_ProcessResponse_PartialResponse(t *testing.T) {
	ctx := context.Background()

	// Create a test processor with output_key
	processor := NewOutputResponseProcessor("test_key", nil)

	// Create a test invocation
	invocation := agent.NewInvocation()

	// Create a test response that is partial
	response := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Test output content",
				},
			},
		},
	}

	// Create a channel to receive events
	eventCh := make(chan *event.Event, 1)

	// Process the response
	processor.ProcessResponse(ctx, invocation, &model.Request{}, response, eventCh)

	// Close the channel and collect events
	close(eventCh)
	var events []*event.Event
	for event := range eventCh {
		events = append(events, event)
	}

	// Verify that no events were emitted for partial response
	if len(events) != 0 {
		t.Errorf("Expected 0 events for partial response, got %d", len(events))
	}
}

func TestOutputResponseProcessor_extractFirstJSONObject(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal",
			input: "{\"city\":\"Beijing\"}",
			want:  "{\"city\":\"Beijing\"}",
		},
		{
			name:  "with_fences",
			input: "```json\n{\"city\":\"Beijing\"}\n```",
			want:  "{\"city\":\"Beijing\"}",
		},
		{
			name:  "no_language",
			input: "```\n{\"city\":\"Beijing\"}\n```",
			want:  "{\"city\":\"Beijing\"}",
		},
		{
			name:  "same_line_language",
			input: "```json {\"city\":\"Beijing\"}```",
			want:  "{\"city\":\"Beijing\"}",
		},
		{
			name:  "multiple_prefix",
			input: "```json\njson\n{\"city\":\"Beijing\"}\n```",
			want:  "{\"city\":\"Beijing\"}",
		},
		{
			name:  "windows_newline",
			input: "```json\r\n{\"city\":\"Beijing\"}\r\n```",
			want:  "{\"city\":\"Beijing\"}",
		},
		{
			name:  "embedded_backticks",
			input: "```json\n{\"city\":\"Beijing\",\"note\":\"contains ``` fence inside\"}\n```",
			want:  "{\"city\":\"Beijing\",\"note\":\"contains ``` fence inside\"}",
		},
		{
			name:  "multiline_string",
			input: "```json\n{\"city\":\"Beijing\\n123\"}\n```",
			want:  "{\"city\":\"Beijing\\n123\"}",
		},
		{
			name:  "slice",
			input: "```json\n[{\"city\":\"Beijing\\n123\"}]\n```",
			want:  "[{\"city\":\"Beijing\\n123\"}]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := extractFirstJSONObject(tc.input)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// typedStruct is used to test typed structured output unmarshalling.
type typedStruct struct {
	A int `json:"a"`
}

func TestOutputResponseProcessor_TypedAndStateDelta(t *testing.T) {
	ctx := context.Background()
	inv := agent.NewInvocation()
	inv.AgentName = "test-agent"
	inv.StructuredOutputType = reflect.TypeOf(typedStruct{})

	// Prepare response content that contains JSON object.
	rsp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "text {\"a\":1} more",
				},
			},
		},
	}

	ch := make(chan *event.Event, 4)

	// Ack state delta event completion to prevent blocking.
	go func() {
		for e := range ch {
			if e.RequiresCompletion {
				key := agent.GetAppendEventNoticeKey(e.ID)
				_ = inv.NotifyCompletion(ctx, key)
			}
		}
	}()

	proc := NewOutputResponseProcessor(
		"k",
		map[string]any{"type": "object"},
	)
	proc.ProcessResponse(ctx, inv, nil, rsp, ch)
}

func TestOutputResponseProcessor_TypedInvalidJSON(t *testing.T) {
	ctx := context.Background()
	inv := agent.NewInvocation()
	inv.AgentName = "test-agent"
	inv.StructuredOutputType = reflect.TypeOf(typedStruct{})

	// Content contains a balanced object that is not valid JSON
	// (unquoted key), so unmarshal will fail.
	rsp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "prefix {not-valid-json} suffix",
				},
			},
		},
	}

	ch := make(chan *event.Event, 2)
	proc := NewOutputResponseProcessor(
		"",
		map[string]any{"type": "object"},
	)
	proc.ProcessResponse(ctx, inv, nil, rsp, ch)

	close(ch)
	if len(ch) != 0 {
		t.Fatalf("expected no events for invalid structured output")
	}
}

func TestOutputResponseProcessor_HandleOutputKey_AddNoticeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	inv := agent.NewInvocation()
	inv.AgentName = "test-agent"

	rsp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "{\"a\":1}",
				},
			},
		},
	}

	ch := make(chan *event.Event, 2)
	proc := NewOutputResponseProcessor("k", nil)
	proc.ProcessResponse(ctx, inv, nil, rsp, ch)

	close(ch)
}

func TestOutputResponseProcessor_ExtractFinalContent(t *testing.T) {
	p := NewOutputResponseProcessor("", nil)
	if _, ok := p.extractFinalContent(nil); ok {
		t.Fatalf("nil rsp should return false")
	}
	if _, ok := p.extractFinalContent(&model.Response{IsPartial: true}); ok {
		t.Fatalf("partial rsp should return false")
	}
	if _, ok := p.extractFinalContent(&model.Response{}); ok {
		t.Fatalf("no choices should return false")
	}
	s, ok := p.extractFinalContent(&model.Response{Choices: []model.Choice{{Message: model.Message{Content: "ok"}}}})
	if !ok || s != "ok" {
		t.Fatalf("expected ok content")
	}
}
