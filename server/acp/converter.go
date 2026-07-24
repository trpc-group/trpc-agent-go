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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func promptToMessage(blocks []acpsdk.ContentBlock) (model.Message, error) {
	message := model.Message{Role: model.RoleUser}
	var text strings.Builder
	for _, block := range blocks {
		switch {
		case block.Text != nil:
			text.WriteString(block.Text.Text)
		case block.ResourceLink != nil:
			mimeType := ""
			if block.ResourceLink.MimeType != nil {
				mimeType = *block.ResourceLink.MimeType
			}
			message.AddFileURL(
				block.ResourceLink.Name,
				block.ResourceLink.Uri,
				mimeType,
			)
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
	message.Content = text.String()
	return message, nil
}

type turnState struct {
	textByResponse      map[string]string
	reasoningByResponse map[string]string
	toolCalls           map[string]toolCallState
	stopReason          acpsdk.StopReason
	usage               *acpsdk.Usage
	reasoningEnabled    bool
}

type toolCallState struct {
	title     string
	arguments string
}

func newTurnState(reasoningEnabled bool) *turnState {
	return &turnState{
		textByResponse:      make(map[string]string),
		reasoningByResponse: make(map[string]string),
		toolCalls:           make(map[string]toolCallState),
		stopReason:          acpsdk.StopReasonEndTurn,
		reasoningEnabled:    reasoningEnabled,
	}
}

func (s *turnState) translate(evt *event.Event) ([]acpsdk.SessionUpdate, error) {
	if evt == nil || evt.Response == nil {
		return nil, nil
	}
	if evt.IsTerminalError() {
		return nil, evt.Response.Error
	}
	if evt.Response.Usage != nil {
		if s.usage == nil {
			s.usage = &acpsdk.Usage{}
		}
		s.usage.InputTokens += evt.Response.Usage.PromptTokens
		s.usage.OutputTokens += evt.Response.Usage.CompletionTokens
		s.usage.TotalTokens += evt.Response.Usage.TotalTokens
	}

	var updates []acpsdk.SessionUpdate
	for _, choice := range evt.Response.Choices {
		key := responseKey(evt, choice.Index)
		if s.reasoningEnabled {
			if reasoning := s.contentDelta(
				s.reasoningByResponse,
				key,
				choice.Delta.ReasoningContent,
				choice.Message.ReasoningContent,
			); reasoning != "" {
				updates = append(updates, acpsdk.UpdateAgentThoughtText(reasoning))
			}
		}
		if text := s.contentDelta(
			s.textByResponse,
			key,
			choice.Delta.Content,
			choice.Message.Content,
		); text != "" && choice.Delta.ToolID == "" && choice.Message.ToolID == "" {
			updates = append(updates, acpsdk.UpdateAgentMessageText(text))
		}
		updates = append(updates, s.translateToolCalls(choice.Delta.ToolCalls)...)
		updates = append(updates, s.translateToolCalls(choice.Message.ToolCalls)...)
		if update, ok := translateToolResult(choice.Delta); ok {
			updates = append(updates, update)
		}
		if update, ok := translateToolResult(choice.Message); ok {
			updates = append(updates, update)
		}
		s.applyFinishReason(choice.FinishReason)
		if evt.Response.ID == "" && !evt.Response.IsPartial {
			delete(s.textByResponse, key)
			delete(s.reasoningByResponse, key)
		}
	}
	return updates, nil
}

func responseKey(evt *event.Event, choiceIndex int) string {
	if evt.Response.ID != "" {
		return fmt.Sprintf("%s/%d", evt.Response.ID, choiceIndex)
	}
	return fmt.Sprintf("%s/%d", evt.InvocationID, choiceIndex)
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
		previous, exists := s.toolCalls[toolCall.ID]
		if !exists {
			s.toolCalls[toolCall.ID] = current
			updates = append(updates, acpsdk.StartToolCall(
				acpsdk.ToolCallId(toolCall.ID),
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
		s.toolCalls[toolCall.ID] = current
		updates = append(updates, acpsdk.UpdateToolCall(
			acpsdk.ToolCallId(toolCall.ID),
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

func translateToolResult(message model.Message) (acpsdk.SessionUpdate, bool) {
	if message.ToolID == "" {
		return acpsdk.SessionUpdate{}, false
	}
	options := []acpsdk.ToolCallUpdateOpt{
		acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
		acpsdk.WithUpdateRawOutput(message.Content),
	}
	if message.Content != "" {
		options = append(options, acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{
			acpsdk.ToolContent(acpsdk.TextBlock(message.Content)),
		}))
	}
	return acpsdk.UpdateToolCall(acpsdk.ToolCallId(message.ToolID), options...), true
}

func (s *turnState) applyFinishReason(finishReason *string) {
	if finishReason == nil {
		return
	}
	switch *finishReason {
	case "length":
		s.stopReason = acpsdk.StopReasonMaxTokens
	case "content_filter", "refusal":
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
