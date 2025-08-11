package processor

import (
	"context"
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
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"test": map[string]interface{}{
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

	// Create a completion channel for the invocation
	eventCompletionCh := make(chan string, 1)

	// Create a test invocation
	invocation := &agent.Invocation{
		InvocationID:      "test_invocation",
		AgentName:         "test_agent",
		EventCompletionCh: eventCompletionCh,
	}

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
		processor.ProcessResponse(ctx, invocation, response, eventCh)
		close(eventCh)
	}()

	// Wait for the event to be sent and then send completion signal
	var emittedEvent *event.Event
	select {
	case event := <-eventCh:
		emittedEvent = event
		// Send completion signal for the event
		if event.RequiresCompletion {
			eventCompletionCh <- event.ID
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

	// Create a completion channel for the invocation
	eventCompletionCh := make(chan string, 1)

	// Create a test invocation
	invocation := &agent.Invocation{
		InvocationID:      "test_invocation",
		AgentName:         "test_agent",
		EventCompletionCh: eventCompletionCh,
	}

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
	processor.ProcessResponse(ctx, invocation, response, eventCh)

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

	// Create a completion channel for the invocation
	eventCompletionCh := make(chan string, 1)

	// Create a test invocation
	invocation := &agent.Invocation{
		InvocationID:      "test_invocation",
		AgentName:         "test_agent",
		EventCompletionCh: eventCompletionCh,
	}

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
	processor.ProcessResponse(ctx, invocation, response, eventCh)

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
