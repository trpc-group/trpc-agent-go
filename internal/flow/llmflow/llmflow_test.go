package llmflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// mockRequestProcessor for testing
type mockRequestProcessor struct {
	ShouldGenerateEvent bool
}

func (m *mockRequestProcessor) ProcessRequest(ctx context.Context, invocation *agent.Invocation, request *model.Request, eventChan chan<- *event.Event) {
	// Just send an event if requested, don't modify the request for now
	if m.ShouldGenerateEvent {
		if invocation == nil {
			log.Errorf("invocation is nil")
			return
		}

		evt := event.New(invocation.InvocationID, invocation.AgentName)
		evt.Object = "preprocessing"

		select {
		case eventChan <- evt:
			log.Debugf("mockRequestProcessor sent event")
		case <-ctx.Done():
			log.Debugf("mockRequestProcessor cancelled")
		}
	}
}

// mockResponseProcessor for testing
type mockResponseProcessor struct {
	ShouldGenerateEvent bool
}

func (m *mockResponseProcessor) ProcessResponse(ctx context.Context, invocation *agent.Invocation, response *model.Response, eventChan chan<- *event.Event) {
	if m.ShouldGenerateEvent {
		evt := event.New(invocation.InvocationID, invocation.AgentName)
		evt.Object = "postprocessing"

		select {
		case eventChan <- evt:
			log.Debugf("mockResponseProcessor sent event")
		case <-ctx.Done():
			log.Debugf("mockResponseProcessor cancelled")
		}
	}
}

// mockModel for testing
type mockModel struct {
	ShouldError bool
}

func (m *mockModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	if m.ShouldError {
		return nil, errors.New("mock model error")
	}

	responseChan := make(chan *model.Response, 1)
	go func() {
		defer close(responseChan)

		response := &model.Response{
			ID:      "test-response-id",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "test-model",
			Choices: []model.Choice{
				{
					Message: model.NewAssistantMessage("Hello! This is a test response."),
				},
			},
			Done: true,
		}

		select {
		case responseChan <- response:
		case <-ctx.Done():
		}
	}()

	return responseChan, nil
}

func TestFlow_Run(t *testing.T) {
	// Create processors
	reqProcessor := &mockRequestProcessor{ShouldGenerateEvent: true}
	respProcessor := &mockResponseProcessor{ShouldGenerateEvent: true}

	// Create a new flow with processors
	f := New([]flow.RequestProcessor{reqProcessor}, []flow.ResponseProcessor{respProcessor}, Options{})

	// Create invocation context
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation-123",
		Model:        &mockModel{ShouldError: false},
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run the flow
	eventChan, err := f.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("Flow.Run() failed: %v", err)
	}

	// Collect events
	var events []*event.Event
	for e := range eventChan {
		events = append(events, e)
		if len(events) >= 3 { // Expect: preprocessing, LLM response, postprocessing
			break
		}
	}

	// Verify events
	if len(events) < 2 {
		t.Errorf("Expected at least 2 events, got %d", len(events))
	}

	// Check for preprocessing event
	hasPreprocessing := false
	hasLLMResponse := false
	hasPostprocessing := false

	for _, e := range events {
		switch e.Object {
		case "preprocessing":
			hasPreprocessing = true
		case "chat.completion":
			hasLLMResponse = true
		case "postprocessing":
			hasPostprocessing = true
		}
	}

	if !hasPreprocessing {
		t.Error("Expected preprocessing event")
	}
	if !hasLLMResponse {
		t.Error("Expected LLM response event")
	}
	if !hasPostprocessing {
		t.Error("Expected postprocessing event")
	}
}

func TestFlow_NoModel(t *testing.T) {
	// Create a new flow with no processors
	f := New(nil, nil, Options{})

	// Create invocation context without model
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation-123",
		Model:        nil, // No model - should return error
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run the flow - should return error event
	eventChan, err := f.Run(ctx, invocation)
	if err != nil {
		// This is expected since we have no model
		t.Logf("Expected error when no model: %v", err)
		return
	}

	// Collect events and expect an error event
	var events []*event.Event
	for e := range eventChan {
		events = append(events, e)
	}

	// Should have exactly one error event
	if len(events) != 1 {
		t.Errorf("Expected 1 error event when no model available, got %d", len(events))
		return
	}

	// Verify it's an error event
	errorEvent := events[0]
	if errorEvent.Object != "error" {
		t.Errorf("Expected error object, got %s", errorEvent.Object)
	}
	if errorEvent.Error == nil {
		t.Error("Expected error field to be set")
	} else if errorEvent.Error.Type != model.ErrorTypeFlowError {
		t.Errorf("Expected flow error type %s, got %s", model.ErrorTypeFlowError, errorEvent.Error.Type)
	} else if errorEvent.Error.Message != "no model available for LLM call" {
		t.Errorf("Expected specific error message, got %s", errorEvent.Error.Message)
	}
}

func TestFlow_ModelError(t *testing.T) {
	// Create a new flow with no processors
	f := New(nil, nil, Options{})

	// Create invocation context with error model
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation-123",
		Model:        &mockModel{ShouldError: true},
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run the flow
	eventChan, err := f.Run(ctx, invocation)
	if err != nil {
		// This is expected since model will error
		t.Logf("Expected error from model: %v", err)
		return
	}

	// If no immediate error, collect events
	var events []*event.Event
	for e := range eventChan {
		events = append(events, e)
	}

	// Should have no LLM response events since model fails
	hasLLMResponse := false
	for _, e := range events {
		if e.Object == "chat.completion" {
			hasLLMResponse = true
		}
	}

	if hasLLMResponse {
		t.Error("Should not have LLM response when model errors")
	}
}

func TestFlow_NoProcessors(t *testing.T) {
	// Create a new flow with no processors
	f := New(nil, nil, Options{})

	// Create invocation context
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation-123",
		Model:        &mockModel{ShouldError: false},
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run the flow
	eventChan, err := f.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("Flow.Run() failed: %v", err)
	}

	// Collect events
	var events []*event.Event
	for e := range eventChan {
		events = append(events, e)
	}

	// Should have only LLM response event (no processor events)
	if len(events) != 1 {
		t.Errorf("Expected 1 event (LLM response), got %d", len(events))
	}

	if events[0].Object != "chat.completion" {
		t.Errorf("Expected chat.completion object, got %s", events[0].Object)
	}
}

func TestFlow_Interfaces(t *testing.T) {
	f := New(nil, nil, Options{})

	// Test that Flow implements the flow.Flow interface
	var _ flow.Flow = f
}
