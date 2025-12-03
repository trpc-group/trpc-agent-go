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
	"reflect"
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
		newTrackEvent(aguievents.NewTextMessageContentEvent("user-1", "hello ")),
		newTrackEvent(aguievents.NewTextMessageContentEvent("user-1", "world")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("user-1")),
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewTextMessageContentEvent("assistant-1", "thinking")),
		newTrackEvent(aguievents.NewTextMessageContentEvent("assistant-1", "...done")),
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

	// User message assertions.
	user := msgs[0]
	if user.ID != "user-1" {
		t.Fatalf("user id mismatch, got %s", user.ID)
	}
	if user.Role != "user" {
		t.Fatalf("user role mismatch: %s", user.Role)
	}
	if user.Name == nil || *user.Name != testUserID {
		t.Fatalf("expected user name %q, got %v", testUserID, user.Name)
	}
	if user.Content == nil || *user.Content != "hello world" {
		t.Fatalf("unexpected user content %v", user.Content)
	}
	if len(user.ToolCalls) != 0 {
		t.Fatalf("user message should not have tool calls")
	}

	// Assistant message assertions.
	assistant := msgs[1]
	if assistant.ID != "assistant-1" {
		t.Fatalf("assistant id mismatch: %s", assistant.ID)
	}
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role mismatch: %s", assistant.Role)
	}
	if assistant.Name == nil || *assistant.Name != testAppName {
		t.Fatalf("expected assistant name %q, got %v", testAppName, assistant.Name)
	}
	if assistant.Content == nil || *assistant.Content != "thinking...done" {
		t.Fatalf("unexpected assistant content %v", assistant.Content)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}
	call := assistant.ToolCalls[0]
	if call.ID != "tool-call-1" || call.Type != "function" {
		t.Fatalf("unexpected tool call meta: %+v", call)
	}
	if call.Function.Name != "calc" {
		t.Fatalf("unexpected tool name %s", call.Function.Name)
	}
	if call.Function.Arguments != "{\"a\":1}" {
		t.Fatalf("unexpected tool args %s", call.Function.Arguments)
	}

	// Tool result assertions.
	tool := msgs[2]
	if tool.ID != "tool-msg-1" {
		t.Fatalf("tool result id mismatch: %s", tool.ID)
	}
	if tool.Role != "tool" {
		t.Fatalf("tool result role mismatch: %s", tool.Role)
	}
	if tool.Content == nil || *tool.Content != "42" {
		t.Fatalf("unexpected tool result content %v", tool.Content)
	}
	if tool.ToolCallID == nil || *tool.ToolCallID != "tool-call-1" {
		t.Fatalf("unexpected tool call reference %v", tool.ToolCallID)
	}
}

