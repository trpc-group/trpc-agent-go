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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// buildStreamEvent creates a minimal streaming event for testing.
func buildStreamEvent(object string, delta model.Message, msg model.Message) *Event {
	return NewResponseEvent("inv", "node", &model.Response{
		Object:    object,
		Choices:   []model.Choice{{Index: 0, Delta: delta, Message: msg}},
		IsPartial: true,
		Done:      false,
	})
}

func TestAppendTagString(t *testing.T) {
	// Empty existing returns tag directly
	if got := AppendTagString("", "a"); got != "a" {
		t.Fatalf("expected 'a', got %q", got)
	}
	// Empty tag returns existing unchanged
	if got := AppendTagString("x", ""); got != "x" {
		t.Fatalf("expected 'x', got %q", got)
	}
	// Append with delimiter
	want := "x" + TagDelimiter + "y"
	if got := AppendTagString("x", "y"); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	// Avoid duplicate
	if got := AppendTagString("x", "x"); got != "x" {
		t.Fatalf("expected 'x', got %q", got)
	}
}

func TestAddTag(t *testing.T) {
	// nil event should be no-op (no panic)
	AddTag(nil, "a")

	e := &Event{}
	AddTag(e, "a")
	if e.Tag != "a" {
		t.Fatalf("expected 'a', got %q", e.Tag)
	}
	// duplicate should not be appended
	AddTag(e, "a")
	if e.Tag != "a" {
		t.Fatalf("expected 'a' after duplicate, got %q", e.Tag)
	}
	// different tag should be appended with delimiter
	AddTag(e, "b")
	want := "a" + TagDelimiter + "b"
	if e.Tag != want {
		t.Fatalf("expected %q, got %q", want, e.Tag)
	}
}

func TestDecideReasoningTag_NilAndNonChat(t *testing.T) {
	// nil event -> empty tag
	if got := DecideReasoningTag(nil, false, nil); got != "" {
		t.Fatalf("expected empty tag for nil event, got %q", got)
	}
	// Non chat-completion object -> empty tag
	ev := NewResponseEvent("inv", "node", &model.Response{Object: model.ObjectTypeToolResponse})
	if got := DecideReasoningTag(ev, false, nil); got != "" {
		t.Fatalf("expected empty tag for non-chat object, got %q", got)
	}
}

func TestDecideReasoningTag_AfterTool_Final(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	if got := DecideReasoningTag(ev, true, nil); got != TagReasoningFinal {
		t.Fatalf("expected %q, got %q", TagReasoningFinal, got)
	}
}

func TestDecideReasoningTag_ToolDelta_SetsSeenAndTool(t *testing.T) {
	// Delta contains tool call -> should mark as tool and set seen=true
	delta := model.Message{ToolCalls: []model.ToolCall{{ID: "t1"}}}
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, delta, model.Message{})
	seen := false
	if got := DecideReasoningTag(ev, false, &seen); got != TagReasoningTool {
		t.Fatalf("expected %q, got %q", TagReasoningTool, got)
	}
	if !seen {
		t.Fatalf("expected toolPlanSeen to be true after tool delta")
	}
}

func TestDecideReasoningTag_ToolPlanSeen_Tool(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	seen := true
	if got := DecideReasoningTag(ev, false, &seen); got != TagReasoningTool {
		t.Fatalf("expected %q, got %q", TagReasoningTool, got)
	}
}

func TestDecideReasoningTag_Unknown_NoSideEffectOnSeen(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	seen := false
	if got := DecideReasoningTag(ev, false, &seen); got != TagReasoningUnknown {
		t.Fatalf("expected %q, got %q", TagReasoningUnknown, got)
	}
	if seen { // should remain false because no tool delta occurred
		t.Fatalf("expected toolPlanSeen to remain false when no tool delta")
	}
}
