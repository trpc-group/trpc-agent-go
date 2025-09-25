//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package event

import (
	"encoding/json"
	"errors"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Translate translates one trpc-agent-go event into zero or more AG-UI events.
func (b *bridge) Translate(event *agentevent.Event) ([]aguievents.Event, error) {
	if event == nil || event.Response == nil {
		return nil, errors.New("event is nil")
	}
	rsp := event.Response
	if rsp.Error != nil {
		return []aguievents.Event{b.NewRunErrorEvent(rsp.Error.Message)}, nil
	}
	events := []aguievents.Event{}
	if rsp.Object == model.ObjectTypeChatCompletionChunk || rsp.Object == model.ObjectTypeChatCompletion {
		textMessageEvents, err := b.textMessageEvent(rsp)
		if err != nil {
			return nil, err
		}
		events = append(events, textMessageEvents...)
	}
	if rsp.IsToolCallResponse() {
		toolCallEvents, err := b.toolCallEvent(rsp)
		if err != nil {
			return nil, err
		}
		events = append(events, toolCallEvents...)
	}
	if rsp.IsToolResultResponse() {
		toolResultEvents, err := b.toolResultEvent(rsp)
		if err != nil {
			return nil, err
		}
		events = append(events, toolResultEvents...)
	}
	if rsp.IsFinalResponse() {
		events = append(events, b.NewRunFinishedEvent())
	}
	return events, nil
}

// textMessageEvent translates a text message trpc-agent-go event to AG-UI events.
func (b *bridge) textMessageEvent(rsp *model.Response) ([]aguievents.Event, error) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return nil, nil
	}
	var events []aguievents.Event
	// Different message ID means a new message.
	if b.lastMessageID != rsp.ID {
		b.lastMessageID = rsp.ID
		switch rsp.Object {
		case model.ObjectTypeChatCompletionChunk:
			role := rsp.Choices[0].Delta.Role.String()
			events = append(events, aguievents.NewTextMessageStartEvent(rsp.ID, aguievents.WithRole(role)))
		case model.ObjectTypeChatCompletion:
			if rsp.Choices[0].Message.Content == "" {
				return nil, nil
			}
			role := rsp.Choices[0].Message.Role.String()
			events = append(events,
				aguievents.NewTextMessageStartEvent(rsp.ID, aguievents.WithRole(role)),
				aguievents.NewTextMessageContentEvent(rsp.ID, rsp.Choices[0].Message.Content),
				aguievents.NewTextMessageEndEvent(rsp.ID),
			)
			return events, nil
		default:
			return nil, errors.New("invalid response object")
		}
	}
	// Streaming response.
	switch rsp.Object {
	// Streaming chunk.
	case model.ObjectTypeChatCompletionChunk:
		if rsp.Choices[0].Delta.Content != "" {
			events = append(events, aguievents.NewTextMessageContentEvent(rsp.ID, rsp.Choices[0].Delta.Content))
		}
	// For streaming response, don't need to emit final completion event.
	// It means the response is ended.
	case model.ObjectTypeChatCompletion:
		events = append(events, aguievents.NewTextMessageEndEvent(rsp.ID))
	default:
		return nil, errors.New("invalid response object")
	}
	return events, nil
}

// toolCallEvent translates a tool call trpc-agent-go event to AG-UI events.
func (b *bridge) toolCallEvent(rsp *model.Response) ([]aguievents.Event, error) {
	var events []aguievents.Event
	toolCall := rsp.Choices[0].Message.ToolCalls[0]
	// Tool call start event.
	var startOpt []aguievents.ToolCallStartOption
	startOpt = append(startOpt, aguievents.WithParentMessageID(rsp.ID))
	events = append(events, aguievents.NewToolCallStartEvent(toolCall.ID, toolCall.Function.Name, startOpt...))
	// Tool call arguments event.
	toolCallArguments := formatToolCallArguments(toolCall.Function.Arguments)
	events = append(events, aguievents.NewToolCallArgsEvent(toolCall.ID, toolCallArguments))
	b.lastMessageID = rsp.ID
	return events, nil
}

// toolResultEvent translates a tool result trpc-agent-go event to AG-UI events.
func (b *bridge) toolResultEvent(rsp *model.Response) ([]aguievents.Event, error) {
	var events []aguievents.Event
	choice := rsp.Choices[0]
	// Tool call end event.
	events = append(events, aguievents.NewToolCallEndEvent(choice.Message.ToolID))
	// Tool call result event.
	events = append(events, aguievents.NewToolCallResultEvent(b.lastMessageID, choice.Message.ToolID, choice.Message.Content))
	return events, nil
}

// formatToolCallArguments formats a tool call arguments event to a string.
func formatToolCallArguments(arguments []byte) string {
	if len(arguments) == 0 {
		return ""
	}
	var obj any
	if err := json.Unmarshal(arguments, &obj); err == nil {
		if pretty, err := json.Marshal(obj); err == nil {
			return string(pretty)
		}
	}
	return string(arguments)
}
