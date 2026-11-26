//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata/responseshistory"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/statecopy"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// SupportsProviderHistory reports whether completed history from protocol can
// be projected to Gemini's content format.
func (m *Model) SupportsProviderHistory(protocol string) bool {
	return protocol == responseshistory.Protocol
}

// prepareProviderHistory projects native records without changing the request
// or session. Histories without provider data continue through the legacy codec.
func (m *Model) prepareProviderHistory(messages []model.Message) ([]model.Message, bool, error) {
	if !providerdata.MessagesHaveData(messages) {
		return messages, false, nil
	}
	prepared := make([]model.Message, 0, len(messages))
	var projection responseshistory.Projector
	for i, message := range messages {
		if message.Role != model.RoleTool {
			if err := projection.Finish(); err != nil {
				return nil, false, fmt.Errorf("gemini history message %d: %w", i, err)
			}
		}
		if message.ProviderData != nil {
			projected, include, err := projection.ProjectMessage(message)
			if err != nil {
				return nil, false, fmt.Errorf("gemini history message %d: %w", i, err)
			}
			if !include {
				continue
			}
			message = projected
		} else {
			message = statecopy.Message(message)
		}
		parts := make([]model.ContentPart, 0, len(message.ContentParts))
		for _, part := range message.ContentParts {
			if emptyHistoryPart(part) {
				continue
			}
			if err := validateHistoryPart(message.Role, part); err != nil {
				return nil, false, fmt.Errorf("gemini history message %d: %w", i, err)
			}
			parts = append(parts, part)
		}
		message.ContentParts = parts
		if message.Role != model.RoleTool && message.Content == "" && len(parts) == 0 && len(message.ToolCalls) == 0 {
			continue
		}
		switch message.Role {
		case model.RoleAssistant, model.RoleUser, model.RoleSystem, model.RoleTool:
		default:
			return nil, false, fmt.Errorf("gemini history message %d: unsupported role %q", i, message.Role)
		}
		if message.Role != model.RoleAssistant && len(message.ToolCalls) != 0 {
			return nil, false, fmt.Errorf("gemini history message %d: function calls require the assistant role", i)
		}
		prepared = append(prepared, message)
	}
	if err := projection.Finish(); err != nil {
		return nil, false, err
	}
	if len(prepared) == 0 {
		return nil, false, fmt.Errorf("gemini history has no transferable messages")
	}
	return prepared, true, nil
}

func emptyHistoryPart(part model.ContentPart) bool {
	if part.Text != nil || part.Image != nil || part.Audio != nil || part.Video != nil || part.File != nil || part.ContentRef != nil {
		return false
	}
	switch part.Type {
	case "", model.ContentTypeText, model.ContentTypeImage, model.ContentTypeAudio, model.ContentTypeVideo, model.ContentTypeFile:
		return true
	default:
		return false
	}
}

func validateHistoryPart(role model.Role, part model.ContentPart) error {
	if part.ContentRef != nil {
		return fmt.Errorf("content reference cannot be represented")
	}
	if (part.Text != nil && part.Type != model.ContentTypeText) ||
		(part.Image != nil && part.Type != model.ContentTypeImage) ||
		(part.Audio != nil && part.Type != model.ContentTypeAudio) ||
		(part.Video != nil && part.Type != model.ContentTypeVideo) ||
		(part.File != nil && part.Type != model.ContentTypeFile) {
		return fmt.Errorf("content type %q has mismatched payload", part.Type)
	}
	if role == model.RoleTool && (part.Type != model.ContentTypeText || part.Text == nil) {
		return fmt.Errorf("tool result content type %q cannot be represented", part.Type)
	}
	switch part.Type {
	case model.ContentTypeText:
		if part.Text != nil {
			return nil
		}
	case model.ContentTypeImage:
		if part.Image != nil && (part.Image.URL != "" || len(part.Image.Data) > 0) {
			return nil
		}
	case model.ContentTypeAudio:
		if part.Audio != nil && (part.Audio.URL != "" || len(part.Audio.Data) > 0) {
			return nil
		}
	case model.ContentTypeVideo:
		if part.Video != nil && (part.Video.URL != "" || len(part.Video.Data) > 0) {
			return nil
		}
	case model.ContentTypeFile:
		if part.File != nil && (model.FileURLText(part.File) != "" || len(part.File.Data) > 0) {
			return nil
		}
	}
	return fmt.Errorf("content type %q cannot be represented", part.Type)
}

