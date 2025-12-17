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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Translator translates trpc-agent-go events to AG-UI events.
type Translator interface {
	// Translate translates a trpc-agent-go event to AG-UI events.
	Translate(ctx context.Context, event *agentevent.Event) ([]aguievents.Event, error)
}

// New creates a new event translator.
func New(ctx context.Context, threadID, runID string) Translator {
	return &translator{
		threadID:         threadID,
		runID:            runID,
		lastMessageID:    "",
		receivingMessage: false,
		seenResponseIDs:  make(map[string]struct{}),
		seenToolCallIDs:  make(map[string]struct{}),
	}
}

// translator is the default implementation of the Translator.
type translator struct {
	threadID         string
	runID            string
	lastMessageID    string
	receivingMessage bool
	seenResponseIDs  map[string]struct{}
	seenToolCallIDs  map[string]struct{}
}

// Translate translates one trpc-agent-go event into zero or more AG-UI events.
func (t *translator) Translate(ctx context.Context, event *agentevent.Event) ([]aguievents.Event, error) {
	if event == nil {
		return nil, errors.New("event is nil")
	}

	var events []aguievents.Event
	hasGraphDelta := event.StateDelta != nil &&
		(len(event.StateDelta[graph.MetadataKeyModel]) > 0 ||
			len(event.StateDelta[graph.MetadataKeyTool]) > 0 ||
			len(event.StateDelta[graph.MetadataKeyNodeCustom]) > 0)

	// GraphAgent emits model/tool metadata via StateDelta instead of raw tool_calls.
	events = append(events, t.graphModelEvents(event)...)
	events = append(events, t.graphToolEvents(event)...)
	// Handle node custom events (progress, text, custom).
	events = append(events, t.graphNodeCustomEvents(event)...)

	rsp := event.Response
	if rsp == nil {
		if len(events) > 0 || hasGraphDelta {
			return events, nil
		}
		return nil, errors.New("event response is nil")
	}
	if rsp.Error != nil {
		log.Errorf("agui: threadID: %s, runID: %s, error in response: %v", t.threadID, t.runID, rsp.Error)
		events = append(events, aguievents.NewRunErrorEvent(rsp.Error.Message, aguievents.WithRunID(t.runID)))
		return events, nil
	}
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
		toolResultEvents, err := t.toolResultEvent(rsp, event.ID)
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
	t.recordResponseID(rsp.ID)
	var events []aguievents.Event
	// Different message ID means a new message.
	if t.lastMessageID != rsp.ID {
		switch rsp.Object {
		case model.ObjectTypeChatCompletionChunk:
			if rsp.Choices[0].Delta.Content == "" {
				return nil, nil
			}
			if t.receivingMessage {
				events = append(events, aguievents.NewTextMessageEndEvent(t.lastMessageID))
				t.receivingMessage = false
			}
			t.lastMessageID = rsp.ID
			t.receivingMessage = true
			role := rsp.Choices[0].Delta.Role.String()
			events = append(events, aguievents.NewTextMessageStartEvent(rsp.ID, aguievents.WithRole(role)))
		case model.ObjectTypeChatCompletion:
			if rsp.Choices[0].Message.Content == "" {
				return nil, nil
			}
			if t.receivingMessage {
				events = append(events, aguievents.NewTextMessageEndEvent(t.lastMessageID))
				t.receivingMessage = false
			}
			t.lastMessageID = rsp.ID
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
		if rsp.Choices[0].FinishReason != nil && *rsp.Choices[0].FinishReason != "" {
			t.receivingMessage = false
			events = append(events, aguievents.NewTextMessageEndEvent(rsp.ID))
		}
	// For streaming response, don't need to emit final completion event.
	// It means the response is ended.
	case model.ObjectTypeChatCompletion:
		if t.receivingMessage {
			t.receivingMessage = false
			events = append(events, aguievents.NewTextMessageEndEvent(rsp.ID))
		}
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
			t.recordToolCallID(toolCall.ID)
			// Tool Call Start Event.
			startOpt := []aguievents.ToolCallStartOption{aguievents.WithParentMessageID(rsp.ID)}
			toolCallStartEvent := aguievents.NewToolCallStartEvent(toolCall.ID, toolCall.Function.Name, startOpt...)
			events = append(events, toolCallStartEvent)
			// Tool Call Arguments Event.
			toolCallArguments := formatToolCallArguments(toolCall.Function.Arguments)
			if toolCallArguments != "" {
				events = append(events, aguievents.NewToolCallArgsEvent(toolCall.ID, toolCallArguments))
			}
			// Tool call end should precede result to align with AG-UI protocol.
			events = append(events, aguievents.NewToolCallEndEvent(toolCall.ID))
		}
	}
	t.lastMessageID = rsp.ID
	return events, nil
}

