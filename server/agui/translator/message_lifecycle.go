//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

import (
	"errors"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func (t *translator) serialPostRunFinalizationEvents() []aguievents.Event {
	var events []aguievents.Event
	if t.receivingReasoning {
		if t.reasoningContentEnabled {
			events = append(events,
				aguievents.NewReasoningMessageEndEvent(t.lastReasoningMessageID),
				aguievents.NewReasoningEndEvent(t.lastReasoningMessageID),
			)
		}
		t.receivingReasoning = false
	}
	events = append(events, t.closeCurrentTextStream()...)
	if t.toolCallDeltaStreamingEnabled {
		events = append(events, t.closeOpenToolCallDeltas()...)
	}
	return events
}

func (t *translator) postRunFinalizationEvents() []aguievents.Event {
	if t.concurrentMessageStreamsEnabled {
		return t.concurrentPostRunFinalizationEvents()
	}
	return t.serialPostRunFinalizationEvents()
}

func (t *translator) closeTextStreamsBeforeQueuedUserMessage() []aguievents.Event {
	if t.concurrentMessageStreamsEnabled {
		events := t.closeOpenTextStreams()
		t.clearGraphTextAppendTarget()
		return events
	}
	return t.closeCurrentTextStream()
}

func (t *translator) translateReasoningMessageEvents(rsp *model.Response) ([]aguievents.Event, error) {
	if t.concurrentMessageStreamsEnabled {
		return t.concurrentReasoningEvents(rsp)
	}
	return t.reasoningEvents(rsp)
}

func (t *translator) translateTextMessageEvents(rsp *model.Response) ([]aguievents.Event, error) {
	if t.concurrentMessageStreamsEnabled {
		return t.concurrentTextMessageEvent(rsp)
	}
	return t.textMessageEvent(rsp)
}

func (t *translator) graphModelBoundaryEvents(responseID string) []aguievents.Event {
	if t.concurrentMessageStreamsEnabled {
		return nil
	}
	return t.closeCurrentTextStreamOnMessageSwitch(responseID)
}

func (t *translator) recordGraphModelResponseID(responseID string) {
	t.recordClosedTextMessageID(responseID)
	t.recordResponseID(responseID)
}

func (t *translator) recordClosedMessageID(messageID string) {
	t.lastMessageID = messageID
	if t.concurrentMessageStreamsEnabled {
		t.clearGraphTextAppendTarget()
	}
}

func (t *translator) recordClosedTextMessageID(messageID string) {
	t.recordClosedMessageID(messageID)
	if t.concurrentMessageStreamsEnabled {
		t.textStreams.markStarted(messageID)
	}
}

func (t *translator) openTextStream(messageID string) {
	t.textStreams.markOpen(messageID)
	t.recordTextStreamChunk(messageID)
}

func (t *translator) recordTextStreamChunk(messageID string) {
	t.lastMessageID = messageID
	t.receivingMessage = true
	if t.concurrentMessageStreamsEnabled {
		t.graphTextAppendTargetID = messageID
	}
}

func (t *translator) clearGraphTextAppendTarget() {
	t.graphTextAppendTargetID = ""
}

func (t *translator) graphTextAppendMessageID() (string, bool) {
	if t.concurrentMessageStreamsEnabled {
		messageID, ok := t.textStreams.singleOpenMessageID()
		if !ok || messageID != t.graphTextAppendTargetID {
			return "", false
		}
		return messageID, true
	}
	if !t.receivingMessage {
		return "", false
	}
	return t.lastMessageID, true
}

func (t *translator) concurrentPostRunFinalizationEvents() []aguievents.Event {
	var events []aguievents.Event
	events = append(events, t.closeOpenReasoningStreams()...)
	events = append(events, t.closeOpenTextStreams()...)
	if t.toolCallDeltaStreamingEnabled {
		events = append(events, t.closeOpenToolCallDeltas()...)
	}
	return events
}

func (t *translator) closeCurrentTextStream() []aguievents.Event {
	if !t.receivingMessage {
		return nil
	}
	messageID := t.lastMessageID
	t.receivingMessage = false
	return []aguievents.Event{aguievents.NewTextMessageEndEvent(messageID)}
}

func (t *translator) closeCurrentTextStreamOnMessageSwitch(nextMessageID string) []aguievents.Event {
	if !t.receivingMessage || t.lastMessageID == nextMessageID {
		return nil
	}
	return t.closeCurrentTextStream()
}

func (t *translator) closeTextStream(messageID string) []aguievents.Event {
	if !t.textStreams.close(messageID) {
		return nil
	}
	if t.graphTextAppendTargetID == messageID {
		t.clearGraphTextAppendTarget()
	}
	t.receivingMessage = t.textStreams.hasOpen()
	if t.receivingMessage {
		t.lastMessageID = t.textStreams.latestOpen()
	} else {
		t.lastMessageID = messageID
	}
	return []aguievents.Event{aguievents.NewTextMessageEndEvent(messageID)}
}

func (t *translator) closeOpenTextStreams() []aguievents.Event {
	var events []aguievents.Event
	for _, messageID := range t.textStreams.order {
		events = append(events, t.closeTextStream(messageID)...)
	}
	return events
}

func (t *translator) closeReasoningStream(messageID string) []aguievents.Event {
	if !t.reasoningStreams.close(messageID) {
		return nil
	}
	t.receivingReasoning = t.reasoningStreams.hasOpen()
	if t.receivingReasoning {
		t.lastReasoningMessageID = t.reasoningStreams.latestOpen()
	} else {
		t.lastReasoningMessageID = messageID
	}
	return []aguievents.Event{
		aguievents.NewReasoningMessageEndEvent(messageID),
		aguievents.NewReasoningEndEvent(messageID),
	}
}

func (t *translator) closeOpenReasoningStreams() []aguievents.Event {
	var events []aguievents.Event
	for _, messageID := range t.reasoningStreams.order {
		events = append(events, t.closeReasoningStream(messageID)...)
	}
	return events
}

func (t *translator) concurrentReasoningEvents(rsp *model.Response) ([]aguievents.Event, error) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return nil, nil
	}
	if rsp.ID == "" {
		return nil, nil
	}
	reasoningID := rsp.ID
	wasStarted := t.reasoningStreams.hasStarted(reasoningID)
	choice := rsp.Choices[0]
	reasoningDelta := ""
	contentDelta := ""
	if rsp.Object == model.ObjectTypeChatCompletionChunk {
		reasoningDelta = choice.Delta.ReasoningContent
		contentDelta = choice.Delta.Content
	} else {
		reasoningDelta = choice.Message.ReasoningContent
		contentDelta = choice.Message.Content
	}
	var events []aguievents.Event
	switch rsp.Object {
	case model.ObjectTypeChatCompletionChunk:
		if reasoningDelta != "" {
			if !t.reasoningStreams.isOpen(reasoningID) {
				if wasStarted {
					return nil, nil
				}
				t.reasoningStreams.markOpen(reasoningID)
				t.lastReasoningMessageID = reasoningID
				t.receivingReasoning = true
				events = append(events,
					aguievents.NewReasoningStartEvent(reasoningID),
					aguievents.NewReasoningMessageStartEvent(reasoningID, string(aguitypes.RoleReasoning)),
				)
			}
			events = append(events, aguievents.NewReasoningMessageContentEvent(reasoningID, reasoningDelta))
		}
		if t.reasoningStreams.isOpen(reasoningID) {
			shouldEnd := false
			if contentDelta != "" {
				shouldEnd = true
			}
			if rsp.IsToolCallResponse() {
				shouldEnd = true
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				shouldEnd = true
			}
			if shouldEnd {
				events = append(events, t.closeReasoningStream(reasoningID)...)
			}
		}
	case model.ObjectTypeChatCompletion:
		if t.reasoningStreams.isOpen(reasoningID) {
			events = append(events, t.closeReasoningStream(reasoningID)...)
			return events, nil
		}
		if reasoningDelta == "" || wasStarted {
			return nil, nil
		}
		t.reasoningStreams.markStarted(reasoningID)
		t.lastReasoningMessageID = reasoningID
		events = append(events,
			aguievents.NewReasoningStartEvent(reasoningID),
			aguievents.NewReasoningMessageStartEvent(reasoningID, string(aguitypes.RoleReasoning)),
			aguievents.NewReasoningMessageContentEvent(reasoningID, reasoningDelta),
			aguievents.NewReasoningMessageEndEvent(reasoningID),
			aguievents.NewReasoningEndEvent(reasoningID),
		)
	default:
		return nil, errors.New("invalid response object")
	}
	return events, nil
}

