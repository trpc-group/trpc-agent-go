//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reduce

import (
	"strings"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	testAppName = "demo-app"
	testUserID  = "user-42"
)

func TestBuildMessagesHappyPath(t *testing.T) {
	events := []session.TrackEvent{
		newTrackEvent(aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user"))),
		newTrackEvent(aguievents.NewTextMessageContentEvent("user-1", "hello")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("user-1")),
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewTextMessageContentEvent("assistant-1", "thinking")),
		newTrackEvent(aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("tool-call-1", "{\"a\":1}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("tool-call-1")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("assistant-1")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "42")),
	}

	msgs, err := Reduce(testAppName, testUserID, events)
	if err != nil {
		t.Fatalf("BuildMessages err: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if got := *msgs[0].Content; got != "hello" {
		t.Fatalf("unexpected user content %q", got)
	}
	if msgs[1].ToolCalls[0].Function.Arguments != "{\"a\":1}" {
		t.Fatalf("unexpected tool call args: %s", msgs[1].ToolCalls[0].Function.Arguments)
	}
	if *msgs[2].Content != "42" {
		t.Fatalf("unexpected tool result content %q", *msgs[2].Content)
	}
	if msgs[0].Name == nil || *msgs[0].Name != testUserID {
		t.Fatalf("expected user name %q, got %v", testUserID, msgs[0].Name)
	}
	if msgs[1].Name == nil || *msgs[1].Name != testAppName {
		t.Fatalf("expected assistant name %q, got %v", testAppName, msgs[1].Name)
	}
}

func TestReduceTextStartErrors(t *testing.T) {
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing id",
			events: trackEventsFrom(aguievents.NewTextMessageStartEvent("", aguievents.WithRole("user"))),
			want:   "text message start missing id",
		},
		{
			name: "duplicate id",
			events: trackEventsFrom(
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("user")),
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
			),
			want: "duplicate text message start: msg-1",
		},
		{
			name:   "unsupported role",
			events: trackEventsFrom(aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("tool"))),
			want:   "unsupported role: tool",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceTextContentErrors(t *testing.T) {
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing start",
			events: trackEventsFrom(aguievents.NewTextMessageContentEvent("ghost", "hi")),
			want:   "text message content without start: ghost",
		},
		{
			name: "after end",
			events: trackEventsFrom(
				aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
				aguievents.NewTextMessageContentEvent("user-1", "hello"),
				aguievents.NewTextMessageEndEvent("user-1"),
				aguievents.NewTextMessageContentEvent("user-1", "!"),
			),
			want: "text message content after end: user-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceTextEndErrors(t *testing.T) {
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing start",
			events: trackEventsFrom(aguievents.NewTextMessageEndEvent("ghost")),
			want:   "text message end without start: ghost",
		},
		{
			name: "duplicate end",
			events: trackEventsFrom(
				aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
				aguievents.NewTextMessageEndEvent("user-1"),
				aguievents.NewTextMessageEndEvent("user-1"),
			),
			want: "duplicate text message end: user-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceFinalizeErrors(t *testing.T) {
	t.Run("text not closed", func(t *testing.T) {
		events := trackEventsFrom(aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")))
		assertReduceError(t, events, "text message user-1 not closed")
	})
	t.Run("tool not completed", func(t *testing.T) {
		events := trackEventsFrom(
			aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
			aguievents.NewTextMessageEndEvent("user-1"),
			aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
			aguievents.NewTextMessageEndEvent("assistant-1"),
			aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
			aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
			aguievents.NewToolCallEndEvent("tool-call-1"),
		)
		assertReduceError(t, events, "tool call tool-call-1 not completed")
	})
}

func TestReduceToolStartErrors(t *testing.T) {
	assistant := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewTextMessageEndEvent("assistant-1"),
	)
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name: "missing id",
			events: combineEvents(assistant,
				trackEventsFrom(aguievents.NewToolCallStartEvent("", "calc", aguievents.WithParentMessageID("assistant-1"))),
			),
			want: "tool call start missing id",
		},
		{
			name: "duplicate start",
			events: combineEvents(assistant, trackEventsFrom(
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
			)),
			want: "duplicate tool call start: tool-call-1",
		},
		{
			name: "missing parent",
			events: combineEvents(assistant,
				trackEventsFrom(aguievents.NewToolCallStartEvent("tool-call-2", "calc")),
			),
			want: "tool call start missing parent message id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceToolStartCreatesParentMessage(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewToolCallStartEvent("tool-call-1", "search", aguievents.WithParentMessageID("assistant-ghost")),
		aguievents.NewToolCallArgsEvent("tool-call-1", "{\"q\":\"hi\"}"),
		aguievents.NewToolCallEndEvent("tool-call-1"),
		aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "done"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	if err != nil {
		t.Fatalf("Reduce err: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	first := msgs[0]
	if first.ID != "assistant-ghost" {
		t.Fatalf("unexpected id %q", first.ID)
	}
	if first.Role != "assistant" {
		t.Fatalf("unexpected role %q", first.Role)
	}
	if first.Name == nil || *first.Name != testAppName {
		t.Fatalf("unexpected assistant name %v", first.Name)
	}
	if len(first.ToolCalls) != 1 {
		t.Fatalf("expected tool call on assistant, got %d", len(first.ToolCalls))
	}
	if first.ToolCalls[0].Function.Name != "search" {
		t.Fatalf("unexpected tool call name %s", first.ToolCalls[0].Function.Name)
	}
	if first.ToolCalls[0].Function.Arguments != "{\"q\":\"hi\"}" {
		t.Fatalf("unexpected arguments %s", first.ToolCalls[0].Function.Arguments)
	}
	second := msgs[1]
	if second.Role != "tool" {
		t.Fatalf("unexpected tool role %q", second.Role)
	}
	if second.ToolCallID == nil || *second.ToolCallID != "tool-call-1" {
		t.Fatalf("unexpected tool call id %v", second.ToolCallID)
	}
	if second.Content == nil || *second.Content != "done" {
		t.Fatalf("unexpected tool result content %v", second.Content)
	}
}

func TestReduceToolArgsErrors(t *testing.T) {
	assistant := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewTextMessageEndEvent("assistant-1"),
	)
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing start",
			events: trackEventsFrom(aguievents.NewToolCallArgsEvent("ghost", "{}")),
			want:   "tool call args without start: ghost",
		},
		{
			name: "invalid phase",
			events: combineEvents(assistant, trackEventsFrom(
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
				aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
				aguievents.NewToolCallEndEvent("tool-call-1"),
				aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
			)),
			want: "tool call args invalid phase: tool-call-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceToolEndErrors(t *testing.T) {
	assistant := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewTextMessageEndEvent("assistant-1"),
	)
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing start",
			events: trackEventsFrom(aguievents.NewToolCallEndEvent("ghost")),
			want:   "tool call end without start: ghost",
		},
		{
			name: "duplicate end",
			events: combineEvents(assistant, trackEventsFrom(
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
				aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
				aguievents.NewToolCallEndEvent("tool-call-1"),
				aguievents.NewToolCallEndEvent("tool-call-1"),
			)),
			want: "duplicate tool call end: tool-call-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceToolResultErrors(t *testing.T) {
	assistant := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewTextMessageEndEvent("assistant-1"),
	)
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing identifiers",
			events: trackEventsFrom(aguievents.NewToolCallResultEvent("", "", "oops")),
			want:   "tool call result missing identifiers",
		},
		{
			name: "missing completion",
			events: combineEvents(assistant, trackEventsFrom(
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
				aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
				aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "oops"),
			)),
			want: "tool call result without completed call: tool-call-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceIgnoresEmptyPayload(t *testing.T) {
	msgs, err := Reduce(testAppName, testUserID, []session.TrackEvent{{}})
	if err != nil {
		t.Fatalf("Reduce err: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected no messages, got %d", len(msgs))
	}
}

