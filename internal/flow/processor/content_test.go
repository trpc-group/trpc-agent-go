package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

func TestNewContentRequestProcessor(t *testing.T) {
	processor := NewContentRequestProcessor()

	assert.NotNil(t, processor)
	assert.Equal(t, IncludeContentsAll, processor.IncludeContents)
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
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name: "test_function",
								},
								ID: "call-123",
							},
						},
					},
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