// toolResultEvent translates a tool result trpc-agent-go event to AG-UI events.
func (t *translator) toolResultEvent(rsp *model.Response, messageID string) ([]aguievents.Event, error) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return nil, nil
	}
	events := make([]aguievents.Event, 0, len(rsp.Choices))
	for _, choice := range rsp.Choices {
		events = append(events, aguievents.NewToolCallResultEvent(messageID,
			choice.Message.ToolID, choice.Message.Content))
	}
	t.lastMessageID = messageID
	return events, nil
}

// formatToolCallArguments formats a tool call arguments event to a string.
func formatToolCallArguments(arguments []byte) string {
	if len(arguments) == 0 {
		return ""
	}
	return string(arguments)
}

// graphModelEvents converts graph model metadata (from StateDelta) into text events.
func (t *translator) graphModelEvents(evt *agentevent.Event) []aguievents.Event {
	if evt.StateDelta == nil {
		return nil
	}
	raw, ok := evt.StateDelta[graph.MetadataKeyModel]
	if !ok || len(raw) == 0 {
		return nil
	}
	var meta graph.ModelExecutionMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return []aguievents.Event{aguievents.NewRunErrorEvent(
			fmt.Sprintf("invalid graph model metadata: %v", err),
			aguievents.WithRunID(t.runID),
		)}
	}
	if meta.Output == "" {
		return nil
	}
	responseID := meta.ResponseID
	if t.hasSeenResponseID(responseID) {
		return nil
	}
	var events []aguievents.Event
	if t.receivingMessage && t.lastMessageID != responseID {
		events = append(events, aguievents.NewTextMessageEndEvent(t.lastMessageID))
		t.receivingMessage = false
	}
	events = append(events,
		aguievents.NewTextMessageStartEvent(responseID, aguievents.WithRole(model.RoleAssistant.String())),
		aguievents.NewTextMessageContentEvent(responseID, meta.Output),
		aguievents.NewTextMessageEndEvent(responseID),
	)
	t.lastMessageID = responseID
	t.recordResponseID(responseID)
	return events
}

// graphToolEvents converts graph tool metadata (from StateDelta) into tool call events.
func (t *translator) graphToolEvents(evt *agentevent.Event) []aguievents.Event {
	if evt.StateDelta == nil {
		return nil
	}
	raw, ok := evt.StateDelta[graph.MetadataKeyTool]
	if !ok || len(raw) == 0 {
		return nil
	}
	var meta graph.ToolExecutionMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return []aguievents.Event{aguievents.NewRunErrorEvent(
			fmt.Sprintf("invalid graph tool metadata: %v", err),
			aguievents.WithRunID(t.runID),
		)}
	}
	if t.hasSeenToolCallID(meta.ToolID) {
		return nil
	}

	switch meta.Phase {
	case graph.ToolExecutionPhaseStart:
		var events []aguievents.Event
		opts := []aguievents.ToolCallStartOption{aguievents.WithParentMessageID(meta.ResponseID)}
		events = append(events, aguievents.NewToolCallStartEvent(meta.ToolID, meta.ToolName, opts...))
		if strings.TrimSpace(meta.Input) != "" {
			events = append(events, aguievents.NewToolCallArgsEvent(meta.ToolID, meta.Input))
		}
		events = append(events, aguievents.NewToolCallEndEvent(meta.ToolID))
		t.recordToolCallID(meta.ToolID)
		return events
	default:
		return nil
	}
}

