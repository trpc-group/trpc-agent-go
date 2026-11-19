//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const responsesProtocol = "openai.responses"

type responsesHistoryItem struct {
	Type      string                    `json:"type"`
	ID        string                    `json:"id"`
	Status    string                    `json:"status"`
	Role      string                    `json:"role"`
	CallID    string                    `json:"call_id"`
	Name      string                    `json:"name"`
	Arguments string                    `json:"arguments"`
	Content   []responsesHistoryContent `json:"content"`
}

type responsesHistoryContent struct {
	Type        string            `json:"type"`
	Annotations []json.RawMessage `json:"annotations"`
}

// SupportsProviderHistory reports whether this adapter can project the given
// native protocol into Chat Completions history.
func (m *Model) SupportsProviderHistory(protocol string) bool {
	return protocol == responsesProtocol
}

// prepareProviderHistory projects completed Responses history for a Chat
// request. changed reports whether a separate slice was prepared.
func (m *Model) prepareProviderHistory(messages []model.Message) ([]model.Message, bool, error) {
	if !providerdata.MessagesHaveData(messages) {
		return messages, false, nil
	}
	projected := make([]model.Message, 0, len(messages))
	for i, message := range messages {
		converted, include, err := projectChatHistoryMessage(message)
		if err != nil {
			return nil, false, fmt.Errorf("chat history message %d: %w", i, err)
		}
		if include {
			projected = append(projected, converted)
		}
	}
	if len(projected) == 0 {
		return nil, false, fmt.Errorf("chat history has no transferable messages")
	}
	if err := validateChatToolRounds(projected); err != nil {
		return nil, false, err
	}
	if err := m.validateChatHistoryContent(projected); err != nil {
		return nil, false, err
	}
	return projected, true, nil
}

func projectChatHistoryMessage(message model.Message) (model.Message, bool, error) {
	data := message.ProviderData
	if slices.ContainsFunc(message.ContentParts, isEmptyChatContentPart) {
		message.ContentParts = slices.DeleteFunc(slices.Clone(message.ContentParts), isEmptyChatContentPart)
		if data == nil && message.Role != model.RoleTool && message.Content == "" &&
			len(message.ContentParts) == 0 && len(message.ToolCalls) == 0 {
			return model.Message{}, false, nil
		}
	}
	if data != nil {
		if data.Protocol != responsesProtocol {
			return model.Message{}, false, fmt.Errorf("unsupported provider data protocol %q", data.Protocol)
		}
		if err := providerdata.ValidateMessage(message, model.Info{Name: data.Model, ProviderDataProtocol: data.Protocol}); err != nil {
			return model.Message{}, false, err
		}
		return projectResponsesHistory(message)
	}
	if message.Role == model.RoleTool && len(message.ContentParts) > 0 {
		var content strings.Builder
		content.WriteString(message.Content)
		for _, part := range message.ContentParts {
			if part.ContentRef != nil {
				return model.Message{}, false, fmt.Errorf("tool result content reference cannot be represented")
			}
			if part.Type != model.ContentTypeText || part.Text == nil {
				return model.Message{}, false, fmt.Errorf("tool result content type %q cannot be represented", part.Type)
			}
			content.WriteString(*part.Text)
		}
		message.Content = content.String()
		message.ContentParts = nil
	}
	return message, true, nil
}

// isEmptyChatContentPart recognizes placeholders without a payload or a
// reference awaiting hydration.
func isEmptyChatContentPart(part model.ContentPart) bool {
	if part.Text != nil || part.Image != nil || part.Audio != nil ||
		part.Video != nil || part.File != nil || part.ContentRef != nil {
		return false
	}
	switch part.Type {
	case "", model.ContentTypeText, model.ContentTypeImage,
		model.ContentTypeAudio, model.ContentTypeVideo, model.ContentTypeFile:
		return true
	default:
		return false
	}
}

func (m *Model) validateChatHistoryContent(messages []model.Message) error {
	for i, message := range messages {
		for _, part := range message.ContentParts {
			if part.ContentRef != nil {
				return fmt.Errorf("chat history message %d: content reference cannot be represented", i)
			}
			switch message.Role {
			case model.RoleAssistant, model.RoleSystem:
				if part.Type != model.ContentTypeText || part.Text == nil {
					return fmt.Errorf("chat history message %d: content type %q cannot be represented for role %q", i, part.Type, message.Role)
				}
			case model.RoleUser:
				if !m.supportsUserHistoryPart(part) {
					return fmt.Errorf("chat history message %d: content type %q cannot be represented by this model", i, part.Type)
				}
			default:
				return fmt.Errorf("chat history message %d: content type %q cannot be represented for role %q", i, part.Type, message.Role)
			}
		}
	}
	return nil
}

// supportsUserHistoryPart mirrors appendUserContentParts without encoding data.
func (m *Model) supportsUserHistoryPart(part model.ContentPart) bool {
	if part.Type == model.ContentTypeFile {
		if part.File == nil {
			return false
		}
		if m.omitFileContentParts || isInternalOnlyFile(part.File) {
			return true
		}
		if m.variantConfig.textOnlyMessageContent {
			return fileURLFallbackText(part.File) != ""
		}
		if m.variantConfig.skipFileTypeInContent {
			return fileURLFallbackText(part.File) != "" || isProviderFileID(part.File.FileID)
		}
		return fileURLFallbackText(part.File) != "" || isProviderFileID(part.File.FileID) || len(part.File.Data) > 0
	}
	if m.variantConfig.textOnlyMessageContent && part.Type != model.ContentTypeText {
		return false
	}
	switch part.Type {
	case model.ContentTypeText:
		return part.Text != nil
	case model.ContentTypeImage:
		return part.Image != nil
	case model.ContentTypeAudio:
		return part.Audio != nil && part.Audio.URL == "" && len(part.Audio.Data) > 0
	default:
		return false
	}
}