// convertProviderHistory keeps each parallel function-call round together and
// places responses in call order. Tool IDs, rather than names or arrival order,
// determine the association.
func (m *Model) convertProviderHistory(messages []model.Message) ([]*genai.Content, error) {
	var contents []*genai.Content
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		switch message.Role {
		case model.RoleAssistant, model.RoleUser, model.RoleSystem, model.RoleTool:
		default:
			return nil, fmt.Errorf("gemini history message %d: unsupported role %q", i, message.Role)
		}
		if message.Role != model.RoleAssistant && len(message.ToolCalls) != 0 {
			return nil, fmt.Errorf("gemini history message %d: function calls require the assistant role", i)
		}
		for _, part := range message.ContentParts {
			if emptyHistoryPart(part) {
				continue
			}
			if err := validateHistoryPart(message.Role, part); err != nil {
				return nil, fmt.Errorf("gemini history message %d: %w", i, err)
			}
		}
		if message.Role == model.RoleTool {
			return nil, fmt.Errorf("gemini history message %d: tool result %q has no pending function call", i, message.ToolID)
		}
		converted := m.convertMessageContent(message)
		if len(message.ToolCalls) == 0 {
			contents = append(contents, converted...)
			continue
		}
		calls := make(map[string]int, len(message.ToolCalls))
		arguments := make([]map[string]any, len(message.ToolCalls))
		for j, call := range message.ToolCalls {
			if strings.TrimSpace(call.ID) == "" {
				return nil, fmt.Errorf("gemini history message %d: function call has an empty call_id", i)
			}
			if _, exists := calls[call.ID]; exists {
				return nil, fmt.Errorf("gemini history message %d: duplicate function call %q", i, call.ID)
			}
			if strings.TrimSpace(call.Function.Name) == "" || (call.Type != "" && call.Type != "function") {
				return nil, fmt.Errorf("gemini history message %d: invalid function call %q", i, call.ID)
			}
			args := bytes.TrimSpace(call.Function.Arguments)
			// Older Gemini responses stored omitted args as JSON null.
			if len(args) == 0 || bytes.Equal(args, []byte("null")) {
				args = []byte("{}")
			}
			var err error
			arguments[j], err = decodeHistoryObject(args)
			if err != nil {
				return nil, fmt.Errorf("gemini history message %d: function call %q arguments: %w", i, call.ID, err)
			}
			calls[call.ID] = j
		}
		// The regular codec preserves Gemini thought signatures and injects the
		// documented bypass for calls from another provider.
		if len(converted) != 1 {
			return nil, fmt.Errorf("gemini history message %d: cannot represent function calls", i)
		}
		callIndex := 0
		for _, part := range converted[0].Parts {
			if part.FunctionCall != nil {
				part.FunctionCall.Args = arguments[callIndex]
				callIndex++
			}
		}
		if callIndex != len(arguments) {
			return nil, fmt.Errorf("gemini history message %d: cannot represent all function calls", i)
		}
		results := make([]*genai.Part, len(calls))
		for i+1 < len(messages) && messages[i+1].Role == model.RoleTool {
			i++
			result := messages[i]
			if len(result.ToolCalls) != 0 {
				return nil, fmt.Errorf("gemini history message %d: function calls require the assistant role", i)
			}
			index, ok := calls[result.ToolID]
			if !ok || results[index] != nil {
				return nil, fmt.Errorf("gemini history message %d: tool result %q has no pending function call", i, result.ToolID)
			}
			var value strings.Builder
			value.WriteString(result.Content)
			for _, part := range result.ContentParts {
				if emptyHistoryPart(part) {
					continue
				}
				if err := validateHistoryPart(result.Role, part); err != nil {
					return nil, fmt.Errorf("gemini history message %d: %w", i, err)
				}
				value.WriteString(*part.Text)
			}
			results[index] = &genai.Part{FunctionResponse: &genai.FunctionResponse{
				ID: result.ToolID, Name: message.ToolCalls[index].Function.Name, Response: encodeToolResult(value.String()),
			}}
		}
		for _, part := range results {
			if part == nil {
				return nil, fmt.Errorf("gemini history has function calls with missing tool results")
			}
		}
		contents = append(contents, converted[0], genai.NewContentFromParts(results, genai.RoleUser))
	}
	return contents, nil
}

