//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func init() {
	registry.MustRegister(&ToolsComponent{})
}

// ToolsComponent executes tools called by LLM.
type ToolsComponent struct{}

// Metadata returns the component metadata.
func (c *ToolsComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.tools",
		DisplayName: "Tools Executor",
		Description: "Executes tools called by LLM and returns results",
		Category:    "Core",
		Version:     "1.0.0",
		Inputs: []registry.ParameterSchema{
			{
				Name:        "messages",
				Type:        "[]model.Message",
				GoType:      reflect.TypeOf([]model.Message{}),
				Description: "Message history with tool calls",
				Required:    true,
				Reducer:     "message",
			},
		},
		Outputs: []registry.ParameterSchema{
			{
				Name:        "messages",
				Type:        "[]model.Message",
				GoType:      reflect.TypeOf([]model.Message{}),
				Description: "Message history with tool results",
				Reducer:     "message",
			},
		},
		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "tools",
				DisplayName: "Tools Map",
				Description: "Map of tool name to tool instance (can be provided via state)",
				Type:        "map[string]tool.Tool",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
		},
	}
}

// Execute executes the tools component.
func (c *ToolsComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Get messages from state
	messages, ok := state[graph.StateKeyMessages].([]model.Message)
	if !ok {
		return nil, fmt.Errorf("messages not found in state or invalid type")
	}

	// Get tools from config or state
	var tools map[string]tool.Tool

	// First try to get from config
	if toolsConfig, ok := config["tools"]; ok {
		tools = make(map[string]tool.Tool)
		if toolsMap, ok := toolsConfig.(map[string]tool.Tool); ok {
			tools = toolsMap
		} else if toolsMap, ok := toolsConfig.(map[string]any); ok {
			// Try to convert from map[string]any
			for name, t := range toolsMap {
				if toolInstance, ok := t.(tool.Tool); ok {
					tools[name] = toolInstance
				}
			}
		}
	}

	// If not in config, try to get from state
	if len(tools) == 0 {
		if toolsState, ok := state["tools"]; ok {
			tools = make(map[string]tool.Tool)
			if toolsMap, ok := toolsState.(map[string]tool.Tool); ok {
				tools = toolsMap
			} else if toolsMap, ok := toolsState.(map[string]any); ok {
				// Try to convert from map[string]any
				for name, t := range toolsMap {
					if toolInstance, ok := t.(tool.Tool); ok {
						tools[name] = toolInstance
					}
				}
			}
		}
	}

	if len(tools) == 0 {
		return nil, fmt.Errorf("no valid tools found in config or state")
	}

	// Find the last assistant message with tool calls
	var lastAssistantMsg *model.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleAssistant && len(messages[i].ToolCalls) > 0 {
			lastAssistantMsg = &messages[i]
			break
		}
	}

	if lastAssistantMsg == nil {
		// No tool calls to execute
		return graph.State{}, nil
	}

	// Execute each tool call
	var toolMessages []model.Message
	for _, toolCall := range lastAssistantMsg.ToolCalls {
		toolName := toolCall.Function.Name
		toolInstance, ok := tools[toolName]
		if !ok {
			// Tool not found, return error message
			toolMessages = append(toolMessages, model.Message{
				Role:     model.RoleTool,
				Content:  fmt.Sprintf("Error: tool '%s' not found", toolName),
				ToolID:   toolCall.ID,
				ToolName: toolName,
			})
			continue
		}

		// Execute tool using CallableTool interface
		var result any
		var err error
		if callableTool, ok := toolInstance.(tool.CallableTool); ok {
			result, err = callableTool.Call(ctx, []byte(toolCall.Function.Arguments))
		} else {
			err = fmt.Errorf("tool '%s' is not callable", toolName)
		}

		if err != nil {
			toolMessages = append(toolMessages, model.Message{
				Role:     model.RoleTool,
				Content:  fmt.Sprintf("Error executing tool: %v", err),
				ToolID:   toolCall.ID,
				ToolName: toolName,
			})
			continue
		}

		// Convert result to JSON string
		resultJSON, err := json.Marshal(result)
		if err != nil {
			toolMessages = append(toolMessages, model.Message{
				Role:     model.RoleTool,
				Content:  fmt.Sprintf("Error marshaling result: %v", err),
				ToolID:   toolCall.ID,
				ToolName: toolName,
			})
			continue
		}

		// Add tool result message
		toolMessages = append(toolMessages, model.Message{
			Role:     model.RoleTool,
			Content:  string(resultJSON),
			ToolID:   toolCall.ID,
			ToolName: toolName,
		})
	}

	// Return state with tool messages
	return graph.State{
		graph.StateKeyMessages: toolMessages,
	}, nil
}
