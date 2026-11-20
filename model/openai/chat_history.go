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
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

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
		if data.Protocol != responseshistory.Protocol {
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
	callIDs, nativeText, sources, err := responsesHistoryForChat(message.ProviderData.Payload)
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
	// A callback can redact or otherwise replace the visible projection without
	// editing ProviderData. Never restore source URLs from that stale record.
	if visible, ok := chatVisibleText(converted); ok && visible == nativeText && len(sources) > 0 {
		sources = slices.DeleteFunc(sources, func(source string) bool { return chatTextContainsURL(visible, source) })
		if len(sources) > 0 {
			links := "\n\nSources:\n" + strings.Join(sources, "\n")
			if len(converted.ContentParts) == 0 {
				converted.Content += links
			} else {
				converted.ContentParts = append(converted.ContentParts, model.ContentPart{Type: model.ContentTypeText, Text: &links})
			}
		}
	}
	for i := range converted.ToolCalls {
		call := &converted.ToolCalls[i]
		if err := validateProjectedChatCall(call); err != nil {
			return model.Message{}, false, err
		}
		call.ExtraFields = nil
	}
	return converted, converted.Content != "" || len(converted.ContentParts) > 0 || len(converted.ToolCalls) > 0, nil
}

// responsesHistoryForChat validates the native items and collects their text
// and supported citations without changing the persisted record.
func responsesHistoryForChat(payload json.RawMessage) ([]string, string, []string, error) {
	items, err := responseshistory.ReadCompleted(payload)
	if err != nil {
		return nil, "", nil, err
	}
	var callIDs []string
	var text strings.Builder
	var sources []string
	seen := make(map[string]struct{})
	for i, item := range items {
		switch item.Type {
		case responseshistory.FunctionCall:
			callIDs = append(callIDs, item.CallID)
		case responseshistory.Message:
			if len(callIDs) != 0 {
				return nil, "", nil, fmt.Errorf("responses message item %d follows a function call in the same output", i)
			}
			for _, part := range item.Content {
				switch part.Type {
				case responseshistory.OutputText:
					text.WriteString(part.Text)
				case responseshistory.Refusal:
					text.WriteString(part.Refusal)
				}
				links, err := chatCitationURLs(part)
				if err != nil {
					return nil, "", nil, fmt.Errorf("responses message item %d has annotations that chat history cannot represent: %w", i, err)
				}
				for _, link := range links {
					if _, ok := seen[link]; !ok {
						seen[link] = struct{}{}
						sources = append(sources, link)
					}
				}
			}
		}
	}
	return callIDs, text.String(), sources, nil
}

func chatVisibleText(message model.Message) (string, bool) {
	var text strings.Builder
	text.WriteString(message.Content)
	for _, part := range message.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil || part.ContentRef != nil {
			return "", false
		}
		text.WriteString(*part.Text)
	}
	return text.String(), true
}

// chatCitationURLs accepts only URL citations with safe, absolute HTTP URLs.
// The title and offsets are not used when projecting plain source links.
func chatCitationURLs(part responseshistory.Content) ([]string, error) {
	if part.Annotations == nil {
		return nil, nil
	}
	var annotations []json.RawMessage
	if err := json.Unmarshal(part.Annotations, &annotations); err != nil {
		return nil, fmt.Errorf("invalid annotations")
	}
	var links []string
	for _, raw := range annotations {
		var citation struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		if err := json.Unmarshal(raw, &citation); err != nil || part.Type != responseshistory.OutputText ||
			citation.Type != "url_citation" || !safeChatCitationURL(citation.URL) {
			return nil, fmt.Errorf("unsupported or invalid annotation")
		}
		links = append(links, citation.URL)
	}
	return links, nil
}

// chatTextContainsURL avoids treating one URL as present merely because it is
// a prefix or a substring of a longer URL in the visible text.
func chatTextContainsURL(text, source string) bool {
	for start := 0; start < len(text); {
		i := strings.Index(text[start:], source)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(source)
		before, after := rune(0), rune(0)
		if i > 0 {
			before, _ = utf8.DecodeLastRuneInString(text[:i])
		}
		if end < len(text) {
			after, _ = utf8.DecodeRuneInString(text[end:])
		}
		wrapped := before == '(' && after == ')'
		if wrapped && i > 1 {
			outer, _ := utf8.DecodeLastRuneInString(text[:i-1])
			wrapped = outer == ']' || !chatURLCharacter(outer)
		}
		if (!chatURLCharacter(before) && !chatURLCharacter(after)) || wrapped {
			return true
		}
		start = i + len(source)
	}
	return false
}

func chatURLCharacter(r rune) bool {
	return r != 0 && !unicode.IsSpace(r) && !unicode.IsControl(r) && !strings.ContainsRune(`<>"`, r)
}

func safeChatCitationURL(raw string) bool {
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Opaque == ""
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
	if !responseshistory.ValidArguments(args) {
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
