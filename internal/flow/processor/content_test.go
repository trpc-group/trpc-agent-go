package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewContentRequestProcessor(t *testing.T) {
	processor := NewContentRequestProcessor()

	assert.NotNil(t, processor)
	assert.Equal(t, IncludeContentsAll, processor.IncludeContents)
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
