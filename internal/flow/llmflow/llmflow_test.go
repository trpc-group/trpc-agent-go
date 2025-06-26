package llmflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// mockAgent implements agent.Agent for testing
type mockAgent struct {
	name  string
	tools []tool.Tool
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Simple mock implementation
	eventChan := make(chan *event.Event, 1)
	defer close(eventChan)
	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

// mockModel implements model.Model for testing
type mockModel struct {
	ShouldError bool
	responses   []*model.Response
	currentIdx  int
}

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if m.ShouldError {
		return nil, errors.New("mock model error")
	}

	respChan := make(chan *model.Response, len(m.responses))

	go func() {
		defer close(respChan)
		for _, resp := range m.responses {
			select {
			case respChan <- resp:
			case <-ctx.Done():
				return
			}
		}
	}()

	return respChan, nil
}

// mockRequestProcessor implements flow.RequestProcessor
type mockRequestProcessor struct{}

func (m *mockRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = "preprocessing"
	select {
	case ch <- evt:
	default:
	}
}

// mockResponseProcessor implements flow.ResponseProcessor
type mockResponseProcessor struct{}

func (m *mockResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	resp *model.Response,
	ch chan<- *event.Event,
) {
	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = "postprocessing"
	select {
	case ch <- evt:
	default:
	}
}

func TestFlow_Interface(t *testing.T) {
	llmFlow := New(nil, nil, Options{})
	var f flow.Flow = llmFlow

	// Test that the flow implements the interface
	log.Debugf("Flow interface test: %v", f)

	// Simple compile test
	var _ flow.Flow = f
}

func TestFlow_ToolCalling(t *testing.T) {
	// Create a mock tool.
	mockTool := tool.NewFunctionTool(func(args struct{ Message string }) struct{ Result string } {
		return struct{ Result string }{Result: "Tool executed with: " + args.Message}
	}, tool.FunctionToolConfig{
		Name:        "test_tool",
		Description: "A test tool",
	})

	// Create a mock agent with the tool.
	mockAgent := &mockAgent{
		name:  "test-agent",
		tools: []tool.Tool{mockTool},
	}

	// Create an LLM model that simulates tool calling.
	mockModel := &mockModel{
		responses: []*model.Response{
			{
				ID:      "response-1",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "test-model",
				Done:    true,
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						ID:   "call-123",
						Function: model.FunctionDefinitionParam{
							Name:      "test_tool",
							Arguments: []byte(`{"Message": "Hello World"}`),
						},
					},
				},
			},
		},
	}

	// Create the flow with processors.
	flow := New(
		[]flow.RequestProcessor{processor.NewContentRequestProcessor()},
		[]flow.ResponseProcessor{},
		Options{},
	)

	// Create an invocation.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Agent:        mockAgent,
		Model:        mockModel,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Please use the test tool",
		},
	}

	// Run the flow.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventChan, err := flow.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have at least 3 events: preprocessing, tool call response, tool response.
	require.GreaterOrEqual(t, len(events), 3)

	// Find the tool call event and tool response event.
	var toolCallEvent, toolResponseEvent *event.Event
	for _, evt := range events {
		if len(evt.ToolCalls) > 0 {
			toolCallEvent = evt
		} else if evt.Response != nil && len(evt.Response.Choices) > 0 {
			for _, choice := range evt.Response.Choices {
				if choice.Message.Role == model.RoleTool {
					toolResponseEvent = evt
					break
				}
			}
		}
	}

	// Verify tool call event.
	require.NotNil(t, toolCallEvent, "Tool call event should exist")
	require.Len(t, toolCallEvent.ToolCalls, 1)
	assert.Equal(t, "test_tool", toolCallEvent.ToolCalls[0].Function.Name)
	assert.Equal(t, "call-123", toolCallEvent.ToolCalls[0].ID)

	// Verify tool response event.
	require.NotNil(t, toolResponseEvent, "Tool response event should exist")
	require.Len(t, toolResponseEvent.Response.Choices, 1)
	choice := toolResponseEvent.Response.Choices[0]
	assert.Equal(t, model.RoleTool, choice.Message.Role)
	assert.Equal(t, "call-123", choice.Message.ToolID)
	assert.Contains(t, choice.Message.Content, "Tool executed with: Hello World")
}

