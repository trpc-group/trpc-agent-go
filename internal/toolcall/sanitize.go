//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolcall provides utilities for sanitizing tool call messages.
package toolcall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	invalidToolCallTag   = "[invalid_tool_call]"
	invalidToolResultTag = "[invalid_tool_result]"
	orphanToolResultTag  = "[orphan_tool_result]"
)

var (
	errArgumentsNotValidJSON = errors.New("arguments are not valid JSON")
)

// SanitizeMessagesWithTools downgrades invalid tool calls and tool results into user messages.
//
// Some model providers require tool call arguments to be valid JSON, and often a JSON object.
// When a model produces invalid tool call arguments (for example, malformed JSON or a JSON
// value that does not match the tool input schema), the tool call can poison the conversation
// history and cause future model requests to fail (e.g., HTTP 400 Bad Request). This function
// removes such tool calls from assistant messages and emits equivalent user messages that
// preserve the original payload for context.
//
// This function also downgrades orphan tool result messages that are not associated with a
// kept tool call message to avoid invalid tool message sequences in strict chat APIs.
func SanitizeMessagesWithTools(messages []model.Message, tools map[string]tool.Tool) []model.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]model.Message, 0, len(messages))
	for i := 0; i < len(messages); {
		msg := messages[i]
		if msg.Role == model.RoleAssistant && len(msg.ToolCalls) > 0 {
			next := i + 1
			for next < len(messages) && messages[next].Role == model.RoleTool {
				next++
			}
			out = append(out, sanitizeToolRound(msg, messages[i+1:next], tools)...)
			i = next
			continue
		}
		if msg.Role == model.RoleTool {
			out = append(out, downgradeOrphanToolResult(msg))
			i++
			continue
		}
		out = append(out, msg)
		i++
	}
	return out
}

type toolCallValidation struct {
	validToolCalls   []model.ToolCall
	invalidToolCalls []invalidToolCall
	validIDs         map[string]struct{}
	invalidIDs       map[string]struct{}
}

type invalidToolCall struct {
	call   model.ToolCall
	reason string
}

type toolResultSplit struct {
	kept        []model.Message
	invalidByID map[string][]model.Message
	orphan      []model.Message
}

// sanitizeToolRound sanitizes a single assistant tool-call round with its following tool results.
func sanitizeToolRound(assistant model.Message, toolResults []model.Message, tools map[string]tool.Tool) []model.Message {
	validation := validateToolCalls(assistant.ToolCalls, tools)
	if len(validation.invalidToolCalls) == 0 {
		assistant.ToolCalls = validation.validToolCalls
		msgs := make([]model.Message, 0, 1+len(toolResults))
		msgs = append(msgs, assistant)
		msgs = append(msgs, toolResults...)
		return msgs
	}
	filteredAssistant := assistant
	filteredAssistant.ToolCalls = validation.validToolCalls
	if len(filteredAssistant.ToolCalls) == 0 {
		filteredAssistant.ToolCalls = nil
	}
	split := splitToolResults(toolResults, validation.validIDs, validation.invalidIDs)
	out := make([]model.Message, 0, 1+len(toolResults)+len(validation.invalidToolCalls)+len(split.orphan))
	if !isEmptyAssistantMessage(filteredAssistant) {
		out = append(out, filteredAssistant)
		out = append(out, split.kept...)
	}
	for _, invalid := range validation.invalidToolCalls {
		out = append(out, downgradeInvalidToolCall(invalid.call, invalid.reason))
		for _, tr := range split.invalidByID[invalid.call.ID] {
			out = append(out, downgradeInvalidToolResult(tr))
		}
	}
	for _, orphan := range split.orphan {
		out = append(out, downgradeOrphanToolResult(orphan))
	}
	return out
}

// validateToolCalls validates tool call arguments and groups tool calls by validity.
func validateToolCalls(toolCalls []model.ToolCall, tools map[string]tool.Tool) toolCallValidation {
	out := toolCallValidation{
		validToolCalls: make([]model.ToolCall, 0, len(toolCalls)),
		validIDs:       make(map[string]struct{}),
		invalidIDs:     make(map[string]struct{}),
	}
	for _, tc := range toolCalls {
		validated, ok, reason := validateToolCall(tc, tools)
		if ok {
			out.validToolCalls = append(out.validToolCalls, validated)
			out.validIDs[validated.ID] = struct{}{}
			continue
		}
		out.invalidToolCalls = append(out.invalidToolCalls, invalidToolCall{call: tc, reason: reason})
		out.invalidIDs[tc.ID] = struct{}{}
	}
	return out
}

