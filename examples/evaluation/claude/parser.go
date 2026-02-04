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

	trimmed := strings.TrimSpace(rawOutput)
	if trimmed == "" {
		return
	}
	jsonStart := strings.IndexAny(trimmed, "[{")
	if jsonStart < 0 {
		return
	}
	trimmed = trimmed[jsonStart:]

	dec := json.NewDecoder(strings.NewReader(trimmed))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return
	}

	var entries []claudeCLIEvent
	if err := json.Unmarshal(raw, &entries); err != nil {
		return
	}

	toolNames := make(map[string]string)
	calculatorToolName := "mcp__" + claudeMCPServerName + "__calculator"

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

				args := []byte("{}")
				if block.Input != nil {
					if toolName == calculatorToolName {
						if rawInput, err := json.Marshal(block.Input); err == nil {
							var parsed calculatorArgs
							if err := json.Unmarshal(rawInput, &parsed); err == nil && parsed.Operation != nil && parsed.A != nil && parsed.B != nil {
								if normalized, err := json.Marshal(calculatorArgsForEval{
									Operation: strings.TrimSpace(*parsed.Operation),
									A:         *parsed.A,
									B:         *parsed.B,
								}); err == nil {
									args = normalized
								}
							}
						}
					} else if marshaled, err := json.Marshal(block.Input); err == nil {
						args = marshaled
					}
				}

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
			case "tool_result":
				toolID := strings.TrimSpace(block.ToolUseID)
				if toolID == "" {
					continue
				}
				toolName := toolNames[toolID]
				if strings.TrimSpace(toolName) == "" {
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
		}
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
	raw, err := json.Marshal(v)
	if err == nil {
		return string(raw)
	}
	return ""
}
