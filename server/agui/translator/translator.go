//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package translator translates trpc-agent-go events to AG-UI events.
package translator

import (
	"errors"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Translator translates trpc-agent-go events to AG-UI events.
type Translator interface {
	// Translate translates a trpc-agent-go event to AG-UI events.
	Translate(event *agentevent.Event) ([]aguievents.Event, error)
}

// New creates a new event translator.
func New(threadID, runID string) Translator {
	return &translator{
		threadID: threadID,
		runID:    runID,
	}
}

// translator is the default implementation of the Translator.
type translator struct {
	threadID         string
	runID            string
	lastMessageID    string
	receivingMessage bool
}

// Translate translates one trpc-agent-go event into zero or more AG-UI events.
func (t *translator) Translate(event *agentevent.Event) ([]aguievents.Event, error) {
	if event == nil || event.Response == nil {
		return nil, errors.New("event is nil")
	}
	rsp := event.Response
	if rsp.Error != nil {
		log.Errorf("agui: threadID: %s, runID: %s, error in response: %v", t.threadID, t.runID, rsp.Error)
		return []aguievents.Event{aguievents.NewRunErrorEvent(rsp.Error.Message, aguievents.WithRunID(t.runID))}, nil
	}
	events := []aguievents.Event{}
	if rsp.Object == model.ObjectTypeChatCompletionChunk || rsp.Object == model.ObjectTypeChatCompletion {
		textMessageEvents, err := t.textMessageEvent(rsp)
		if err != nil {
			return nil, err
		}
		events = append(events, textMessageEvents...)
	}
	if rsp.IsToolCallResponse() {
		toolCallEvents, err := t.toolCallEvent(rsp)
		if err != nil {
			return nil, err
		}
		events = append(events, toolCallEvents...)
	}
	if rsp.IsToolResultResponse() {
		toolResultEvents, err := t.toolResultEvent(rsp)
		if err != nil {
			return nil, err
		}
		events = append(events, toolResultEvents...)
	}
	if event.IsRunnerCompletion() {
		if t.receivingMessage {
			events = append(events, aguievents.NewTextMessageEndEvent(t.lastMessageID))
		}
		events = append(events, aguievents.NewRunFinishedEvent(t.threadID, t.runID))
	}
	return events, nil
}

// textMessageEvent translates a text message trpc-agent-go event to AG-UI events.
func (t *translator) textMessageEvent(rsp *model.Response) ([]aguievents.Event, error) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return nil, nil
	}
	var events []aguievents.Event
	// Different message ID means a new message.
	if t.lastMessageID != rsp.ID {
		t.lastMessageID = rsp.ID
		switch rsp.Object {
		case model.ObjectTypeChatCompletionChunk:
			t.receivingMessage = true
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
		t.receivingMessage = false
		events = append(events, aguievents.NewTextMessageEndEvent(rsp.ID))
	default:
		return nil, errors.New("invalid response object")
	}
	return events, nil
}

// toolCallEvent translates a tool call trpc-agent-go event to AG-UI events.
func (t *translator) toolCallEvent(rsp *model.Response) ([]aguievents.Event, error) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return nil, nil
	}
	events := make([]aguievents.Event, 0, len(rsp.Choices))
	for _, choice := range rsp.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			// Tool Call Start Event.
			startOpt := []aguievents.ToolCallStartOption{aguievents.WithParentMessageID(rsp.ID)}
			toolCallStartEvent := aguievents.NewToolCallStartEvent(toolCall.ID, toolCall.Function.Name, startOpt...)
			events = append(events, toolCallStartEvent)
			// Tool Call Arguments Event.
			toolCallArguments := formatToolCallArguments(toolCall.Function.Arguments)
			if toolCallArguments != "" {
				events = append(events, aguievents.NewToolCallArgsEvent(toolCall.ID, toolCallArguments))
			}
		}
	}
	t.lastMessageID = rsp.ID
	return events, nil
}

// toolResultEvent translates a tool result trpc-agent-go event to AG-UI events.
func (t *translator) toolResultEvent(rsp *model.Response) ([]aguievents.Event, error) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return nil, nil
	}
	eventas := make([]aguievents.Event, 0, len(rsp.Choices))
	for _, choice := range rsp.Choices {
		// Tool call end event.
		eventas = append(eventas, aguievents.NewToolCallEndEvent(choice.Message.ToolID))
		// Tool call result event.
		eventas = append(eventas, aguievents.NewToolCallResultEvent(t.lastMessageID,
			choice.Message.ToolID, choice.Message.Content))
	}
	return eventas, nil
}

// formatToolCallArguments formats a tool call arguments event to a string.
func formatToolCallArguments(arguments []byte) string {
	if len(arguments) == 0 {
		return ""
	}
	return string(arguments)
}
