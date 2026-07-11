//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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

// 构造助手工具调用事件。
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

// 构造工具响应事件。
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
