//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package eventtag

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// buildStreamEvent creates a minimal streaming event for testing.
func buildStreamEvent(object string, delta model.Message, msg model.Message) *event.Event {
	return event.NewResponseEvent("inv", "node", &model.Response{
		Object:    object,
		Choices:   []model.Choice{{Index: 0, Delta: delta, Message: msg}},
		IsPartial: true,
		Done:      false,
	})
}

func TestDecideReasoningTag_NilAndNonChat(t *testing.T) {
	// nil event -> empty tag
	if got := DecideReasoningTag(nil, false, nil); got != "" {
		t.Fatalf("expected empty tag for nil event, got %q", got)
	}
	// Non chat-completion object -> empty tag
	ev := event.NewResponseEvent("inv", "node", &model.Response{Object: model.ObjectTypeToolResponse})
	if got := DecideReasoningTag(ev, false, nil); got != "" {
		t.Fatalf("expected empty tag for non-chat object, got %q", got)
	}
}

func TestDecideReasoningTag_AfterTool_Final(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	if got := DecideReasoningTag(ev, true, nil); got != event.TagReasoningFinal {
		t.Fatalf("expected %q, got %q", event.TagReasoningFinal, got)
	}
}

func TestDecideReasoningTag_AfterTool_WithToolDelta_Tool(t *testing.T) {
	// Even if afterTool is true, if the current chunk reveals tool intent,
	// it should be tagged as reasoning.tool.
	delta := model.Message{ToolCalls: []model.ToolCall{{ID: "t1"}}}
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, delta, model.Message{})
	if got := DecideReasoningTag(ev, true, nil); got != event.TagReasoningTool {
		t.Fatalf("expected %q, got %q", event.TagReasoningTool, got)
	}
}

func TestDecideReasoningTag_ToolDelta_SetsSeenAndTool(t *testing.T) {
	// Delta contains tool call -> should mark as tool and set seen=true
	delta := model.Message{ToolCalls: []model.ToolCall{{ID: "t1"}}}
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, delta, model.Message{})
	seen := false
	if got := DecideReasoningTag(ev, false, &seen); got != event.TagReasoningTool {
		t.Fatalf("expected %q, got %q", event.TagReasoningTool, got)
	}
	if !seen {
		t.Fatalf("expected toolPlanSeen to be true after tool delta")
	}
}

func TestDecideReasoningTag_ToolPlanSeen_Tool(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	seen := true
	if got := DecideReasoningTag(ev, false, &seen); got != event.TagReasoningTool {
		t.Fatalf("expected %q, got %q", event.TagReasoningTool, got)
	}
}

func TestDecideReasoningTag_Unknown_NoSideEffectOnSeen(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	seen := false
	if got := DecideReasoningTag(ev, false, &seen); got != event.TagReasoningUnknown {
		t.Fatalf("expected %q, got %q", event.TagReasoningUnknown, got)
	}
	if seen { // should remain false because no tool delta occurred
		t.Fatalf("expected toolPlanSeen to remain false when no tool delta")
	}
}
