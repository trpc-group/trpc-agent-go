//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// helper to build a streaming event with optional tool deltas
func buildStreamEvent(object string, delta model.Message, msg model.Message) *event.Event {
	return event.NewResponseEvent("inv", "node", &model.Response{
		Object:    object,
		Choices:   []model.Choice{{Index: 0, Delta: delta, Message: msg}},
		IsPartial: true,
		Done:      false,
	})
}

func TestAttachReasoningTag_AfterToolResult_Final(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	cfg := modelResponseConfig{AfterToolResult: true}
	attachReasoningTag(ev, cfg)
	if ev.Tag != event.TagReasoningFinal {
		t.Fatalf("expected tag %q, got %q", event.TagReasoningFinal, ev.Tag)
	}
}

func TestAttachReasoningTag_AfterToolResult_WithToolDelta_Tool(t *testing.T) {
	// If a chunk contains tool intent even after tools have run in this turn,
	// it should still be tagged as reasoning.tool.
	delta := model.Message{ToolCalls: []model.ToolCall{{ID: "t1"}}}
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, delta, model.Message{})
	cfg := modelResponseConfig{AfterToolResult: true}
	attachReasoningTag(ev, cfg)
	if ev.Tag != event.TagReasoningTool {
		t.Fatalf("expected tag %q, got %q", event.TagReasoningTool, ev.Tag)
	}
}

func TestAttachReasoningTag_ToolPlanSeen_Tool(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	seen := true
	cfg := modelResponseConfig{AfterToolResult: false, ToolPlanSeen: &seen}
	attachReasoningTag(ev, cfg)
	if ev.Tag != event.TagReasoningTool {
		t.Fatalf("expected tag %q, got %q", event.TagReasoningTool, ev.Tag)
	}
}

func TestAttachReasoningTag_ToolDelta_SetsSeenAndTool(t *testing.T) {
	// Delta carries a tool call -> should mark as reasoning.tool and update seen
	delta := model.Message{ToolCalls: []model.ToolCall{{ID: "t1"}}}
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, delta, model.Message{})
	seen := false
	cfg := modelResponseConfig{ToolPlanSeen: &seen}
	attachReasoningTag(ev, cfg)
	if ev.Tag != event.TagReasoningTool {
		t.Fatalf("expected tag %q, got %q", event.TagReasoningTool, ev.Tag)
	}
	if !seen {
		t.Fatalf("expected ToolPlanSeen to be true after delta")
	}
}

func TestAttachReasoningTag_Unknown(t *testing.T) {
	ev := buildStreamEvent(model.ObjectTypeChatCompletionChunk, model.Message{}, model.Message{})
	cfg := modelResponseConfig{}
	attachReasoningTag(ev, cfg)
	if ev.Tag != event.TagReasoningUnknown {
		t.Fatalf("expected tag %q, got %q", event.TagReasoningUnknown, ev.Tag)
	}
}