// projectResponsesHistory validates the native record while preserving the
// caller-visible projection, which callbacks may have edited for this request.
func projectResponsesHistory(message model.Message) (model.Message, bool, error) {
	callIDs, err := responsesHistoryCallIDs(message.ProviderData.Payload)
	if err != nil {
		return model.Message{}, false, err
	}
	if len(callIDs) != len(message.ToolCalls) {
		return model.Message{}, false, fmt.Errorf("responses function calls do not match the message projection")
	}
	for i, id := range callIDs {
		if id != message.ToolCalls[i].ID {
			return model.Message{}, false, fmt.Errorf("responses function call %q does not match the message projection", id)
		}
	}
	converted := providerdata.CloneMessageProjections(message)
	converted.ProviderData = nil
	converted.ReasoningContent = ""
	converted.ReasoningSignature = ""
	for i := range converted.ToolCalls {
		call := &converted.ToolCalls[i]
		if err := validateProjectedChatCall(call); err != nil {
			return model.Message{}, false, err
		}
		call.ExtraFields = nil
	}
	return converted, converted.Content != "" || len(converted.ContentParts) > 0 || len(converted.ToolCalls) > 0, nil
}

func responsesHistoryCallIDs(payload json.RawMessage) ([]string, error) {
	var items []responsesHistoryItem
	if err := json.Unmarshal(payload, &items); err != nil || items == nil {
		return nil, fmt.Errorf("responses provider data must be an item array")
	}
	var callIDs []string
	for i, item := range items {
		if err := validateResponsesHistoryItem(item, i, len(callIDs) > 0); err != nil {
			return nil, err
		}
		if item.Type == "function_call" {
			callIDs = append(callIDs, item.CallID)
		}
	}
	return callIDs, nil
}

func validateResponsesHistoryItem(item responsesHistoryItem, index int, hasCalls bool) error {
	if item.Status != "" && item.Status != "completed" {
		return fmt.Errorf("responses item %d has status %q", index, item.Status)
	}
	switch item.Type {
	case "reasoning":
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("responses reasoning item %d has an empty id", index)
		}
	case "message":
		return validateResponsesHistoryMessage(item, index, hasCalls)
	case "function_call":
		if strings.TrimSpace(item.CallID) == "" || strings.TrimSpace(item.Name) == "" {
			return fmt.Errorf("responses function call item %d has an empty call_id or name", index)
		}
		if !isChatHistoryJSONObject([]byte(item.Arguments)) {
			return fmt.Errorf("responses function call item %d has invalid JSON arguments", index)
		}
	default:
		return fmt.Errorf("responses item %d has unsupported type %q", index, item.Type)
	}
	return nil
}

func validateResponsesHistoryMessage(item responsesHistoryItem, index int, hasCalls bool) error {
	if strings.TrimSpace(item.ID) == "" || item.Status != "completed" || item.Role != "assistant" {
		return fmt.Errorf("responses message item %d is not a completed assistant message", index)
	}
	if hasCalls {
		return fmt.Errorf("responses message item %d follows a function call in the same output", index)
	}
	for _, part := range item.Content {
		if len(part.Annotations) != 0 {
			return fmt.Errorf("responses message item %d has annotations that chat history cannot represent", index)
		}
		if part.Type != "output_text" && part.Type != "refusal" {
			return fmt.Errorf("responses message item %d has unsupported content type %q", index, part.Type)
		}
	}
	return nil
}

func isChatHistoryJSONObject(raw []byte) bool {
	args := bytes.TrimSpace(raw)
	return len(args) > 0 && args[0] == '{' && json.Valid(args)
}

// validateProjectedChatCall checks the call that will actually be sent. Its
// values may have been edited independently of the native payload.
func validateProjectedChatCall(call *model.ToolCall) error {
	if strings.TrimSpace(call.Function.Name) == "" {
		return fmt.Errorf("function call %q has an empty name", call.ID)
	}
	args := bytes.TrimSpace(call.Function.Arguments)
	if len(args) == 0 {
		call.Function.Arguments = []byte("{}")
		return nil
	}
	if !isChatHistoryJSONObject(args) {
		return fmt.Errorf("function call %q has invalid JSON object arguments", call.ID)
	}
	return nil
}

func validateChatToolRounds(messages []model.Message) error {
	pending := make(map[string]struct{})
	for i, message := range messages {
		if len(pending) != 0 && message.Role != model.RoleTool {
			return fmt.Errorf("chat history message %d: function calls have missing tool results", i)
		}
		switch message.Role {
		case model.RoleAssistant:
			for _, call := range message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" {
					return fmt.Errorf("chat history message %d: function call has an empty call_id", i)
				}
				if _, exists := pending[call.ID]; exists {
					return fmt.Errorf("chat history message %d: duplicate function call %q", i, call.ID)
				}
				pending[call.ID] = struct{}{}
			}
		case model.RoleTool:
			if _, exists := pending[message.ToolID]; !exists {
				return fmt.Errorf("chat history message %d: tool result %q has no pending function call", i, message.ToolID)
			}
			delete(pending, message.ToolID)
		}
	}
	if len(pending) != 0 {
		return fmt.Errorf("chat history has function calls with missing tool results")
	}
	return nil
}