func TestReduceReturnsMessagesOnReduceError(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
		aguievents.NewTextMessageContentEvent("user-1", "hello"),
		aguievents.NewTextMessageEndEvent("user-1"),
		aguievents.NewTextMessageContentEvent("user-1", "!"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	if err == nil || !strings.Contains(err.Error(), "reduce: text message content after end: user-1") {
		t.Fatalf("unexpected error %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content == nil || *msgs[0].Content != "hello" {
		t.Fatalf("unexpected content %v", msgs[0].Content)
	}
}

func TestReduceReturnsMessagesOnFinalizeError(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
		aguievents.NewTextMessageContentEvent("user-1", "hello"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	if err == nil || !strings.Contains(err.Error(), "finalize: text message user-1 not closed") {
		t.Fatalf("unexpected error %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != nil {
		t.Fatalf("expected nil content, got %v", msgs[0].Content)
	}
}

func TestHandleTextChunkSuccess(t *testing.T) {
	tests := []struct {
		name        string
		chunk       *aguievents.TextMessageChunkEvent
		wantRole    string
		wantName    string
		wantContent string
	}{
		{
			name:        "assistant default role empty delta",
			chunk:       aguievents.NewTextMessageChunkEvent(stringPtr("msg-1"), stringPtr("assistant"), stringPtr("")),
			wantRole:    "assistant",
			wantName:    testAppName,
			wantContent: "",
		},
		{
			name:        "user role with delta",
			chunk:       aguievents.NewTextMessageChunkEvent(stringPtr("msg-2"), stringPtr("user"), stringPtr("hi")),
			wantRole:    "user",
			wantName:    testUserID,
			wantContent: "hi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := new(testAppName, testUserID)
			if err := r.handleTextChunk(tt.chunk); err != nil {
				t.Fatalf("handleTextChunk err: %v", err)
			}
			if err := r.finalize(); err != nil {
				t.Fatalf("finalize err: %v", err)
			}
			if len(r.messages) != 1 {
				t.Fatalf("expected 1 message, got %d", len(r.messages))
			}
			msg := r.messages[0]
			if msg.Role != tt.wantRole {
				t.Fatalf("unexpected role %q", msg.Role)
			}
			if msg.Name == nil || *msg.Name != tt.wantName {
				t.Fatalf("unexpected name %v", msg.Name)
			}
			if msg.Content == nil || *msg.Content != tt.wantContent {
				t.Fatalf("unexpected content %v", msg.Content)
			}
			state, ok := r.texts[*tt.chunk.MessageID]
			if !ok {
				t.Fatalf("expected text state for %s", *tt.chunk.MessageID)
			}
			if state.phase != textEnded {
				t.Fatalf("unexpected phase %v", state.phase)
			}
			if got := state.content.String(); got != tt.wantContent {
				t.Fatalf("unexpected builder content %q", got)
			}
			if state.index != 0 {
				t.Fatalf("unexpected state index %d", state.index)
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestHandleTextChunkErrors(t *testing.T) {
	t.Run("missing id", func(t *testing.T) {
		chunk := aguievents.NewTextMessageChunkEvent(stringPtr(""), stringPtr("assistant"), stringPtr(""))
		r := new(testAppName, testUserID)
		if err := r.handleTextChunk(chunk); err == nil || !strings.Contains(err.Error(), "text message chunk missing id") {
			t.Fatalf("unexpected error %v", err)
		}
	})
	t.Run("duplicate id", func(t *testing.T) {
		chunk := aguievents.NewTextMessageChunkEvent(stringPtr("msg-1"), stringPtr("assistant"), stringPtr(""))
		r := new(testAppName, testUserID)
		if err := r.handleTextChunk(chunk); err != nil {
			t.Fatalf("handleTextChunk err: %v", err)
		}
		if err := r.handleTextChunk(chunk); err == nil || !strings.Contains(err.Error(), "duplicate text message chunk: msg-1") {
			t.Fatalf("unexpected error %v", err)
		}
	})
	t.Run("unsupported role", func(t *testing.T) {
		chunk := aguievents.NewTextMessageChunkEvent(stringPtr("msg-3"), stringPtr("tool"), stringPtr(""))
		r := new(testAppName, testUserID)
		if err := r.handleTextChunk(chunk); err == nil || !strings.Contains(err.Error(), "unsupported role: tool") {
			t.Fatalf("unexpected error %v", err)
		}
	})
	t.Run("empty string id pointer", func(t *testing.T) {
		chunk := aguievents.NewTextMessageChunkEvent(stringPtr(""), stringPtr("assistant"), stringPtr(""))
		empty := ""
		chunk.MessageID = &empty
		r := new(testAppName, testUserID)
		if err := r.handleTextChunk(chunk); err == nil || !strings.Contains(err.Error(), "text message chunk missing id") {
			t.Fatalf("unexpected error %v", err)
		}
	})
}

func TestReduceEventDispatchesChunk(t *testing.T) {
	r := new(testAppName, testUserID)
	chunk := aguievents.NewTextMessageChunkEvent(stringPtr("msg-1"), stringPtr("assistant"), stringPtr("hi"))
	if err := r.reduceEvent(chunk); err != nil {
		t.Fatalf("reduceEvent err: %v", err)
	}
	if err := r.finalize(); err != nil {
		t.Fatalf("finalize err: %v", err)
	}
	if len(r.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(r.messages))
	}
	if r.messages[0].Content == nil || *r.messages[0].Content != "hi" {
		t.Fatalf("unexpected content %v", r.messages[0].Content)
	}
}

func TestAssistantOnlyToolCall(t *testing.T) {
	events := []session.TrackEvent{
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewToolCallStartEvent("tool-call-1", "search", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("tool-call-1", "{}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("tool-call-1")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("assistant-1")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "reply")),
	}

	msgs, err := Reduce(testAppName, testUserID, events)
	if err != nil {
		t.Fatalf("Reduce err: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	assistant := msgs[0]
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}
	call := assistant.ToolCalls[0]
	if call.ID != "tool-call-1" || call.Function.Name != "search" {
		t.Fatalf("unexpected tool call %+v", call)
	}
	if call.Function.Arguments != "{}" {
		t.Fatalf("unexpected tool args: %s", call.Function.Arguments)
	}
	tool := msgs[1]
	if tool.ToolCallID == nil || *tool.ToolCallID != "tool-call-1" {
		t.Fatalf("unexpected tool call id %v", tool.ToolCallID)
	}
	if tool.Content == nil || *tool.Content != "reply" {
		t.Fatalf("unexpected tool content %v", tool.Content)
	}
}

func TestAssistantInterleavesTextAfterToolEnd(t *testing.T) {
	events := []session.TrackEvent{
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("tool-call-1", "{\"x\":1}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("tool-call-1")),
		newTrackEvent(aguievents.NewTextMessageContentEvent("assistant-1", "waiting")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("assistant-1")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "done")),
	}

	msgs, err := Reduce(testAppName, testUserID, events)
	if err != nil {
		t.Fatalf("Reduce err: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	assistant := msgs[0]
	if assistant.Content == nil || *assistant.Content != "waiting" {
		t.Fatalf("unexpected assistant content %v", assistant.Content)
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Function.Arguments != "{\"x\":1}" {
		t.Fatalf("unexpected tool calls %+v", assistant.ToolCalls)
	}
	tool := msgs[1]
	if tool.Content == nil || *tool.Content != "done" {
		t.Fatalf("unexpected tool content %v", tool.Content)
	}
	if tool.ToolCallID == nil || *tool.ToolCallID != "tool-call-1" {
		t.Fatalf("unexpected tool call id %v", tool.ToolCallID)
	}
}

func TestAssistantMultipleToolCalls(t *testing.T) {
	events := []session.TrackEvent{
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewToolCallStartEvent("call-1", "alpha", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("call-1", "{\"step\":1}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("call-1")),
		newTrackEvent(aguievents.NewToolCallStartEvent("call-2", "beta", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("call-2", "{\"step\":2}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("call-2")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("assistant-1")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-1", "call-1", "first-result")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-2", "call-2", "second-result")),
	}

	msgs, err := Reduce(testAppName, testUserID, events)
	if err != nil {
		t.Fatalf("Reduce err: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	assistant := msgs[0]
	if len(assistant.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(assistant.ToolCalls))
	}
	if assistant.ToolCalls[0].ID != "call-1" || assistant.ToolCalls[0].Function.Name != "alpha" {
		t.Fatalf("unexpected first tool call: %+v", assistant.ToolCalls[0])
	}
	if assistant.ToolCalls[0].Function.Arguments != "{\"step\":1}" {
		t.Fatalf("unexpected call-1 args: %s", assistant.ToolCalls[0].Function.Arguments)
	}
	if assistant.ToolCalls[1].ID != "call-2" || assistant.ToolCalls[1].Function.Name != "beta" {
		t.Fatalf("unexpected second tool call: %+v", assistant.ToolCalls[1])
	}
	if assistant.ToolCalls[1].Function.Arguments != "{\"step\":2}" {
		t.Fatalf("unexpected call-2 args: %s", assistant.ToolCalls[1].Function.Arguments)
	}

	first := msgs[1]
	if first.ToolCallID == nil || *first.ToolCallID != "call-1" {
		t.Fatalf("unexpected first result id %v", first.ToolCallID)
	}
	if first.Content == nil || *first.Content != "first-result" {
		t.Fatalf("unexpected first result content %v", first.Content)
	}
	second := msgs[2]
	if second.ToolCallID == nil || *second.ToolCallID != "call-2" {
		t.Fatalf("unexpected second result id %v", second.ToolCallID)
	}
	if second.Content == nil || *second.Content != "second-result" {
		t.Fatalf("unexpected second result content %v", second.Content)
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

func TestHandleActivityAllCases(t *testing.T) {
	stepStarted := aguievents.NewStepStartedEvent("prep")
	stepFinished := aguievents.NewStepFinishedEvent("cleanup")
	stateSnapshot := aguievents.NewStateSnapshotEvent(map[string]any{"status": "ok"})
	stateDeltaOps := []aguievents.JSONPatchOperation{{Op: "add", Path: "/count", Value: 1}}
	stateDelta := aguievents.NewStateDeltaEvent(stateDeltaOps)
	messageSnapshotEvent := aguievents.NewMessagesSnapshotEvent([]aguievents.Message{
		{ID: "msg-1", Role: "assistant"},
	})
	activitySnapshot := aguievents.NewActivitySnapshotEvent("activity-1", "PLAN", map[string]any{"status": "draft"}).WithReplace(false)
	activityDeltaOps := []aguievents.JSONPatchOperation{{Op: "replace", Path: "/status", Value: "done"}}
	activityDelta := aguievents.NewActivityDeltaEvent("activity-2", "PLAN", activityDeltaOps)
	customEvent := aguievents.NewCustomEvent("custom-event", aguievents.WithValue(map[string]any{"k": "v"}))
	rawEvent := aguievents.NewRawEvent(map[string]any{"raw": true}, aguievents.WithSource("unit-test"))
	runStarted := aguievents.NewRunStartedEvent("thread-1", "run-1")

	tests := []struct {
		name        string
		event       aguievents.Event
		wantCount   int
		wantID      string
		wantType    string
		wantContent map[string]any
	}{
		{
			name:        "step started",
			event:       stepStarted,
			wantCount:   1,
			wantID:      stepStarted.ID(),
			wantType:    string(stepStarted.Type()),
			wantContent: map[string]any{"stepName": stepStarted.StepName},
		},
		{
			name:        "step finished",
			event:       stepFinished,
			wantCount:   1,
			wantID:      stepFinished.ID(),
			wantType:    string(stepFinished.Type()),
			wantContent: map[string]any{"stepName": stepFinished.StepName},
		},
		{
			name:        "state snapshot",
			event:       stateSnapshot,
			wantCount:   1,
			wantID:      stateSnapshot.ID(),
			wantType:    string(stateSnapshot.Type()),
			wantContent: map[string]any{"snapshot": stateSnapshot.Snapshot},
		},
		{
			name:        "state delta",
			event:       stateDelta,
			wantCount:   1,
			wantID:      stateDelta.ID(),
			wantType:    string(stateDelta.Type()),
			wantContent: map[string]any{"delta": stateDelta.Delta},
		},
		{
			name:        "messages snapshot",
			event:       messageSnapshotEvent,
			wantCount:   1,
			wantID:      messageSnapshotEvent.ID(),
			wantType:    string(messageSnapshotEvent.Type()),
			wantContent: map[string]any{"messages": messageSnapshotEvent.Messages},
		},
		{
			name:      "activity snapshot",
			event:     activitySnapshot,
			wantCount: 1,
			wantID:    activitySnapshot.ID(),
			wantType:  string(activitySnapshot.Type()),
			wantContent: map[string]any{
				"messageId":    activitySnapshot.MessageID,
				"activityType": activitySnapshot.ActivityType,
				"content":      activitySnapshot.Content,
				"replace":      activitySnapshot.Replace,
			},
		},
		{
			name:      "activity delta",
			event:     activityDelta,
			wantCount: 1,
			wantID:    activityDelta.ID(),
			wantType:  string(activityDelta.Type()),
			wantContent: map[string]any{
				"messageId":    activityDelta.MessageID,
				"activityType": activityDelta.ActivityType,
				"patch":        activityDelta.Patch,
			},
		},
		{
			name:      "custom event",
			event:     customEvent,
			wantCount: 1,
			wantID:    customEvent.ID(),
			wantType:  string(customEvent.Type()),
			wantContent: map[string]any{
				"name":  customEvent.Name,
				"value": customEvent.Value,
			},
		},
		{
			name:      "raw event",
			event:     rawEvent,
			wantCount: 1,
			wantID:    rawEvent.ID(),
			wantType:  string(rawEvent.Type()),
			wantContent: map[string]any{
				"source": rawEvent.Source,
				"event":  rawEvent.Event,
			},
		},
		{
			name:        "default passthrough",
			event:       runStarted,
			wantCount:   0,
			wantID:      runStarted.GetBaseEvent().ID(),
			wantType:    string(runStarted.Type()),
			wantContent: map[string]any{"content": runStarted},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := new(testAppName, testUserID)
			if err := r.handleActivity(tt.event); err != nil {
				t.Fatalf("handleActivity err: %v", err)
			}
			if len(r.messages) != tt.wantCount {
				t.Fatalf("expected %d activity messages, got %d", tt.wantCount, len(r.messages))
			}
			if len(r.messages) == 0 {
				return
			}
			msg := r.messages[0]
			if msg.Role != "activity" {
				t.Fatalf("unexpected role %q", msg.Role)
			}
			if msg.ID != tt.wantID {
				t.Fatalf("unexpected id %q", msg.ID)
			}
			if msg.ActivityType != tt.wantType {
				t.Fatalf("unexpected activity type %q", msg.ActivityType)
			}
			if msg.Content != nil {
				t.Fatalf("expected nil text content, got %v", msg.Content)
			}
			if !reflect.DeepEqual(msg.ActivityContent, tt.wantContent) {
				t.Fatalf("unexpected activity content %+v", msg.ActivityContent)
			}
		})
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
