//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"strings"

	demotool "trpc.group/trpc-go/trpc-agent-go/examples/agui/server/externaltool/graphagent/tool"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var executeInternalToolsNode = graph.NewToolsNodeFunc(
	demotool.NewInternalTools(),
	graph.WithEnableParallelTools(true),
)

func internalToolNode(ctx context.Context, state graph.State) (any, error) {
	return executeInternalToolsNode(ctx, state)
}

func externalInterruptNode(ctx context.Context, state graph.State) (any, error) {
	msgs, _ := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	var toolMessages []model.Message
	pendingToolCalls := latestAssistantToolCalls(msgs)
	for _, pendingToolCall := range pendingToolCalls {
		if pendingToolCall.ID == "" || hasToolResult(msgs, pendingToolCall.ID) {
			continue
		}
		if !demotool.IsExternalTool(pendingToolCall.Function.Name) {
			return nil, fmt.Errorf("unsupported external tool %q", pendingToolCall.Function.Name)
		}
		toolMessage, err := externalToolResultMessage(ctx, state, pendingToolCall, pendingToolCalls)
		if err != nil {
			return nil, err
		}
		toolMessages = append(toolMessages, toolMessage)
	}
	if len(toolMessages) == 0 {
		return nil, nil
	}
	return graph.State{
		graph.StateKeyMessages: graph.AppendMessages{Items: toolMessages},
	}, nil
}

func externalToolResultMessage(
	ctx context.Context,
	state graph.State,
	pendingToolCall model.ToolCall,
	pendingToolCalls []model.ToolCall,
) (model.Message, error) {
	resumeValue, err := graph.Interrupt(ctx, state, pendingToolCall.ID, externalInterruptPrompt(pendingToolCalls))
	if err != nil {
		return model.Message{}, err
	}
	content, ok := resumeValue.(string)
	if !ok {
		return model.Message{}, fmt.Errorf("resume value for tool call %s must be a string", pendingToolCall.ID)
	}
	if strings.TrimSpace(content) == "" {
		return model.Message{}, fmt.Errorf("resume value for tool call %s cannot be empty", pendingToolCall.ID)
	}
	return model.NewToolMessage(pendingToolCall.ID, pendingToolCall.Function.Name, content), nil
}

func externalInterruptPrompt(toolCalls []model.ToolCall) map[string]any {
	tools := make([]map[string]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		if toolCall.ID == "" || !demotool.IsExternalTool(toolCall.Function.Name) {
			continue
		}
		tools = append(tools, map[string]string{
			"toolCallId": toolCall.ID,
			"name":       toolCall.Function.Name,
			"arguments":  string(toolCall.Function.Arguments),
		})
	}
	return map[string]any{
		"message": "Run the external tool calls and resume with one role=tool message per toolCallId.",
		"tools":   tools,
	}
}

func latestAssistantToolCalls(messages []model.Message) []model.ToolCall {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		switch msg.Role {
		case model.RoleAssistant:
			return msg.ToolCalls
		case model.RoleUser:
			return nil
		default:
			continue
		}
	}
	return nil
}

func hasToolResult(messages []model.Message, toolID string) bool {
	for _, msg := range messages {
		if msg.Role == model.RoleTool && msg.ToolID == toolID {
			return true
		}
	}
	return false
}
