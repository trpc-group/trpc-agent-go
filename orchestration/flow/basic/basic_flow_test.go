package basic

import (
	"context"
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/flow"
)

// MockRequestProcessor for testing
type MockRequestProcessor struct {
	ShouldGenerateEvent bool
}

func (m *MockRequestProcessor) ProcessRequest(ctx context.Context, invocationCtx *flow.InvocationContext, request *model.Request, eventChan chan<- *event.Event) {
	// Add a test message to the request
	request.Messages = append(request.Messages, model.NewUserMessage("Test message from processor"))

	if m.ShouldGenerateEvent {
		evt := event.NewEvent(invocationCtx.InvocationID, invocationCtx.AgentName)
		evt.Object = "preprocessing"

		select {
		case eventChan <- evt:
			log.Debugf("MockRequestProcessor sent event")
		case <-ctx.Done():
			log.Debugf("MockRequestProcessor cancelled")
		}
	}
}

// MockResponseProcessor for testing
type MockResponseProcessor struct {
	ShouldError bool
}

func (m *MockResponseProcessor) ProcessResponse(ctx context.Context, invocationCtx *flow.InvocationContext, response *model.Response) ([]*event.Event, error) {
	if m.ShouldError {
		return nil, errors.New("mock processor error")
	}

	evt := event.NewEvent(invocationCtx.InvocationID, invocationCtx.AgentName)
	evt.Object = "postprocessing"
	return []*event.Event{evt}, nil
}

// MockModel for testing
type MockModel struct {
	ShouldError bool
}

func (m *MockModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
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
			Model:   request.Model,
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
	// Create a new flow
	f := New()

	// Add processors
	f.AddRequestProcessor(&MockRequestProcessor{ShouldGenerateEvent: true})
	f.AddResponseProcessor(&MockResponseProcessor{ShouldError: false})

	// Create invocation context
	invocationCtx := &flow.InvocationContext{
		AgentName:    "test-agent",
		InvocationID: "test-invocation-123",
		Model:        &MockModel{ShouldError: false},
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run the flow
	eventChan := f.Run(ctx, invocationCtx)

	// Collect events
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
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

	for _, evt := range events {
		switch evt.Object {
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
	// Create a new flow
	f := New()

	// Create invocation context without model (mock mode)
	invocationCtx := &flow.InvocationContext{
		AgentName:    "test-agent",
		InvocationID: "test-invocation-123",
		Model:        nil, // No model - should use mock
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run the flow
	eventChan := f.Run(ctx, invocationCtx)

	// Collect events
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have at least one mock event
	if len(events) == 0 {
		t.Error("Expected at least one event in mock mode")
	}

	// Check mock event
	mockEvent := events[0]
	if mockEvent.Model != "mock" {
		t.Errorf("Expected mock model, got %s", mockEvent.Model)
	}
	if mockEvent.Object != "chat.completion" {
		t.Errorf("Expected chat.completion object, got %s", mockEvent.Object)
	}
}

func TestFlow_ProcessorRegistry(t *testing.T) {
	f := New()

	// Test adding processors
	reqProcessor := &MockRequestProcessor{}
	respProcessor := &MockResponseProcessor{}

	f.AddRequestProcessor(reqProcessor)
	f.AddResponseProcessor(respProcessor)

	// Test getting processors
	reqProcessors := f.GetRequestProcessors()
	respProcessors := f.GetResponseProcessors()

	if len(reqProcessors) != 1 {
		t.Errorf("Expected 1 request processor, got %d", len(reqProcessors))
	}
	if len(respProcessors) != 1 {
		t.Errorf("Expected 1 response processor, got %d", len(respProcessors))
	}
}

func TestFlow_Interfaces(t *testing.T) {
	f := New()

	// Test that Flow implements the flow.Flow interface
	var _ flow.Flow = f

	// Test that Flow implements ProcessorRegistry interface
	var _ flow.ProcessorRegistry = f
}
