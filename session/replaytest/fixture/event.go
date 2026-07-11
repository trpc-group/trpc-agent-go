//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fixture builds deterministic session events for replay tests.
package fixture

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// NewUserEvent builds a user message event with the given content.
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

// NewAssistantEvent builds an assistant message event with the given content.
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

// NewAssistantToolCallEvent builds an assistant tool-call event.
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

// NewToolResponseEvent builds a tool response event.
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