func (t *translator) recordResponseID(id string) {
	t.seenResponseIDs[id] = struct{}{}
}

func (t *translator) hasSeenResponseID(id string) bool {
	_, ok := t.seenResponseIDs[id]
	return ok
}

func (t *translator) recordToolCallID(id string) {
	t.seenToolCallIDs[id] = struct{}{}
}

func (t *translator) hasSeenToolCallID(id string) bool {
	_, ok := t.seenToolCallIDs[id]
	return ok
}

// graphNodeCustomEvents converts graph node custom metadata (from StateDelta) into AG-UI events.
// It handles three types of node custom events:
//   - Custom events: Converted to AG-UI Custom events with full payload
//   - Progress events: Converted to AG-UI Custom events with progress information
//   - Text events: Converted to TextMessageContent events if in message context,
//     otherwise converted to AG-UI Custom events
func (t *translator) graphNodeCustomEvents(evt *agentevent.Event) []aguievents.Event {
	if evt.StateDelta == nil {
		return nil
	}
	raw, ok := evt.StateDelta[graph.MetadataKeyNodeCustom]
	if !ok || len(raw) == 0 {
		return nil
	}
	var meta graph.NodeCustomEventMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return []aguievents.Event{aguievents.NewRunErrorEvent(
			fmt.Sprintf("invalid graph node custom metadata: %v", err),
			aguievents.WithRunID(t.runID),
		)}
	}

	switch meta.Category {
	case graph.NodeCustomEventCategoryProgress:
		return t.handleProgressEvent(meta)
	case graph.NodeCustomEventCategoryText:
		return t.handleTextEvent(meta)
	default:
		return t.handleCustomEvent(meta)
	}
}

// handleProgressEvent converts a progress event to AG-UI Custom events.
func (t *translator) handleProgressEvent(meta graph.NodeCustomEventMetadata) []aguievents.Event {
	eventType := "node.progress"
	if meta.EventType != "" {
		eventType = meta.EventType
	}

	payload := map[string]any{
		"nodeId":   meta.NodeID,
		"progress": meta.Progress,
		"message":  meta.Message,
	}
	if meta.StepNumber > 0 {
		payload["stepNumber"] = meta.StepNumber
	}

	return []aguievents.Event{
		aguievents.NewCustomEvent(eventType, aguievents.WithValue(payload)),
	}
}

// handleTextEvent converts a text event to AG-UI events.
// If currently receiving a message, it emits a TextMessageContent event;
// otherwise, it emits a Custom event.
func (t *translator) handleTextEvent(meta graph.NodeCustomEventMetadata) []aguievents.Event {
	// If we're currently in a message context and the text is from the same
	// message context, emit as TextMessageContent for seamless streaming.
	if t.receivingMessage && meta.Message != "" {
		return []aguievents.Event{
			aguievents.NewTextMessageContentEvent(t.lastMessageID, meta.Message),
		}
	}

	// Otherwise emit as Custom event with text content.
	eventType := "node.text"
	if meta.EventType != "" {
		eventType = meta.EventType
	}

	payload := map[string]any{
		"nodeId":  meta.NodeID,
		"content": meta.Message,
	}
	if meta.StepNumber > 0 {
		payload["stepNumber"] = meta.StepNumber
	}

	return []aguievents.Event{
		aguievents.NewCustomEvent(eventType, aguievents.WithValue(payload)),
	}
}

// handleCustomEvent converts a generic custom event to AG-UI Custom events.
func (t *translator) handleCustomEvent(meta graph.NodeCustomEventMetadata) []aguievents.Event {
	eventType := "node.custom"
	if meta.EventType != "" {
		eventType = meta.EventType
	}

	payload := map[string]any{
		"nodeId": meta.NodeID,
	}
	if meta.Payload != nil {
		payload["payload"] = meta.Payload
	}
	if meta.Message != "" {
		payload["message"] = meta.Message
	}
	if meta.StepNumber > 0 {
		payload["stepNumber"] = meta.StepNumber
	}
	payload["timestamp"] = meta.Timestamp

	return []aguievents.Event{
		aguievents.NewCustomEvent(eventType, aguievents.WithValue(payload)),
	}
}