func TestFlow_ToolExecutionError(t *testing.T) {
	// Create a mock tool that returns an error.
	// For tools that need to return errors, we need a different approach
	// since FunctionTool expects func(I) O, not func(I) (O, error)
	mockTool := tool.NewFunctionTool(func(args struct{ Invalid bool }) struct{ Error string } {
		if args.Invalid {
			return struct{ Error string }{Error: "tool execution failed"}
		}
		return struct{ Error string }{Error: ""}
	}, tool.FunctionToolConfig{
		Name:        "error_tool",
		Description: "A tool that can fail",
	})

	// Create a mock agent with the error tool.
	mockAgent := &mockAgent{
		name:  "test-agent",
		tools: []tool.Tool{mockTool},
	}

	// Create an LLM model that calls the error tool.
	mockModel := &mockModel{
		responses: []*model.Response{
			{
				ID:      "response-1",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "test-model",
				Done:    true,
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						ID:   "call-error",
						Function: model.FunctionDefinitionParam{
							Name:      "error_tool",
							Arguments: []byte(`{"Invalid": true}`),
						},
					},
				},
			},
		},
	}

	// Create the flow.
	flow := New(
		[]flow.RequestProcessor{processor.NewContentRequestProcessor()},
		[]flow.ResponseProcessor{},
		Options{},
	)

	// Create an invocation.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Agent:        mockAgent,
		Model:        mockModel,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Please use the error tool",
		},
	}

	// Run the flow.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventChan, err := flow.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Find the tool response event.
	var toolResponseEvent *event.Event
	for _, evt := range events {
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			for _, choice := range evt.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID == "call-error" {
					toolResponseEvent = evt
					break
				}
			}
		}
	}

	// Verify response event exists (even if it doesn't have an error in the content).
	require.NotNil(t, toolResponseEvent, "Tool response event should exist")
	choice := toolResponseEvent.Response.Choices[0]
	assert.Equal(t, model.RoleTool, choice.Message.Role)
	assert.Equal(t, "call-error", choice.Message.ToolID)
}

func TestFlow_UnknownTool(t *testing.T) {
	// Create a mock agent with no tools.
	mockAgent := &mockAgent{
		name:  "test-agent",
		tools: []tool.Tool{}, // No tools
	}

	// Create an LLM model that tries to call an unknown tool.
	mockModel := &mockModel{
		responses: []*model.Response{
			{
				ID:      "response-1",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "test-model",
				Done:    true,
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						ID:   "call-unknown",
						Function: model.FunctionDefinitionParam{
							Name:      "unknown_tool",
							Arguments: []byte(`{}`),
						},
					},
				},
			},
		},
	}

	// Create the flow.
	flow := New(
		[]flow.RequestProcessor{processor.NewContentRequestProcessor()},
		[]flow.ResponseProcessor{},
		Options{},
	)

	// Create an invocation.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Agent:        mockAgent,
		Model:        mockModel,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Please use an unknown tool",
		},
	}

	// Run the flow.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventChan, err := flow.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Find the tool response event with tool not found error.
	var notFoundResponseEvent *event.Event
	for _, evt := range events {
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			for _, choice := range evt.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID == "call-unknown" {
					notFoundResponseEvent = evt
					break
				}
			}
		}
	}

	// Verify tool not found response event.
	require.NotNil(t, notFoundResponseEvent, "Tool not found response event should exist")
	choice := notFoundResponseEvent.Response.Choices[0]
	assert.Equal(t, model.RoleTool, choice.Message.Role)
	assert.Equal(t, "call-unknown", choice.Message.ToolID)
	assert.Equal(t, ErrorToolNotFound, choice.Message.Content)
}