// validateToolCall validates and normalizes a single tool call.
func validateToolCall(tc model.ToolCall, tools map[string]tool.Tool) (model.ToolCall, bool, string) {
	normalizedArgs, decoded, err := normalizeAndDecodeArguments(tc.Function.Arguments)
	if err != nil {
		return tc, false, err.Error()
	}
	if ok, reason := validateToolCallArguments(tc.Function.Name, decoded, tools); !ok {
		return tc, false, reason
	}
	tc.Function.Arguments = normalizedArgs
	return tc, true, ""
}

// normalizeAndDecodeArguments trims, normalizes, and decodes tool call arguments as a JSON value.
func normalizeAndDecodeArguments(args []byte) ([]byte, any, error) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if !json.Valid(trimmed) {
		return nil, nil, errArgumentsNotValidJSON
	}
	var decoded any
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return nil, nil, errArgumentsNotValidJSON
	}
	return trimmed, decoded, nil
}

// validateToolCallArguments validates decoded tool arguments against the tool input schema when available.
func validateToolCallArguments(toolName string, args any, tools map[string]tool.Tool) (bool, string) {
	if tools == nil {
		return true, ""
	}
	tl := tools[toolName]
	if tl == nil {
		return true, ""
	}
	decl := tl.Declaration()
	if decl == nil || decl.InputSchema == nil {
		return true, ""
	}
	return validateArgumentsAgainstSchema(args, decl.InputSchema)
}

// validateArgumentsAgainstSchema validates a decoded JSON value against a JSON schema and returns a reason on mismatch.
func validateArgumentsAgainstSchema(args any, schema *tool.Schema) (bool, string) {
	if schema == nil {
		return true, ""
	}
	if args == nil {
		defs := schema.Defs
		resolved := schema
		for resolved != nil && resolved.Ref != "" {
			next := resolveSchemaRef(resolved.Ref, defs)
			if next == nil {
				resolved = nil
				break
			}
			resolved = next
		}
		if resolved == nil {
			return true, ""
		}
		schemaType := resolved.Type
		if schemaType == "" {
			if len(resolved.Properties) > 0 {
				schemaType = "object"
			} else if resolved.Items != nil {
				schemaType = "array"
			}
		}
		switch schemaType {
		case "object":
			return false, "expected object at $"
		case "array":
			return false, "expected array at $"
		case "string":
			return false, "expected string at $"
		case "boolean":
			return false, "expected boolean at $"
		case "integer":
			return false, "expected integer at $"
		case "number":
			return false, "expected number at $"
		default:
			return true, ""
		}
	}
	ok, reason := validateValueAgainstSchema(args, schema, schema.Defs, "$")
	if ok {
		return true, ""
	}
	return false, reason
}

func inferSchemaType(schema *tool.Schema) string {
	if schema == nil {
		return ""
	}
	if schema.Type != "" {
		return schema.Type
	}
	if len(schema.Properties) > 0 {
		return "object"
	}
	if schema.Items != nil {
		return "array"
	}
	return ""
}

// validateValueAgainstSchema validates a value against a subset of JSON Schema and skips unknown schema types.
func validateValueAgainstSchema(value any, schema *tool.Schema, defs map[string]*tool.Schema, path string) (bool, string) {
	if schema == nil || value == nil {
		return true, ""
	}
	if schema.Ref != "" {
		resolved := resolveSchemaRef(schema.Ref, defs)
		if resolved == nil {
			return true, ""
		}
		return validateValueAgainstSchema(value, resolved, defs, path)
	}
	switch inferSchemaType(schema) {
	case "object":
		return validateObjectValueAgainstSchema(value, schema, defs, path)
	case "array":
		return validateArrayValueAgainstSchema(value, schema, defs, path)
	case "string":
		return validateStringValueAgainstSchema(value, path)
	case "boolean":
		return validateBooleanValueAgainstSchema(value, path)
	case "integer":
		return validateIntegerValueAgainstSchema(value, path)
	case "number":
		return validateNumberValueAgainstSchema(value, path)
	default:
		return true, ""
	}
}

func validateObjectValueAgainstSchema(value any, schema *tool.Schema, defs map[string]*tool.Schema, path string) (bool, string) {
	asMap, ok := value.(map[string]any)
	if !ok {
		return false, fmt.Sprintf("expected object at %s", path)
	}
	for key, propSchema := range schema.Properties {
		propValue, exists := asMap[key]
		if !exists {
			continue
		}
		ok2, reason := validateValueAgainstSchema(propValue, propSchema, defs, path+"."+key)
		if !ok2 {
			return false, reason
		}
	}
	return true, ""
}

