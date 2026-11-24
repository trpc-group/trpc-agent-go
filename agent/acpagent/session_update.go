//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"encoding/json"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	acpToolUpdateExtension = "trpc_agent.acp.tool_call"
	acpPlanExtension       = "trpc_agent.acp.plan"
)

type observedToolCall struct {
	name      string
	arguments []byte
}

type sessionUpdateTranslator struct {
	invocationID string
	author       string
	text         strings.Builder
	reasoning    strings.Builder
	contentParts []model.ContentPart
	tools        map[acpsdk.ToolCallId]*observedToolCall
}

func newSessionUpdateTranslator(
	invocationID string,
	author string,
) *sessionUpdateTranslator {
	return &sessionUpdateTranslator{
		invocationID: invocationID,
		author:       author,
		tools:        make(map[acpsdk.ToolCallId]*observedToolCall),
	}
}

func (t *sessionUpdateTranslator) translate(
	notification acpsdk.SessionNotification,
) ([]*event.Event, error) {
	update := notification.Update
	switch {
	case update.AgentMessageChunk != nil:
		text, parts := assistantContent(update.AgentMessageChunk.Content)
		if text == "" && len(parts) == 0 {
			return nil, nil
		}
		t.text.WriteString(text)
		t.contentParts = append(t.contentParts, parts...)
		return []*event.Event{
			t.partialMessageEvent(text, "", parts),
		}, nil
	case update.AgentThoughtChunk != nil:
		text := contentBlockText(update.AgentThoughtChunk.Content)
		if text == "" {
			return nil, nil
		}
		t.reasoning.WriteString(text)
		return []*event.Event{
			t.partialMessageEvent("", text, nil),
		}, nil
	case update.ToolCall != nil:
		return t.toolCallEvent(update.ToolCall)
	case update.ToolCallUpdate != nil:
		return t.toolCallUpdateEvent(update.ToolCallUpdate)
	case update.Plan != nil:
		return t.planEvent(update.Plan)
	default:
		return nil, nil
	}
}

func (t *sessionUpdateTranslator) partialMessageEvent(
	content string,
	reasoning string,
	contentParts []model.ContentPart,
) *event.Event {
	response := &model.Response{
		Object:    model.ObjectTypeChatCompletionChunk,
		Done:      false,
		IsPartial: true,
		Choices: []model.Choice{
			{
				Index: 0,
				Delta: model.Message{
					Role:             model.RoleAssistant,
					Content:          content,
					ReasoningContent: reasoning,
					ContentParts:     append([]model.ContentPart(nil), contentParts...),
				},
			},
		},
	}
	return event.NewResponseEvent(t.invocationID, t.author, response)
}

func (t *sessionUpdateTranslator) toolCallEvent(
	update *acpsdk.SessionUpdateToolCall,
) ([]*event.Event, error) {
	if update == nil {
		return nil, nil
	}
	name := preferredToolName(update.Meta, update.Title, string(update.ToolCallId))
	arguments, err := marshalOptionalJSON(update.RawInput)
	if err != nil {
		return nil, fmt.Errorf("acpagent: marshal tool call input: %w", err)
	}
	t.tools[update.ToolCallId] = &observedToolCall{
		name:      name,
		arguments: arguments,
	}
	evt := t.toolCallFrameworkEvent(update.ToolCallId, name, arguments)
	raw, err := json.Marshal(update)
	if err != nil {
		return nil, fmt.Errorf("acpagent: marshal tool call update: %w", err)
	}
	evt.Extensions = map[string]json.RawMessage{
		acpToolUpdateExtension: raw,
	}
	return []*event.Event{evt}, nil
}

