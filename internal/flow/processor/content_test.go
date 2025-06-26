package processor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

func TestNewContentRequestProcessor(t *testing.T) {
	processor := NewContentRequestProcessor()

	assert.NotNil(t, processor)
	assert.Equal(t, IncludeContentsAll, processor.IncludeContents)
}

func TestContentRequestProcessor_ProcessRequest_BasicFlow(t *testing.T) {
	processor := NewContentRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{},
	}

	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hello, how are you?",
		},
	}

	eventCh := make(chan *event.Event, 10)
	ctx := context.Background()

	processor.ProcessRequest(ctx, invocation, req, eventCh)

	// Should append the user message.
	require.Equal(t, 1, len(req.Messages))
	assert.Equal(t, model.RoleUser, req.Messages[0].Role)
	assert.Equal(t, "Hello, how are you?", req.Messages[0].Content)

	// Should send preprocessing event.
	select {
	case evt := <-eventCh:
		assert.Equal(t, model.ObjectTypePreprocessingContent, evt.Object)
	default:
		t.Error("Expected preprocessing event not sent")
	}
}

func TestContentRequestProcessor_ProcessRequest_WithSession(t *testing.T) {
	processor := NewContentRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{},
	}

	// Create session with historical events.
	sess := &session.Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Index: 0,
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "Previous user message",
							},
						},
					},
					Done: true,
				},
				InvocationID: "prev-invocation",
				Author:       string(model.RoleUser),
				ID:           "prev-event",
				Timestamp:    time.Now(),
			},
		},
	}

	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "current-invocation",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Current message",
		},
	}

	eventCh := make(chan *event.Event, 10)
	ctx := context.Background()

	processor.ProcessRequest(ctx, invocation, req, eventCh)

	// Should have messages from session plus current message.
	require.GreaterOrEqual(t, len(req.Messages), 1)

	// Check for current message.
	found := false
	for _, msg := range req.Messages {
		if msg.Content == "Current message" {
			found = true
			break
		}
	}
	assert.True(t, found, "Current message should be present")
}

func TestContentRequestProcessor_ProcessRequest_IncludeContentsNone(t *testing.T) {
	processor := &ContentRequestProcessor{
		IncludeContents: IncludeContentsNone,
	}
	req := &model.Request{
		Messages: []model.Message{},
	}

	// Create session with events.
	sess := &session.Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Index: 0,
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "Session message",
							},
						},
					},
					Done: true,
				},
				InvocationID: "session-invocation",
				Author:       string(model.RoleUser),
				ID:           "session-event",
				Timestamp:    time.Now(),
			},
		},
	}

	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "current-invocation",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Current message",
		},
	}

	eventCh := make(chan *event.Event, 10)
	ctx := context.Background()

	processor.ProcessRequest(ctx, invocation, req, eventCh)

	// Should only have current message, not session messages.
	require.Equal(t, 1, len(req.Messages))
	assert.Equal(t, "Current message", req.Messages[0].Content)
}

func TestContentRequestProcessor_FilterEvents(t *testing.T) {
	processor := NewContentRequestProcessor()

	events := []event.Event{
		// Valid event with content.
		{
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleUser,
							Content: "Valid message",
						},
					},
				},
				Done: true,
			},
			InvocationID: "other-invocation",
			Author:       string(model.RoleUser),
			ID:           "valid-event",
			Timestamp:    time.Now(),
		},
		// Event from current invocation (should be filtered).
		{
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleUser,
							Content: "Current invocation message",
						},
					},
				},
				Done: true,
			},
			InvocationID: "current-invocation",
			Author:       string(model.RoleUser),
			ID:           "current-event",
			Timestamp:    time.Now(),
		},
		// Event without valid content (should be filtered).
		{
			Response: &model.Response{
				Done: true,
			},
			InvocationID: "empty-invocation",
			Author:       "test-agent",
			ID:           "empty-event",
			Timestamp:    time.Now(),
		},
	}

	filteredEvents := processor.filterEvents(events)

	// Should only have the valid event.
	require.Equal(t, 2, len(filteredEvents))
	assert.Equal(t, "Valid message", filteredEvents[0].Choices[0].Message.Content)
}

