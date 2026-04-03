//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package content

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type groundingToolCall struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Args any    `json:"args,omitempty"`
}

type groundingToolOutput struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Output any    `json:"output,omitempty"`
}

// ExtractGroundingContext formats invocation artifacts into an ADK-style validation context.
func ExtractGroundingContext(actual *evalset.Invocation) (string, error) {
	if actual == nil {
		return "No validation context was captured.", nil
	}
	sections := make([]string, 0, 4)
	if userPrompt := strings.TrimSpace(ExtractTextFromContent(actual.UserContent)); userPrompt != "" {
		sections = append(sections, "User prompt:\n"+userPrompt)
	}
	if messages := formatGroundingMessages(actual.ContextMessages); messages != "" {
		sections = append(sections, "Context messages:\n"+messages)
	}
	toolCalls, toolOutputs, err := formatGroundingTools(actual.Tools)
	if err != nil {
		return "", fmt.Errorf("format tool calls and outputs: %w", err)
	}
	if toolCalls != "" {
		sections = append(sections, "tool_calls:\n"+toolCalls)
	}
	if toolOutputs != "" {
		sections = append(sections, "tool_outputs:\n"+toolOutputs)
	}
	if len(sections) == 0 {
		return "No validation context was captured.", nil
	}
	return strings.Join(sections, "\n\n"), nil
}

func formatGroundingMessages(messages []*model.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		text := strings.TrimSpace(ExtractTextFromContent(message))
		if text == "" {
			continue
		}
		builder.WriteString("- [")
		builder.WriteString(string(message.Role))
		builder.WriteString("] ")
		builder.WriteString(text)
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func formatGroundingTools(tools []*evalset.Tool) (string, string, error) {
	toolCalls := make([]groundingToolCall, 0, len(tools))
	toolOutputs := make([]groundingToolOutput, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if _, err := json.Marshal(tool.Arguments); err != nil {
			return "", "", fmt.Errorf("marshal tool %s arguments: %w", tool.Name, err)
		}
		if _, err := json.Marshal(tool.Result); err != nil {
			return "", "", fmt.Errorf("marshal tool %s result: %w", tool.Name, err)
		}
		toolCalls = append(toolCalls, groundingToolCall{
			ID:   tool.ID,
			Name: tool.Name,
			Args: tool.Arguments,
		})
		toolOutputs = append(toolOutputs, groundingToolOutput{
			ID:     tool.ID,
			Name:   tool.Name,
			Output: tool.Result,
		})
	}
	callsText, err := marshalGroundingSection(toolCalls)
	if err != nil {
		return "", "", err
	}
	outputsText, err := marshalGroundingSection(toolOutputs)
	if err != nil {
		return "", "", err
	}
	return callsText, outputsText, nil
}

func marshalGroundingSection(payload any) (string, error) {
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "[]" {
		return "", nil
	}
	return text, nil
}
