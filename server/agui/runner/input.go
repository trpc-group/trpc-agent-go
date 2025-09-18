//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// NOTE: This file should be removed when the AG-UI Go SDK exposes the official structure.

// RunAgentInput captures the parameters for an AG-UI run request.
// NOTE: This type should be removed when the AG-UI Go SDK exposes the official structure.
type RunAgentInput struct {
	ThreadID       string
	RunID          string
	Messages       []model.Message
	State          map[string]any
	ForwardedProps map[string]any
}

// UserID returns the derived user identifier.
// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func (in *RunAgentInput) UserID() string {
	return in.ThreadID
}

// SessionID returns the derived session identifier.
// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func (in *RunAgentInput) SessionID() string {
	return in.RunID
}

// LatestUserMessage returns the most recent user message with content.
// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func (in *RunAgentInput) LatestUserMessage() (model.Message, bool) {
	for i := len(in.Messages) - 1; i >= 0; i-- {
		msg := in.Messages[i]
		if msg.Role == model.RoleUser && (msg.Content != "" || len(msg.ContentParts) > 0) {
			return msg, true
		}
	}
	return model.Message{}, false
}

// DecodeRunAgentInput decodes a RunAgentInput from an HTTP request body.
// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func DecodeRunAgentInput(r io.Reader) (*RunAgentInput, error) {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, errors.New("agui: decode request failed: " + err.Error())
	}

	input := &RunAgentInput{
		ThreadID:       readStringRaw(raw, "threadId", "thread_id"),
		RunID:          readStringRaw(raw, "runId", "run_id"),
		State:          decodeMap(raw, "state"),
		ForwardedProps: decodeMap(raw, "forwardedProps", "forwarded_props"),
	}

	if msgRaw, ok := raw["messages"]; ok {
		messages, err := decodeMessages(msgRaw)
		if err != nil {
			return nil, err
		}
		input.Messages = messages
	} else {
		return nil, errors.New("agui: messages field is required")
	}

	if _, ok := input.LatestUserMessage(); !ok {
		return nil, errors.New("agui: at least one user message with content is required")
	}

	return input, nil
}

// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func readStringRaw(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if data, ok := raw[key]; ok {
			var s string
			if err := json.Unmarshal(data, &s); err == nil {
				return s
			}
		}
	}
	return ""
}

// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func decodeMap(raw map[string]json.RawMessage, keys ...string) map[string]any {
	for _, key := range keys {
		if data, ok := raw[key]; ok && len(data) > 0 {
			var m map[string]any
			if err := json.Unmarshal(data, &m); err == nil {
				return m
			}
		}
	}
	return nil
}

// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func decodeMessages(data json.RawMessage) ([]model.Message, error) {
	var rawMsgs []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMsgs); err != nil {
		return nil, errors.New("agui: messages must be an array of objects: " + err.Error())
	}

	msgs := make([]model.Message, 0, len(rawMsgs))
	for _, item := range rawMsgs {
		role := strings.ToLower(readStringRaw(item, "role"))
		if role == "" {
			return nil, errors.New("agui: message.role is required")
		}

		msg := model.Message{Role: model.Role(role)}
		if !msg.Role.IsValid() {
			return nil, errors.New("agui: unsupported message role " + role)
		}

		msg.Content = readStringRaw(item, "content")
		if msg.Content == "" {
			msg.Content = decodeTextParts(item, "content", "parts", "contentParts")
		}
		if msg.Content == "" {
			msg.Content = readStringRaw(item, "delta")
		}

		if msg.Role == model.RoleTool {
			msg.ToolID = readStringRaw(item, "toolId", "tool_id")
			msg.ToolName = readStringRaw(item, "toolName", "tool_name")
		}

		if toolCallsRaw, ok := findRaw(item, "toolCalls", "tool_calls"); ok {
			msg.ToolCalls = decodeToolCalls(toolCallsRaw)
		}

		msgs = append(msgs, msg)
	}

	return msgs, nil
}

// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func decodeTextParts(item map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if data, ok := item[key]; ok {
			var parts []map[string]any
			if err := json.Unmarshal(data, &parts); err == nil {
				var builder strings.Builder
				for _, part := range parts {
					typeVal, _ := part["type"].(string)
					if strings.ToLower(typeVal) != "text" {
						continue
					}
					if text, ok := part["text"].(string); ok {
						builder.WriteString(text)
					}
				}
				if builder.Len() > 0 {
					return builder.String()
				}
			}
		}
	}
	return ""
}

// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func findRaw(item map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	for _, key := range keys {
		if data, ok := item[key]; ok {
			return data, true
		}
	}
	return nil, false
}

// NOTE: This function should be removed when the AG-UI Go SDK exposes the official structure.
func decodeToolCalls(data json.RawMessage) []model.ToolCall {
	var rawCalls []map[string]any
	if err := json.Unmarshal(data, &rawCalls); err != nil {
		return nil
	}

	toolCalls := make([]model.ToolCall, 0, len(rawCalls))
	for _, call := range rawCalls {
		var tc model.ToolCall
		tc.Type = "function"
		if id, ok := call["id"].(string); ok {
			tc.ID = id
		}
		if t, ok := call["type"].(string); ok && t != "" {
			tc.Type = t
		}
		if fn, ok := call["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				tc.Function.Name = name
			}
			if args, ok := fn["arguments"]; ok {
				if encoded, err := json.Marshal(args); err == nil {
					tc.Function.Arguments = encoded
				}
			}
		}
		toolCalls = append(toolCalls, tc)
	}
	return toolCalls
}
