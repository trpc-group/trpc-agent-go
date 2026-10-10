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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/util/message"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	invalidToolCallTag     = "[invalid_tool_call]"
	invalidToolResultTag   = "[invalid_tool_result]"
	orphanToolResultTag    = "[orphan_tool_result]"
	interruptedToolCallTag = "[interrupted_tool_call]"
)

var (
	errArgumentsNotValidJSON = errors.New("arguments are not valid JSON")
	errFunctionNameEmpty     = errors.New("function name is empty")
	errDuplicateToolCallID   = errors.New("tool call id is duplicated in the same assistant message")
)

// SanitizeMessagesWithTools downgrades invalid tool calls and tool results into user messages.
//
// Some model providers require tool call function names to be non-empty and arguments to be
// valid JSON, often a JSON object. When a model produces an invalid tool call (for example, an
// empty function name, malformed JSON, or a value that does not match the tool input schema),
// the tool call can poison the conversation history and cause future model requests to fail
// (e.g., HTTP 400 Bad Request). This function removes such tool calls from assistant messages
// and emits equivalent user messages that preserve the original payload for context.
//
// A valid tool call whose result never arrived — typically because the run was interrupted
// before the tool finished — is instead PAIRED with a synthetic tool-role result tagged
// [interrupted_tool_call], so the conversation keeps a valid assistant(tool_calls) →
// tool(result) round and the model sees the interruption as a tool outcome rather than as
// user text. Downgrading such calls to user messages distorted the conversation roles and
// was repeated on every request that replayed the interrupted history.
//
// This function also downgrades orphan tool result messages that are not associated with a
// kept tool call message, to avoid invalid tool message sequences in strict chat APIs.
// The context is used to attach request-scoped metadata to downgrade warnings.
func SanitizeMessagesWithTools(ctx context.Context, messages []model.Message, tools map[string]tool.Tool) []model.Message {
	return SanitizeMessagesWithToolsResult(ctx, messages, tools).Messages
}

// SanitizeResult contains sanitized messages and their input provenance.
// When non-nil, SourceIndexes has one entry per message and identifies the
// corresponding index in the input message slice. A nil slice represents an
// identity mapping: output message i came from input message i.
type SanitizeResult struct {
	Messages      []model.Message
	SourceIndexes []int
}

// SanitizeMessagesWithToolsResult sanitizes messages and preserves the input
// index from which every output message was derived.
func SanitizeMessagesWithToolsResult(
	ctx context.Context,
	messages []model.Message,
	tools map[string]tool.Tool,
) SanitizeResult {
	if len(messages) == 0 {
		return SanitizeResult{Messages: messages}
	}
	if !containsToolMessages(messages) {
		out := make([]model.Message, len(messages))
		copy(out, messages)
		return SanitizeResult{Messages: out}
	}
	builder := sanitizeResultBuilder{
		messages: make([]model.Message, 0, len(messages)),
	}
	for i := 0; i < len(messages); {
		msg := messages[i]
		if msg.Role == model.RoleAssistant && len(msg.ToolCalls) > 0 {
			next := i + 1
			for next < len(messages) && messages[next].Role == model.RoleTool {
				next++
			}
			builder.appendAll(sanitizeToolRound(
				ctx,
				indexedMessage{message: msg, sourceIndex: i},
				messages[i+1:next],
				i+1,
				tools,
			))
			i = next
			continue
		}
		if msg.Role == model.RoleTool {
			builder.append(indexedMessage{
				message:     downgradeOrphanToolResult(ctx, msg),
				sourceIndex: i,
			})
			i++
			continue
		}
		builder.append(indexedMessage{message: msg, sourceIndex: i})
		i++
	}
	return SanitizeResult{
		Messages:      builder.messages,
		SourceIndexes: builder.sourceIndexes,
	}
}

func containsToolMessages(messages []model.Message) bool {
	for i := range messages {
		if messages[i].Role == model.RoleTool ||
			(messages[i].Role == model.RoleAssistant &&
				len(messages[i].ToolCalls) > 0) {
			return true
		}
	}
	return false
}

type indexedMessage struct {
	message     model.Message
	sourceIndex int
}

type sanitizeResultBuilder struct {
	messages      []model.Message
	sourceIndexes []int
}

func (b *sanitizeResultBuilder) append(message indexedMessage) {
	outputIndex := len(b.messages)
	b.messages = append(b.messages, message.message)
	if b.sourceIndexes != nil {
		b.sourceIndexes = append(b.sourceIndexes, message.sourceIndex)
		return
	}
	if message.sourceIndex == outputIndex {
		return
	}
	b.sourceIndexes = make([]int, outputIndex, cap(b.messages))
	for i := range b.sourceIndexes {
		b.sourceIndexes[i] = i
	}
	b.sourceIndexes = append(b.sourceIndexes, message.sourceIndex)
}