func TestReduceInvalidPayload(t *testing.T) {
	event := session.TrackEvent{Payload: []byte("{")}
	assertReduceError(t, []session.TrackEvent{event}, "unmarshal track event payload")
}

func TestReduceIgnoresUnknownEvents(t *testing.T) {
	events := trackEventsFrom(aguievents.NewRunStartedEvent("thread", "run"))
	msgs, err := Reduce(testAppName, testUserID, events)
	if err != nil {
		t.Fatalf("Reduce err: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected no messages, got %d", len(msgs))
	}
}

func TestHandleToolEndMissingParent(t *testing.T) {
	r := new(testAppName, testUserID)
	r.toolCalls["tool-call-1"] = &toolCallState{
		messageID: "ghost-parent",
		phase:     toolAwaitingArgs,
	}
	err := r.handleToolEnd(aguievents.NewToolCallEndEvent("tool-call-1"))
	if err == nil || !strings.Contains(err.Error(), "tool call end missing parent message: ghost-parent") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTrackEvent(evt aguievents.Event) session.TrackEvent {
	payload, _ := evt.ToJSON()
	return session.TrackEvent{
		Track:     session.Track("agui"),
		Payload:   payload,
		Timestamp: time.Now(),
	}
}

func trackEventsFrom(evts ...aguievents.Event) []session.TrackEvent {
	out := make([]session.TrackEvent, 0, len(evts))
	for _, evt := range evts {
		out = append(out, newTrackEvent(evt))
	}
	return out
}

func combineEvents(chunks ...[]session.TrackEvent) []session.TrackEvent {
	total := 0
	for _, chunk := range chunks {
		total += len(chunk)
	}
	out := make([]session.TrackEvent, 0, total)
	for _, chunk := range chunks {
		out = append(out, chunk...)
	}
	return out
}

func assertReduceError(t *testing.T, events []session.TrackEvent, want string) {
	t.Helper()
	_, err := Reduce(testAppName, testUserID, events)
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("unexpected error %v", err)
	}
}