// encodeToolResult preserves ordinary JSON objects and the float64 values
// exposed to Gemini callbacks. Keep the original text when the SDK would
// round a number, omit an empty object, or cannot represent it as an object.
func encodeToolResult(text string) map[string]any {
	raw := []byte(text)
	if object, err := decodeHistoryObject(raw); err == nil && len(object) > 0 {
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err == nil {
			return result
		}
	}
	return map[string]any{"output": text}
}

func decodeHistoryObject(raw []byte) (map[string]any, error) {
	if !responseshistory.ValidArguments(raw) {
		return nil, fmt.Errorf("expected JSON object")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := validateHistoryNumbers(value); err != nil {
		return nil, err
	}
	return value, nil
}

// validateHistoryNumbers checks the decimal value that go-genai will emit
// after its internal JSON-to-float64 conversion. A JSON number is allowed when
// its value survives that conversion exactly, including large powers of ten.
func validateHistoryNumbers(value any) error {
	switch v := value.(type) {
	case json.Number:
		number, err := v.Float64()
		if err != nil {
			return fmt.Errorf("number %q cannot be preserved by Gemini SDK", v)
		}
		if number == 0 {
			mantissa := strings.SplitN(strings.ToLower(string(v)), "e", 2)[0]
			if strings.ContainsAny(mantissa, "123456789") {
				return fmt.Errorf("number %q cannot be preserved by Gemini SDK", v)
			}
			return nil
		}
		encoded, err := json.Marshal(number)
		if err != nil {
			return fmt.Errorf("number %q cannot be preserved by Gemini SDK", v)
		}
		original, ok := new(big.Rat).SetString(string(v))
		if !ok {
			return fmt.Errorf("number %q cannot be preserved by Gemini SDK", v)
		}
		encodedValue, ok := new(big.Rat).SetString(string(encoded))
		if !ok || original.Cmp(encodedValue) != 0 {
			return fmt.Errorf("number %q cannot be preserved by Gemini SDK", v)
		}
	case map[string]any:
		for _, item := range v {
			if err := validateHistoryNumbers(item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := validateHistoryNumbers(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate the final callback-visible arguments and results too: a request
// callback may have edited them after the initial history projection.
func validateProviderHistoryNumbers(contents []*genai.Content) error {
	for i, content := range contents {
		if content == nil {
			continue
		}
		for j, part := range content.Parts {
			if part == nil {
				continue
			}
			var value any
			switch {
			case part.FunctionCall != nil:
				value = part.FunctionCall.Args
			case part.FunctionResponse != nil:
				value = part.FunctionResponse.Response
			default:
				continue
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("gemini history content %d part %d: %w", i, j, err)
			}
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			var decoded any
			if err := decoder.Decode(&decoded); err != nil {
				return fmt.Errorf("gemini history content %d part %d: %w", i, j, err)
			}
			if err := validateHistoryNumbers(decoded); err != nil {
				return fmt.Errorf("gemini history content %d part %d: %w", i, j, err)
			}
		}
	}
	return nil
}
