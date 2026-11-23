//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func promptToMessage(blocks []acpsdk.ContentBlock) (model.Message, error) {
	message := model.Message{Role: model.RoleUser}
	var text strings.Builder
	var contentParts []model.ContentPart
	hasMultimodalContent := false
	for _, block := range blocks {
		switch {
		case block.Text != nil:
			text.WriteString(block.Text.Text)
			textContent := block.Text.Text
			contentParts = append(contentParts, model.ContentPart{
				Type: model.ContentTypeText,
				Text: &textContent,
			})
		case block.ResourceLink != nil:
			hasMultimodalContent = true
			mimeType := ""
			if block.ResourceLink.MimeType != nil {
				mimeType = *block.ResourceLink.MimeType
			}
			contentParts = append(contentParts, model.ContentPart{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     block.ResourceLink.Name,
					URL:      block.ResourceLink.Uri,
					MimeType: mimeType,
				},
			})
		case block.Image != nil:
			return model.Message{}, errors.New("image content is not supported")
		case block.Audio != nil:
			return model.Message{}, errors.New("audio content is not supported")
		case block.Resource != nil:
			return model.Message{}, errors.New("embedded resource content is not supported")
		default:
			return model.Message{}, errors.New("empty content block")
		}
	}
	if hasMultimodalContent {
		message.ContentParts = contentParts
	} else {
		message.Content = text.String()
	}
	return message, nil
}

type turnState struct {
	textByResponse      map[string]string
	reasoningByResponse map[string]string
	activeResponses     map[string]string
	roleByResponse      map[string]model.Role
	toolCalls           map[string]toolCallState
	toolCallIDs         *toolCallIDMapper
	stopReason          acpsdk.StopReason
	usage               *acpsdk.Usage
	reasoningEnabled    bool
	nextResponse        uint64
}

type toolCallState struct {
	title     string
	arguments string
}

func newTurnState(
	reasoningEnabled bool,
	toolCallIDs *toolCallIDMapper,
) *turnState {
	return &turnState{
		textByResponse:      make(map[string]string),
		reasoningByResponse: make(map[string]string),
		activeResponses:     make(map[string]string),
		roleByResponse:      make(map[string]model.Role),
		toolCalls:           make(map[string]toolCallState),
		toolCallIDs:         toolCallIDs,
		stopReason:          acpsdk.StopReasonEndTurn,
		reasoningEnabled:    reasoningEnabled,
	}
}

func (s *turnState) translate(
	ctx context.Context,
	evt *event.Event,
) ([]acpsdk.SessionUpdate, error) {
	if evt == nil || evt.Response == nil {
		return nil, nil
	}
	if evt.IsTerminalError() {
		return nil, evt.Response.Error
	}
	if evt.Response.Usage != nil && !evt.Response.IsPartial {
		if s.usage == nil {
			s.usage = &acpsdk.Usage{}
		}
		s.usage.InputTokens += evt.Response.Usage.PromptTokens
		s.usage.OutputTokens += evt.Response.Usage.CompletionTokens
		s.usage.TotalTokens += evt.Response.Usage.TotalTokens
	}

	var updates []acpsdk.SessionUpdate
	terminal := !evt.Response.IsPartial
	for _, choice := range evt.Response.Choices {
		streamKey := responseStreamKey(evt, choice.Index)
		key := s.responseKey(evt, streamKey)
		assistantContent := s.isAssistantContent(key, choice)
		if s.reasoningEnabled {
			if reasoning := s.contentDelta(
				s.reasoningByResponse,
				key,
				choice.Delta.ReasoningContent,
				choice.Message.ReasoningContent,
			); assistantContent && reasoning != "" {
				updates = append(updates, acpsdk.UpdateAgentThoughtText(reasoning))
			}
		}
		if text := s.contentDelta(
			s.textByResponse,
			key,
			choice.Delta.Content,
			choice.Message.Content,
		); assistantContent &&
			text != "" &&
			choice.Delta.ToolID == "" &&
			choice.Message.ToolID == "" {
			updates = append(updates, acpsdk.UpdateAgentMessageText(text))
		}
		updates = append(updates, s.translateToolCalls(ctx, choice.Delta.ToolCalls)...)
		updates = append(updates, s.translateToolCalls(ctx, choice.Message.ToolCalls)...)
		if update, ok := s.translateToolResult(ctx, choice.Delta, terminal); ok {
			updates = append(updates, update)
		}
		if update, ok := s.translateToolResult(ctx, choice.Message, terminal); ok {
			updates = append(updates, update)
		}
		s.applyFinishReason(choice.FinishReason)
		if !evt.Response.IsPartial {
			delete(s.textByResponse, key)
			delete(s.reasoningByResponse, key)
			delete(s.roleByResponse, key)
			if s.activeResponses[streamKey] == key {
				delete(s.activeResponses, streamKey)
			}
		}
	}
	return updates, nil
}