func (t *translator) concurrentTextMessageEvent(rsp *model.Response) ([]aguievents.Event, error) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return nil, nil
	}
	if rsp.ID == "" {
		return nil, nil
	}
	wasStarted := t.textStreams.hasStarted(rsp.ID)
	t.recordResponseID(rsp.ID)
	var events []aguievents.Event
	switch rsp.Object {
	case model.ObjectTypeChatCompletionChunk:
		if rsp.Choices[0].Delta.Content != "" {
			if !t.textStreams.isOpen(rsp.ID) {
				if wasStarted {
					return nil, nil
				}
				t.openTextStream(rsp.ID)
				role := rsp.Choices[0].Delta.Role.String()
				events = append(events, aguievents.NewTextMessageStartEvent(rsp.ID, aguievents.WithRole(role)))
			} else {
				t.recordTextStreamChunk(rsp.ID)
			}
			events = append(events, aguievents.NewTextMessageContentEvent(rsp.ID, rsp.Choices[0].Delta.Content))
		}
		if rsp.Choices[0].FinishReason != nil && *rsp.Choices[0].FinishReason != "" {
			events = append(events, t.closeTextStream(rsp.ID)...)
		}
	// For streaming response, don't need to emit final completion event.
	// It means the response is ended.
	case model.ObjectTypeChatCompletion:
		if t.textStreams.isOpen(rsp.ID) {
			events = append(events, t.closeTextStream(rsp.ID)...)
			return events, nil
		}
		if rsp.Choices[0].Message.Content == "" || wasStarted {
			return nil, nil
		}
		t.openTextStream(rsp.ID)
		role := rsp.Choices[0].Message.Role.String()
		events = append(events,
			aguievents.NewTextMessageStartEvent(rsp.ID, aguievents.WithRole(role)),
			aguievents.NewTextMessageContentEvent(rsp.ID, rsp.Choices[0].Message.Content),
		)
		events = append(events, t.closeTextStream(rsp.ID)...)
	default:
		return nil, errors.New("invalid response object")
	}
	return events, nil
}

