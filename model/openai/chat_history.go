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
	"fmt"
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata/responseshistory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// SupportsProviderHistory reports whether this adapter can project the given
// native protocol into Chat Completions history.
func (m *Model) SupportsProviderHistory(protocol string) bool {
	return protocol == responseshistory.Protocol
}

// prepareProviderHistory projects completed Responses history for a Chat
// request. changed reports whether a separate slice was prepared.
func (m *Model) prepareProviderHistory(messages []model.Message) ([]model.Message, bool, error) {
	if !providerdata.MessagesHaveData(messages) {
		return messages, false, nil
	}
	projected := make([]model.Message, 0, len(messages))
	var searches responseshistory.Projector
	for i, message := range messages {
		if message.Role != model.RoleTool {
			if err := searches.Finish(); err != nil {
				return nil, false, fmt.Errorf("chat history message %d: %w", i, err)
			}
		}
		converted, include, err := projectChatHistoryMessage(message, &searches)
		if err != nil {
			return nil, false, fmt.Errorf("chat history message %d: %w", i, err)
		}
		if include {
			projected = append(projected, converted)
		}
	}
	if err := searches.Finish(); err != nil {
		return nil, false, err
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

func projectChatHistoryMessage(message model.Message, searches *responseshistory.Projector) (model.Message, bool, error) {
	data := message.ProviderData
	if slices.ContainsFunc(message.ContentParts, isEmptyChatContentPart) {
		message.ContentParts = slices.DeleteFunc(slices.Clone(message.ContentParts), isEmptyChatContentPart)
		if data == nil && message.Role != model.RoleTool && message.Content == "" &&
			len(message.ContentParts) == 0 && len(message.ToolCalls) == 0 {
			return model.Message{}, false, nil
		}
	}
	if data != nil {
		return searches.ProjectMessage(message)
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