func (b *sanitizeResultBuilder) appendAll(messages []indexedMessage) {
	for _, message := range messages {
		b.append(message)
	}
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
	kept        []indexedMessage
	invalidByID map[string][]indexedMessage
	orphan      []indexedMessage
}

// sanitizeToolRound sanitizes a single assistant tool-call round with its following tool results.
func sanitizeToolRound(
	ctx context.Context,
	assistant indexedMessage,
	toolResults []model.Message,
	firstToolResultSourceIndex int,
	tools map[string]tool.Tool,
) []indexedMessage {
	validation := validateToolCalls(assistant.message.ToolCalls, tools)
	split := splitToolResults(
		toolResults,
		firstToolResultSourceIndex,
		validation.validIDs,
		validation.invalidIDs,
	)
	// A valid call is answered when one of the kept results carries its ID.
	// Classification walks validation.validToolCalls in ORIGINAL order so the
	// assistant message's tool_calls sequence is preserved whether a call is
	// answered or orphaned (an orphan-first batch must not be reordered to
	// answered-first).
	respondedIDs := make(map[string]struct{}, len(split.kept))
	for _, tr := range split.kept {
		if tr.message.ToolID != "" {
			respondedIDs[tr.message.ToolID] = struct{}{}
		}
	}
	var orphansInOrder []model.ToolCall
	filteredAssistant := assistant.message
	filteredAssistant.ToolCalls = append(
		[]model.ToolCall(nil),
		validation.validToolCalls...,
	)
	for _, tc := range validation.validToolCalls {
		if tc.ID == "" {
			orphansInOrder = append(orphansInOrder, tc)
			continue
		}
		if _, answered := respondedIDs[tc.ID]; !answered {
			orphansInOrder = append(orphansInOrder, tc)
		}
	}
	if len(filteredAssistant.ToolCalls) == 0 {
		filteredAssistant.ToolCalls = nil
	}
	out := make(
		[]indexedMessage,
		0,
		1+len(toolResults)+len(validation.invalidToolCalls)+len(orphansInOrder)+len(split.orphan),
	)
	if !message.IsEmptyAssistantMessage(filteredAssistant) {
		out = append(out, indexedMessage{
			message:     filteredAssistant,
			sourceIndex: assistant.sourceIndex,
		})
		out = append(out, split.kept...)
		// Orphan tool calls — valid calls whose result never arrived,
		// typically because the run was interrupted mid-execution — stay in
		// the assistant message above and are answered here by synthetic
		// interrupted results, in the same original order. This keeps a
		// wire-valid tool round and the correct role for the model, instead
		// of downgrading the call to a user message on every request that
		// replays the interrupted history.
		for _, orphanCall := range orphansInOrder {
			out = append(out, indexedMessage{
				message:     synthesizeInterruptedToolResult(ctx, orphanCall),
				sourceIndex: assistant.sourceIndex,
			})
		}
	}
	for _, invalid := range validation.invalidToolCalls {
		out = append(out, indexedMessage{
			message: downgradeInvalidToolCall(
				ctx,
				invalid.call,
				invalid.reason,
			),
			sourceIndex: assistant.sourceIndex,
		})
		for _, tr := range split.invalidByID[invalid.call.ID] {
			out = append(out, indexedMessage{
				message:     downgradeInvalidToolResult(ctx, tr.message),
				sourceIndex: tr.sourceIndex,
			})
		}
	}
	for _, orphan := range split.orphan {
		out = append(out, indexedMessage{
			message:     downgradeOrphanToolResult(ctx, orphan.message),
			sourceIndex: orphan.sourceIndex,
		})
	}
	return out
}

