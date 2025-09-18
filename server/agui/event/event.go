//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package event provides the event translation from runner events to AG-UI events.
package event

import (
	"encoding/json"
	"strings"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Translator converts runner events into AG-UI events.
type Translator interface {
	FromRunnerEvent(evt *agentevent.Event) []aguievents.Event
	Finalize() []aguievents.Event
}

// defaultTranslator maps runner events into AG-UI event sequence.
type defaultTranslator struct {
	threadID      string
	runID         string
	messageID     string
	messageActive bool
	toolCalls     map[string]string
}

// NewTranslator returns the default translator implementation.
func NewTranslator(threadID, runID string) Translator {
	return &defaultTranslator{
		threadID:  threadID,
		runID:     runID,
		toolCalls: make(map[string]string),
	}
}

// FromRunnerEvent converts one runner event into zero or more AG-UI events.
func (t *defaultTranslator) FromRunnerEvent(evt *agentevent.Event) []aguievents.Event {
	if evt == nil {
		return nil
	}
	var out []aguievents.Event
	if snapshot := t.stateSnapshotEvent(evt); snapshot != nil {
		out = append(out, snapshot)
	}
	if evt.Response != nil {
		resp := evt.Response
		if resp.Error != nil {
			out = append(out, aguievents.NewRunErrorEvent(resp.Error.Message, aguievents.WithRunID(t.runID)))
			return out
		}
		if resp.IsToolCallResponse() {
			out = append(out, t.translateToolCall(resp)...)
			return out
		}
		if resp.IsToolResultResponse() {
			out = append(out, t.translateToolResult(resp)...)
			return out
		}
		if delta := extractTextFromResponse(resp); delta != "" {
			out = append(out, t.emitAssistantText(delta, resp.IsPartial)...)
			if !resp.IsPartial {
				out = append(out, t.finishMessage()...)
			}
			return out
		}
	}
	return out
}

// Finalize emits any trailing events required to finish the stream.
func (t *defaultTranslator) Finalize() []aguievents.Event {
	return t.finishMessage()
}

func (t *defaultTranslator) emitAssistantText(delta string, partial bool) []aguievents.Event {
	if strings.TrimSpace(delta) == "" {
		return nil
	}
	var events []aguievents.Event
	if !t.messageActive {
		t.messageID = aguievents.GenerateMessageID()
		start := aguievents.NewTextMessageStartEvent(t.messageID, aguievents.WithRole("assistant"))
		events = append(events, start)
		t.messageActive = true
	}
	events = append(events, aguievents.NewTextMessageContentEvent(t.messageID, delta))
	if !partial {
		events = append(events, t.finishMessage()...)
	}
	return events
}

func (t *defaultTranslator) finishMessage() []aguievents.Event {
	if !t.messageActive || t.messageID == "" {
		return nil
	}
	end := aguievents.NewTextMessageEndEvent(t.messageID)
	t.messageActive = false
	t.messageID = ""
	return []aguievents.Event{end}
}

func (t *defaultTranslator) stateSnapshotEvent(evt *agentevent.Event) aguievents.Event {
	if len(evt.StateDelta) == 0 {
		return nil
	}
	snapshot := make(map[string]any, len(evt.StateDelta))
	for key, val := range evt.StateDelta {
		if len(val) == 0 {
			snapshot[key] = ""
			continue
		}
		var decoded any
		if err := json.Unmarshal(val, &decoded); err == nil {
			snapshot[key] = decoded
			continue
		}
		snapshot[key] = string(val)
	}
	return aguievents.NewStateSnapshotEvent(snapshot)
}

func (t *defaultTranslator) translateToolCall(resp *model.Response) []aguievents.Event {
	var events []aguievents.Event
	for _, choice := range resp.Choices {
		calls := choice.Delta.ToolCalls
		if len(calls) == 0 {
			calls = choice.Message.ToolCalls
		}
		for _, tc := range calls {
			id := tc.ID
			if id == "" {
				id = aguievents.GenerateToolCallID()
			}
			name := tc.Function.Name
			if name == "" {
				name = "tool"
			}
			if _, exists := t.toolCalls[id]; !exists {
				events = append(events, aguievents.NewToolCallStartEvent(id, name))
				t.toolCalls[id] = name
			}
			if len(tc.Function.Arguments) > 0 {
				if args := formatArguments(tc.Function.Arguments); args != "" {
					events = append(events, aguievents.NewToolCallArgsEvent(id, args))
				}
			}
			if !resp.IsPartial {
				events = append(events, aguievents.NewToolCallEndEvent(id))
				delete(t.toolCalls, id)
			}
		}
	}
	return events
}

func (t *defaultTranslator) translateToolResult(resp *model.Response) []aguievents.Event {
	var events []aguievents.Event
	for _, choice := range resp.Choices {
		msg := choice.Message
		if msg.ToolID == "" {
			continue
		}
		content := textFromMessage(msg)
		if content == "" {
			continue
		}
		messageID := msg.ToolID
		if messageID == "" {
			messageID = aguievents.GenerateMessageID()
		}
		events = append(events, aguievents.NewToolCallResultEvent(messageID, msg.ToolID, content))
	}
	return events
}

func formatArguments(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var obj any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if pretty, err := json.Marshal(obj); err == nil {
			return string(pretty)
		}
	}
	return string(raw)
}

func extractTextFromResponse(resp *model.Response) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	choice := resp.Choices[0]
	if resp.IsPartial {
		if msg := textFromMessage(choice.Delta); msg != "" {
			return msg
		}
	}
	return textFromMessage(choice.Message)
}

func textFromMessage(msg model.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	if len(msg.ContentParts) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, part := range msg.ContentParts {
		if part.Text != nil {
			builder.WriteString(*part.Text)
		}
	}
	return builder.String()
}