func (s *turnState) isAssistantContent(key string, choice model.Choice) bool {
	role := choice.Delta.Role
	if role == "" {
		role = choice.Message.Role
	}
	if role != "" {
		s.roleByResponse[key] = role
	} else {
		role = s.roleByResponse[key]
	}
	return role == "" || role == model.RoleAssistant
}

func responseStreamKey(evt *event.Event, choiceIndex int) string {
	return fmt.Sprintf("%s/%d", evt.InvocationID, choiceIndex)
}

func (s *turnState) responseKey(evt *event.Event, streamKey string) string {
	if evt.Response.ID != "" {
		key := fmt.Sprintf("%s/%s", evt.Response.ID, streamKey)
		s.activeResponses[streamKey] = key
		return key
	}
	if key := s.activeResponses[streamKey]; key != "" {
		return key
	}
	s.nextResponse++
	key := fmt.Sprintf("response-%d/%s", s.nextResponse, streamKey)
	s.activeResponses[streamKey] = key
	return key
}

func (*turnState) contentDelta(
	emitted map[string]string,
	key string,
	delta string,
	message string,
) string {
	if delta != "" {
		emitted[key] += delta
		return delta
	}
	if message == "" {
		return ""
	}
	previous := emitted[key]
	emitted[key] = message
	if strings.HasPrefix(message, previous) {
		return strings.TrimPrefix(message, previous)
	}
	return message
}

func (s *turnState) translateToolCalls(
	ctx context.Context,
	toolCalls []model.ToolCall,
) []acpsdk.SessionUpdate {
	var updates []acpsdk.SessionUpdate
	for _, toolCall := range toolCalls {
		if toolCall.ID == "" {
			continue
		}
		title := toolCall.Function.Name
		if title == "" {
			title = "Tool call"
		}
		arguments := string(toolCall.Function.Arguments)
		current := toolCallState{title: title, arguments: arguments}
		acpToolCallID := s.toolCallIDs.resolve(
			ctx,
			toolCall.ID,
			toolCall.Function.Name,
		)
		if acpToolCallID == "" {
			continue
		}
		previous, exists := s.toolCalls[string(acpToolCallID)]
		if !exists {
			s.toolCalls[string(acpToolCallID)] = current
			updates = append(updates, acpsdk.StartToolCall(
				acpToolCallID,
				title,
				acpsdk.WithStartKind(acpsdk.ToolKindOther),
				acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
				acpsdk.WithStartRawInput(decodeJSONArgument(arguments)),
			))
			continue
		}
		if previous == current {
			continue
		}
		s.toolCalls[string(acpToolCallID)] = current
		updates = append(updates, acpsdk.UpdateToolCall(
			acpToolCallID,
			acpsdk.WithUpdateTitle(title),
			acpsdk.WithUpdateRawInput(decodeJSONArgument(arguments)),
		))
	}
	return updates
}

func decodeJSONArgument(argument string) any {
	if argument == "" {
		return nil
	}
	var decoded any
	if json.Unmarshal([]byte(argument), &decoded) == nil {
		return decoded
	}
	return argument
}

func (s *turnState) translateToolResult(
	ctx context.Context,
	message model.Message,
	terminal bool,
) (acpsdk.SessionUpdate, bool) {
	if message.ToolID == "" {
		return acpsdk.SessionUpdate{}, false
	}
	acpToolCallID, ok := s.toolCallIDs.current(message.ToolID)
	if !ok {
		log.WarnContext(
			ctx,
			"ACP tool result update has no active tool call mapping",
			"provider_tool_call_id",
			message.ToolID,
			"terminal",
			terminal,
		)
		return acpsdk.SessionUpdate{}, false
	}
	status := acpsdk.ToolCallStatusInProgress
	if terminal {
		status = acpsdk.ToolCallStatusCompleted
	}
	options := []acpsdk.ToolCallUpdateOpt{
		acpsdk.WithUpdateStatus(status),
		acpsdk.WithUpdateRawOutput(message.Content),
	}
	if message.Content != "" {
		options = append(options, acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{
			acpsdk.ToolContent(acpsdk.TextBlock(message.Content)),
		}))
	}
	update := acpsdk.UpdateToolCall(acpToolCallID, options...)
	if terminal {
		s.toolCallIDs.complete(ctx, message.ToolID)
		delete(s.toolCalls, string(acpToolCallID))
	}
	return update, true
}

func (s *turnState) applyFinishReason(finishReason *string) {
	if finishReason == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(*finishReason)) {
	case "length", "max_tokens":
		s.stopReason = acpsdk.StopReasonMaxTokens
	case "blocked",
		"blocklist",
		"content_filter",
		"content_filtered",
		"guardrail_intervened",
		"prohibited_content",
		"recitation",
		"refusal",
		"safety",
		"spii":
		s.stopReason = acpsdk.StopReasonRefusal
	case "cancelled", "canceled":
		s.stopReason = acpsdk.StopReasonCancelled
	}
}

func (s *turnState) response(messageID *string) acpsdk.PromptResponse {
	return acpsdk.PromptResponse{
		StopReason:    s.stopReason,
		Usage:         s.usage,
		UserMessageId: messageID,
	}
}