// validateToolCalls validates tool call arguments and groups tool calls by validity.
// A non-empty call ID may be claimed only once per assistant message: later
// calls repeating a claimed ID are reported invalid so the sanitizer
// downgrades them, keeping the retained tool calls (and any synthetic
// results paired with them) unique by ID on the wire.
func validateToolCalls(toolCalls []model.ToolCall, tools map[string]tool.Tool) toolCallValidation {
	out := toolCallValidation{
		validToolCalls: make([]model.ToolCall, 0, len(toolCalls)),
		validIDs:       make(map[string]struct{}),
		invalidIDs:     make(map[string]struct{}),
	}
	seenIDs := make(map[string]struct{}, len(toolCalls))
	for _, tc := range toolCalls {
		if tc.ID != "" {
			if _, dup := seenIDs[tc.ID]; dup {
				out.invalidToolCalls = append(out.invalidToolCalls, invalidToolCall{
					call:   tc,
					reason: errDuplicateToolCallID.Error(),
				})
				out.invalidIDs[tc.ID] = struct{}{}
				continue
			}
			seenIDs[tc.ID] = struct{}{}
		}
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
	if strings.TrimSpace(tc.Function.Name) == "" {
		return tc, false, errFunctionNameEmpty.Error()
	}
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
		return validateStringValueAgainstSchema(value, schema, path)
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

func validateStringValueAgainstSchema(value any, schema *tool.Schema, path string) (bool, string) {
	str, ok := value.(string)
	if !ok {
		return false, fmt.Sprintf("expected string at %s", path)
	}
	if schema == nil || schema.Pattern == "" {
		return true, ""
	}
	re, err := regexp.Compile(schema.Pattern)
	if err != nil {
		// JSON Schema patterns use ECMA-262 syntax; Go regexp supports a subset.
		return true, ""
	}
	if re.MatchString(str) {
		return true, ""
	}
	return false, fmt.Sprintf("string at %s does not match pattern", path)
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
func splitToolResults(
	toolResults []model.Message,
	firstSourceIndex int,
	validIDs map[string]struct{},
	invalidIDs map[string]struct{},
) toolResultSplit {
	out := toolResultSplit{
		kept:        make([]indexedMessage, 0, len(toolResults)),
		invalidByID: make(map[string][]indexedMessage),
	}
	respondedValidIDs := make(map[string]struct{}, len(validIDs))
	for i, message := range toolResults {
		tr := indexedMessage{
			message:     message,
			sourceIndex: firstSourceIndex + i,
		}
		if message.ToolID == "" {
			out.orphan = append(out.orphan, tr)
			continue
		}
		if _, ok := validIDs[message.ToolID]; ok {
			if _, responded := respondedValidIDs[message.ToolID]; responded {
				out.orphan = append(out.orphan, tr)
				continue
			}
			respondedValidIDs[message.ToolID] = struct{}{}
			out.kept = append(out.kept, tr)
			continue
		}
		if _, ok := invalidIDs[message.ToolID]; ok {
			out.invalidByID[message.ToolID] = append(
				out.invalidByID[message.ToolID],
				tr,
			)
			continue
		}
		out.orphan = append(out.orphan, tr)
	}
	return out
}

// downgradeInvalidToolCall converts an invalid tool call into a user message that preserves its payload.
func downgradeInvalidToolCall(ctx context.Context, call model.ToolCall, reason string) model.Message {
	log.WarnfContext(ctx,
		"toolcall: downgraded invalid tool call to user message: name=%q id=%q reason=%q",
		call.Function.Name,
		call.ID,
		reason,
	)
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

// synthesizeInterruptedToolResult answers an orphan tool call — a valid call
// whose result never arrived — with a synthetic tool-role result, keeping a
// valid tool round in the conversation. The result deliberately does NOT
// claim the call did not complete: a missing result only proves the result
// is unavailable, and the tool may have taken effect before the result was
// lost, so the model is told the execution status is unknown and must
// verify any effects before re-issuing a side-effecting call. The result
// carries the call id and tool name but not the original arguments: the
// assistant message already preserves them, and repeating the payload on
// every request that replays this history would bloat the context and
// could re-emit sensitive argument content.
func synthesizeInterruptedToolResult(ctx context.Context, call model.ToolCall) model.Message {
	log.WarnfContext(ctx,
		"toolcall: paired orphan tool call with synthetic interrupted result: name=%q id=%q",
		call.Function.Name,
		call.ID,
	)
	content := fmt.Sprintf(
		"%s The result of this tool call is unavailable: execution was interrupted or the result was lost before it was delivered. The execution status of the call is unknown, so verify any effects before re-issuing a side-effecting call. The call and its arguments are preserved in the preceding assistant message.",
		interruptedToolCallTag,
	)
	return model.Message{
		Role:     model.RoleTool,
		ToolID:   call.ID,
		ToolName: call.Function.Name,
		Content:  content,
	}
}

// downgradeInvalidToolResult converts a tool result associated with an invalid tool call into a user message.
func downgradeInvalidToolResult(ctx context.Context, msg model.Message) model.Message {
	log.WarnfContext(ctx,
		"toolcall: downgraded invalid tool result to user message: tool_name=%q tool_call_id=%q",
		msg.ToolName,
		msg.ToolID,
	)
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
func downgradeOrphanToolResult(ctx context.Context, msg model.Message) model.Message {
	log.WarnfContext(ctx,
		"toolcall: downgraded orphan tool result to user message: tool_name=%q tool_call_id=%q",
		msg.ToolName,
		msg.ToolID,
	)
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
