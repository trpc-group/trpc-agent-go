package fixture

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// 同 service_test中的
func NewUserEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: content,
				},
			}},
		},
		Timestamp: time.Now(),
	}
}

func NewAssistantEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			}},
		},
		Timestamp: time.Now(),
	}
}

func NewAssistantToolCallEvent(toolID, toolName, toolArgs string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID:   toolID,
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      toolName,
							Arguments: []byte(toolArgs),
						},
					}},
				},
			}},
		},
		Timestamp: time.Now(),
	}
}

func NewToolResponseEvent(toolID, toolName, content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   toolID,
					ToolName: toolName,
					Content:  content,
				},
			}},
		},
		Timestamp: time.Now(),
	}
}