type messageStreamState struct {
	// open contains message IDs currently between START and END.
	open map[string]struct{}
	// order preserves first-open order so finalization can close streams deterministically.
	order []string
	// startedIDs keeps message IDs that have emitted START, including closed streams.
	startedIDs map[string]struct{}
}

func newMessageStreamState() messageStreamState {
	return messageStreamState{
		open:       make(map[string]struct{}),
		startedIDs: make(map[string]struct{}),
	}
}

func (s *messageStreamState) isOpen(messageID string) bool {
	if s.open == nil {
		return false
	}
	_, ok := s.open[messageID]
	return ok
}

func (s *messageStreamState) markOpen(messageID string) {
	s.ensure()
	if _, ok := s.open[messageID]; !ok {
		s.open[messageID] = struct{}{}
		s.order = append(s.order, messageID)
	}
	s.startedIDs[messageID] = struct{}{}
}

func (s *messageStreamState) markStarted(messageID string) {
	s.ensure()
	s.startedIDs[messageID] = struct{}{}
}

func (s *messageStreamState) hasStarted(messageID string) bool {
	if s.startedIDs == nil {
		return false
	}
	_, ok := s.startedIDs[messageID]
	return ok
}

func (s *messageStreamState) close(messageID string) bool {
	if !s.isOpen(messageID) {
		return false
	}
	delete(s.open, messageID)
	if len(s.open) == 0 {
		s.order = nil
	}
	return true
}

func (s *messageStreamState) hasOpen() bool {
	return len(s.open) > 0
}

func (s *messageStreamState) singleOpenMessageID() (string, bool) {
	if len(s.open) != 1 {
		return "", false
	}
	messageID := s.latestOpen()
	return messageID, messageID != ""
}

func (s *messageStreamState) latestOpen() string {
	for i := len(s.order) - 1; i >= 0; i-- {
		messageID := s.order[i]
		if _, ok := s.open[messageID]; ok {
			return messageID
		}
	}
	return ""
}

func (s *messageStreamState) ensure() {
	if s.open == nil {
		s.open = make(map[string]struct{})
	}
	if s.startedIDs == nil {
		s.startedIDs = make(map[string]struct{})
	}
}
