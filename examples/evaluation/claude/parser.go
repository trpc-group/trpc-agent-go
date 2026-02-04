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
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// claudeCLIEvent represents one top-level record produced by the CLI JSON output.
type claudeCLIEvent struct {
	Type    string            `json:"type,omitempty"`
	Subtype string            `json:"subtype,omitempty"`
	Message *claudeCLIMessage `json:"message,omitempty"`
	Result  string            `json:"result,omitempty"`
}

// claudeCLIMessage carries content blocks for an assistant/user message record.
type claudeCLIMessage struct {
	Content []claudeCLIContentBlock `json:"content,omitempty"`
}

// claudeCLIContentBlock is a single item inside the message content array.
type claudeCLIContentBlock struct {
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	Text      string `json:"text,omitempty"`
}

// calculatorArgsForEval is the canonical argument shape used for deterministic matching.
type calculatorArgsForEval struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

// emitClaudeToolEvents parses the CLI JSON output and emits tool-call and tool-result events.
func emitClaudeToolEvents(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event, author, rawOutput string) {
	if invocation == nil {
		return
	}

	entries, ok := parseClaudeCLIEvents(rawOutput)
	if !ok {
		return
	}

	toolNames := make(map[string]string)

	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		for _, block := range entry.Message.Content {
			switch strings.TrimSpace(block.Type) {
			case "tool_use":
				handleToolUseBlock(ctx, invocation, out, author, toolNames, block)
			case "tool_result":
				handleToolResultBlock(ctx, invocation, out, author, toolNames, block)
			}
		}
	}
}

// parseClaudeCLIEvents extracts the first CLI JSON payload and decodes it into events.
func parseClaudeCLIEvents(rawOutput string) ([]claudeCLIEvent, bool) {
	trimmed := strings.TrimSpace(rawOutput)
	if trimmed == "" {
		return nil, false
	}
	jsonStart := strings.IndexAny(trimmed, "[{")
	if jsonStart < 0 {
		return nil, false
	}
	trimmed = trimmed[jsonStart:]

	dec := json.NewDecoder(strings.NewReader(trimmed))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, false
	}

	var entries []claudeCLIEvent
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false
	}
	return entries, true
}

// handleToolUseBlock emits a tool-call event for a tool_use content block.
func handleToolUseBlock(
	ctx context.Context,
	invocation *agent.Invocation,
	out chan<- *event.Event,
	author string,
	toolNames map[string]string,
	block claudeCLIContentBlock,
) {
	toolID := strings.TrimSpace(block.ID)
	if toolID == "" {
		return
	}
	toolName := strings.TrimSpace(block.Name)
	if toolName == "" {
		toolName = "<unknown>"
	}
	toolNames[toolID] = toolName

	args := marshalToolUseArguments(toolName, block.Input)
	tc := model.ToolCall{
		Type: "function",
		ID:   toolID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: args,
		},
	}
	rsp := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{tc},
				},
			},
		},
	}
	agent.EmitEvent(ctx, invocation, out, event.NewResponseEvent(invocation.InvocationID, author, rsp))
}

// handleToolResultBlock emits a tool-result event for a tool_result content block.
func handleToolResultBlock(
	ctx context.Context,
	invocation *agent.Invocation,
	out chan<- *event.Event,
	author string,
	toolNames map[string]string,
	block claudeCLIContentBlock,
) {
	toolID := strings.TrimSpace(block.ToolUseID)
	if toolID == "" {
		return
	}
	toolName := strings.TrimSpace(toolNames[toolID])
	if toolName == "" {
		toolName = "<unknown>"
	}
	rsp := &model.Response{
		Object: model.ObjectTypeToolResponse,
		Done:   false,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   toolID,
					ToolName: toolName,
					Content:  extractToolResultText(block.Content),
				},
			},
		},
	}
	agent.EmitEvent(ctx, invocation, out, event.NewResponseEvent(invocation.InvocationID, author, rsp))
}

// marshalToolUseArguments serializes tool arguments and applies per-tool normalization when needed.
func marshalToolUseArguments(toolName string, input any) []byte {
	if input == nil {
		return []byte("{}")
	}
	if toolName == claudeCalculatorMCPToolName {
		if normalized, ok := marshalCalculatorArgumentsForEval(input); ok {
			return normalized
		}
	}
	args, err := json.Marshal(input)
	if err != nil {
		return []byte("{}")
	}
	return args
}

// marshalCalculatorArgumentsForEval converts calculator args into a deterministic JSON encoding.
func marshalCalculatorArgumentsForEval(input any) ([]byte, bool) {
	rawInput, err := json.Marshal(input)
	if err != nil {
		return nil, false
	}
	var parsed calculatorArgs
	if err := json.Unmarshal(rawInput, &parsed); err != nil {
		return nil, false
	}
	if parsed.Operation == nil || parsed.A == nil || parsed.B == nil {
		return nil, false
	}
	normalized := calculatorArgsForEval{
		Operation: strings.TrimSpace(*parsed.Operation),
		A:         *parsed.A,
		B:         *parsed.B,
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, false
	}
	return data, true
}

// extractToolResultText extracts the human-readable payload from a tool_result content block.
func extractToolResultText(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if blocks, ok := v.([]any); ok {
		var sb strings.Builder
		for _, block := range blocks {
			m, ok := block.(map[string]any)
			if !ok {
				continue
			}
			text, ok := m["text"].(string)
			if ok {
				sb.WriteString(text)
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	if m, ok := v.(map[string]any); ok {
		if text, ok := m["text"].(string); ok {
			return text
		}
	}
	raw, err := json.Marshal(v)
	if err == nil {
		return string(raw)
	}
	return ""
}
