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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// claudeToolEvent is a normalized tool_use/tool_result pair extracted from the CLI stream.
type claudeToolEvent struct {
	Kind     string
	ToolID   string
	ToolName string
	Input    any
	Result   any
}

// normalizedCalculatorToolArguments is the canonical argument shape used for deterministic matching.
type normalizedCalculatorToolArguments struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

// parseClaudeToolEvents extracts tool_use/tool_result blocks from the CLI JSON output.
func parseClaudeToolEvents(rawOutput string) ([]claudeToolEvent, error) {
	jsonPayload, err := findFirstCompleteJSONValue([]byte(rawOutput))
	if err != nil {
		return nil, err
	}

	var entries []claudeCLIEvent
	if err := json.Unmarshal(jsonPayload, &entries); err != nil {
		return nil, fmt.Errorf("parse claude json output: %w", err)
	}

	events := make([]claudeToolEvent, 0)
	toolNames := make(map[string]string)

	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		for _, block := range entry.Message.Content {
			switch strings.TrimSpace(block.Type) {
			case "tool_use":
				toolID := strings.TrimSpace(block.ID)
				if toolID == "" {
					continue
				}
				toolName := strings.TrimSpace(block.Name)
				if toolName == "" {
					toolName = "<unknown>"
				}
				toolNames[toolID] = toolName
				events = append(events, claudeToolEvent{
					Kind:     "tool_use",
					ToolID:   toolID,
					ToolName: toolName,
					Input:    block.Input,
				})
			case "tool_result":
				toolID := strings.TrimSpace(block.ToolUseID)
				if toolID == "" {
					continue
				}
				events = append(events, claudeToolEvent{
					Kind:     "tool_result",
					ToolID:   toolID,
					ToolName: toolNames[toolID],
					Result:   block.Content,
				})
			}
		}
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("no tool events found")
	}
	return events, nil
}

// emitClaudeToolEvents converts parsed tool events into framework response events so metrics can inspect tool usage.
func emitClaudeToolEvents(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event, author string, toolEvents []claudeToolEvent) {
	if invocation == nil {
		return
	}
	for _, toolEvent := range toolEvents {
		switch toolEvent.Kind {
		case "tool_use":
			args := marshalJSONOrJSONString(normalizeToolCallArguments(toolEvent.ToolName, toolEvent.Input))
			tc := model.ToolCall{
				Type: "function",
				ID:   toolEvent.ToolID,
				Function: model.FunctionDefinitionParam{
					Name:      toolEvent.ToolName,
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
		case "tool_result":
			content := extractToolResultText(toolEvent.Result)
			rsp := &model.Response{
				Object: model.ObjectTypeToolResponse,
				Done:   false,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:     model.RoleTool,
							ToolID:   toolEvent.ToolID,
							ToolName: toolEvent.ToolName,
							Content:  content,
						},
					},
				},
			}
			agent.EmitEvent(ctx, invocation, out, event.NewResponseEvent(invocation.InvocationID, author, rsp))
		}
	}
}

// normalizeToolCallArguments canonicalizes tool arguments into a deterministic shape for matching.
func normalizeToolCallArguments(toolName string, input any) any {
	if strings.TrimSpace(toolName) != "mcp__"+claudeMCPServerName+"__calculator" {
		return input
	}

	raw, err := json.Marshal(input)
	if err != nil {
		return input
	}

	var args calculatorArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return input
	}
	if args.Operation == nil || strings.TrimSpace(*args.Operation) == "" {
		return input
	}
	if args.A == nil || args.B == nil {
		return input
	}

	return normalizedCalculatorToolArguments{
		Operation: strings.TrimSpace(*args.Operation),
		A:         float64(*args.A),
		B:         float64(*args.B),
	}
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
	return stringifyJSON(v)
}

// findFirstCompleteJSONValue returns the first complete JSON value found in a mixed output stream.
// We capture both stdout and stderr, so non-JSON logs can appear before or after the real CLI JSON payload.
// The Claude CLI is expected to output exactly one JSON array/object, so we intentionally keep only the first one.
func findFirstCompleteJSONValue(output []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("json payload is empty")
	}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		return decodeCompleteJSONValuePrefix(trimmed)
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != '[' && trimmed[i] != '{' {
			continue
		}
		candidate := bytes.TrimSpace(trimmed[i:])
		value, err := decodeCompleteJSONValuePrefix(candidate)
		if err == nil {
			return value, nil
		}
	}
	return nil, fmt.Errorf("json payload not found")
}

// decodeCompleteJSONValuePrefix decodes a single JSON value from the start of candidate and returns its exact byte range.
// This allows callers to ignore any trailing bytes that may exist in a combined stdout/stderr stream.
func decodeCompleteJSONValuePrefix(candidate []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(candidate))
	var msg json.RawMessage
	if err := dec.Decode(&msg); err != nil {
		return nil, err
	}
	end := dec.InputOffset()
	if end <= 0 || end > int64(len(candidate)) {
		return nil, fmt.Errorf("invalid json payload size")
	}
	return candidate[:end], nil
}

// marshalJSONOrJSONString returns a JSON encoding of v and falls back to encoding its string form.
func marshalJSONOrJSONString(v any) []byte {
	if v == nil {
		return []byte("{}")
	}
	b, err := json.Marshal(v)
	if err == nil {
		return b
	}
	fallback, fallbackErr := json.Marshal(fmt.Sprintf("%v", v))
	if fallbackErr == nil {
		return fallback
	}
	return []byte("{}")
}

// stringifyJSON converts v into a JSON string when possible and falls back to fmt formatting.
func stringifyJSON(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}