func (t *sessionUpdateTranslator) toolCallUpdateEvent(
	update *acpsdk.SessionToolCallUpdate,
) ([]*event.Event, error) {
	if update == nil {
		return nil, nil
	}
	tool := t.tools[update.ToolCallId]
	if tool == nil {
		tool = &observedToolCall{
			name:      string(update.ToolCallId),
			arguments: []byte("null"),
		}
		t.tools[update.ToolCallId] = tool
	}
	title := ""
	if update.Title != nil {
		title = strings.TrimSpace(*update.Title)
	}
	tool.name = preferredToolName(update.Meta, title, tool.name)
	if update.RawInput != nil {
		arguments, err := marshalOptionalJSON(update.RawInput)
		if err != nil {
			return nil, fmt.Errorf("acpagent: marshal tool call update input: %w", err)
		}
		tool.arguments = arguments
	}
	evt := t.toolCallFrameworkEvent(update.ToolCallId, tool.name, tool.arguments)
	raw, err := json.Marshal(update)
	if err != nil {
		return nil, fmt.Errorf("acpagent: marshal tool call event: %w", err)
	}
	evt.Extensions = map[string]json.RawMessage{
		acpToolUpdateExtension: raw,
	}
	return []*event.Event{evt}, nil
}

func (t *sessionUpdateTranslator) toolCallFrameworkEvent(
	id acpsdk.ToolCallId,
	name string,
	arguments []byte,
) *event.Event {
	response := &model.Response{
		Object:    model.ObjectTypeChatCompletion,
		Done:      false,
		IsPartial: true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Type: "function",
							ID:   string(id),
							Function: model.FunctionDefinitionParam{
								Name:      name,
								Arguments: arguments,
							},
						},
					},
				},
			},
		},
	}
	return event.NewResponseEvent(t.invocationID, t.author, response)
}

func (t *sessionUpdateTranslator) planEvent(
	update *acpsdk.SessionUpdatePlan,
) ([]*event.Event, error) {
	if update == nil {
		return nil, nil
	}
	raw, err := json.Marshal(update)
	if err != nil {
		return nil, fmt.Errorf("acpagent: marshal plan update: %w", err)
	}
	response := &model.Response{
		Object:    model.ObjectTypeChatCompletionChunk,
		Done:      false,
		IsPartial: true,
	}
	evt := event.NewResponseEvent(t.invocationID, t.author, response)
	evt.Extensions = map[string]json.RawMessage{
		acpPlanExtension: raw,
	}
	return []*event.Event{evt}, nil
}

func (t *sessionUpdateTranslator) finalEvent(
	response acpsdk.PromptResponse,
) *event.Event {
	finalResponse := &model.Response{
		ID:     "acpagent-final:" + t.invocationID,
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Usage:  modelUsageFromACP(response.Usage),
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:             model.RoleAssistant,
					Content:          t.text.String(),
					ReasoningContent: t.reasoning.String(),
					ContentParts:     append([]model.ContentPart(nil), t.contentParts...),
				},
			},
		},
	}
	return event.NewResponseEvent(t.invocationID, t.author, finalResponse)
}

func contentBlockText(block acpsdk.ContentBlock) string {
	if block.Text == nil {
		return ""
	}
	return block.Text.Text
}

func assistantContent(block acpsdk.ContentBlock) (string, []model.ContentPart) {
	if block.Text != nil {
		return block.Text.Text, nil
	}
	if block.ResourceLink != nil {
		part := model.ContentPart{
			Type: model.ContentTypeFile,
			File: &model.File{
				Name: block.ResourceLink.Name,
				URL:  block.ResourceLink.Uri,
			},
		}
		if block.ResourceLink.MimeType != nil {
			part.File.MimeType = *block.ResourceLink.MimeType
		}
		return "", []model.ContentPart{part}
	}
	return "", nil
}

func preferredToolName(meta map[string]any, title string, fallback string) string {
	if name, ok := meta[permissionMetaToolName].(string); ok {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	if title = strings.TrimSpace(title); title != "" {
		return title
	}
	return fallback
}

func marshalOptionalJSON(value any) ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(value)
}

func modelUsageFromACP(usage *acpsdk.Usage) *model.Usage {
	if usage == nil {
		return nil
	}
	converted := &model.Usage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if usage.ThoughtTokens != nil {
		converted.CompletionTokensDetails.ReasoningTokens = *usage.ThoughtTokens
	}
	return converted
}