func validateArrayValueAgainstSchema(value any, schema *tool.Schema, defs map[string]*tool.Schema, path string) (bool, string) {
	asSlice, ok := value.([]any)
	if !ok {
		return false, fmt.Sprintf("expected array at %s", path)
	}
	if schema.Items == nil {
		return true, ""
	}
	for i, item := range asSlice {
		ok2, reason := validateValueAgainstSchema(item, schema.Items, defs, fmt.Sprintf("%s[%d]", path, i))
		if !ok2 {
			return false, reason
		}
	}
	return true, ""
}

func validateStringValueAgainstSchema(value any, path string) (bool, string) {
	if _, ok := value.(string); ok {
		return true, ""
	}
	return false, fmt.Sprintf("expected string at %s", path)
}

func validateBooleanValueAgainstSchema(value any, path string) (bool, string) {
	if _, ok := value.(bool); ok {
		return true, ""
	}
	return false, fmt.Sprintf("expected boolean at %s", path)
}

func validateIntegerValueAgainstSchema(value any, path string) (bool, string) {
	num, ok := value.(json.Number)
	if !ok {
		return false, fmt.Sprintf("expected integer at %s", path)
	}
	if _, err := num.Int64(); err != nil {
		return false, fmt.Sprintf("expected integer at %s", path)
	}
	return true, ""
}

func validateNumberValueAgainstSchema(value any, path string) (bool, string) {
	num, ok := value.(json.Number)
	if !ok {
		return false, fmt.Sprintf("expected number at %s", path)
	}
	if _, err := num.Float64(); err != nil {
		return false, fmt.Sprintf("expected number at %s", path)
	}
	return true, ""
}

// splitToolResults groups tool result messages by tool_call_id based on tool call validity.
func splitToolResults(toolResults []model.Message, validIDs map[string]struct{}, invalidIDs map[string]struct{}) toolResultSplit {
	out := toolResultSplit{
		kept:        make([]model.Message, 0, len(toolResults)),
		invalidByID: make(map[string][]model.Message),
	}
	for _, tr := range toolResults {
		if tr.ToolID == "" {
			out.orphan = append(out.orphan, tr)
			continue
		}
		if _, ok := validIDs[tr.ToolID]; ok {
			out.kept = append(out.kept, tr)
			continue
		}
		if _, ok := invalidIDs[tr.ToolID]; ok {
			out.invalidByID[tr.ToolID] = append(out.invalidByID[tr.ToolID], tr)
			continue
		}
		out.orphan = append(out.orphan, tr)
	}
	return out
}

// downgradeInvalidToolCall converts an invalid tool call into a user message that preserves its payload.
func downgradeInvalidToolCall(call model.ToolCall, reason string) model.Message {
	content := fmt.Sprintf(
		"%s Tool call arguments were downgraded to a user message (%s).\nname: %s\nid: %s\narguments:\n```text\n%s\n```",
		invalidToolCallTag,
		reason,
		call.Function.Name,
		call.ID,
		string(call.Function.Arguments),
	)
	return model.Message{
		Role:    model.RoleUser,
		Content: content,
	}
}

// downgradeInvalidToolResult converts a tool result associated with an invalid tool call into a user message.
func downgradeInvalidToolResult(msg model.Message) model.Message {
	content := fmt.Sprintf(
		"%s Tool result was downgraded to a user message.\ntool_call_id: %s\ntool_name: %s\ncontent:\n```text\n%s\n```",
		invalidToolResultTag,
		msg.ToolID,
		msg.ToolName,
		msg.Content,
	)
	return model.Message{
		Role:    model.RoleUser,
		Content: content,
	}
}

// downgradeOrphanToolResult converts an orphaned tool result into a user message.
func downgradeOrphanToolResult(msg model.Message) model.Message {
	content := fmt.Sprintf(
		"%s Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: %s\ntool_name: %s\ncontent:\n```text\n%s\n```",
		orphanToolResultTag,
		msg.ToolID,
		msg.ToolName,
		msg.Content,
	)
	return model.Message{
		Role:    model.RoleUser,
		Content: content,
	}
}

// isEmptyAssistantMessage reports whether an assistant message has no visible content and no tool calls.
func isEmptyAssistantMessage(msg model.Message) bool {
	if msg.Role != model.RoleAssistant {
		return false
	}
	return msg.Content == "" &&
		len(msg.ContentParts) == 0 &&
		len(msg.ToolCalls) == 0 &&
		msg.ReasoningContent == ""
}

// resolveSchemaRef resolves a local JSON schema #/$defs reference.
func resolveSchemaRef(ref string, defs map[string]*tool.Schema) *tool.Schema {
	if defs == nil {
		return nil
	}
	const prefix = "#/$defs/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	name := strings.TrimPrefix(ref, prefix)
	if name == "" {
		return nil
	}
	return defs[name]
}