func TestContentRequestProcessor_HasValidContent(t *testing.T) {
	processor := NewContentRequestProcessor()

	// Event with message content.
	eventWithContent := event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "Test content",
					},
				},
			},
			Done: true,
		},
	}

	// Event with delta content.
	eventWithDelta := event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Index: 0,
					Delta: model.Message{
						Role:    model.RoleAssistant,
						Content: "Delta content",
					},
				},
			},
			Done: true,
		},
	}

	// Event with tool calls.
	eventWithToolCalls := event.Event{
		Response: &model.Response{
			ToolCalls: []model.ToolCall{
				{
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name: "test_function",
					},
					ID: "call-123",
				},
			},
			Done: true,
		},
	}

	// Event without content.
	eventWithoutContent := event.Event{
		Response: &model.Response{
			Done: true,
		},
	}

	assert.True(t, processor.hasValidContent(&eventWithContent))
	assert.True(t, processor.hasValidContent(&eventWithDelta))
	assert.True(t, processor.hasValidContent(&eventWithToolCalls))
	assert.False(t, processor.hasValidContent(&eventWithoutContent))
}

func TestContentRequestProcessor_ToolCallHandling(t *testing.T) {
	processor := NewContentRequestProcessor()

	// Create event with tool calls.
	toolEvent := event.Event{
		Response: &model.Response{
			ToolCalls: []model.ToolCall{
				{
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      "get_weather",
						Arguments: []byte(`{"location": "San Francisco"}`),
					},
					ID: "call-123",
				},
			},
			Done: true,
		},
		InvocationID: "tool-invocation",
		Author:       "test-agent",
		ID:           "tool-event",
		Timestamp:    time.Now(),
	}

	// Test hasValidContent with tool calls.
	assert.True(t, processor.hasValidContent(&toolEvent))

	// Test tool call message building.
	toolMessage := processor.buildToolCallMessage(toolEvent.ToolCalls)
	assert.Contains(t, toolMessage, "Called get_weather with:")
	assert.Contains(t, toolMessage, `{"location": "San Francisco"}`)
}

func TestContentRequestProcessor_ConvertEventsToMessages(t *testing.T) {
	processor := NewContentRequestProcessor()

	events := []event.Event{
		// Event with message content.
		{
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleUser,
							Content: "User message",
						},
					},
				},
				Done: true,
			},
			InvocationID: "msg-invocation",
			Author:       string(model.RoleUser),
			ID:           "msg-event",
			Timestamp:    time.Now(),
		},
		// Event with delta content.
		{
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "Assistant delta",
						},
					},
				},
				Done: true,
			},
			InvocationID: "delta-invocation",
			Author:       "assistant",
			ID:           "delta-event",
			Timestamp:    time.Now(),
		},
		// Event with tool calls.
		{
			Response: &model.Response{
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "test_function",
							Arguments: []byte(`{"param": "value"}`),
						},
						ID: "call-456",
					},
				},
				Done: true,
			},
			InvocationID: "tool-invocation",
			Author:       "assistant",
			ID:           "tool-event",
			Timestamp:    time.Now(),
		},
	}

	messages := processor.convertEventsToMessages(events)

	// Should have 3 messages: user message, assistant delta, tool call message.
	require.Equal(t, 3, len(messages))

	assert.Equal(t, "User message", messages[0].Content)
	assert.Equal(t, model.RoleUser, messages[0].Role)

	assert.Equal(t, "Assistant delta", messages[1].Content)
	assert.Equal(t, model.RoleAssistant, messages[1].Role)

	assert.Contains(t, messages[2].Content, "Called test_function with:")
	assert.Equal(t, model.RoleAssistant, messages[2].Role)
}
