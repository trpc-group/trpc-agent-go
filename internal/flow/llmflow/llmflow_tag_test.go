//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestHasToolSinceLastUser(t *testing.T) {
	msgs := []model.Message{
		model.NewUserMessage("hi"),
		{Role: model.RoleAssistant, Content: "ok"},
		{Role: model.RoleTool, Content: "toolout"},
	}
	if !hasToolSinceLastUser(msgs) {
		t.Fatalf("expected true when tool appears after last user")
	}
	msgs2 := []model.Message{
		model.NewUserMessage("hi"),
		{Role: model.RoleAssistant, Content: "ok"},
	}
	if hasToolSinceLastUser(msgs2) {
		t.Fatalf("expected false when no tool after last user")
	}
}

func TestAttachReasoningTagLLM(t *testing.T) {
	// Build a streaming event
	ev := event.NewResponseEvent("inv", "agent", &model.Response{
		Object:    model.ObjectTypeChatCompletionChunk,
		Choices:   []model.Choice{{Index: 0, Delta: model.Message{}}},
		IsPartial: true,
		Done:      false,
	})
	// After tool => final
	attachReasoningTagLLM(ev, true, nil)
	if ev.Tag != event.TagReasoningFinal {
		t.Fatalf("expected %q, got %q", event.TagReasoningFinal, ev.Tag)
	}

	// Tool plan seen => tool (appended, not overwritten)
	seen := true
	attachReasoningTagLLM(ev, false, &seen)
	if !strings.Contains(ev.Tag, event.TagReasoningTool) {
		t.Fatalf("expected to contain %q, got %q", event.TagReasoningTool, ev.Tag)
	}

	// Unknown by default (appended)
	seen = false
	attachReasoningTagLLM(ev, false, &seen)
	if !strings.Contains(ev.Tag, event.TagReasoningUnknown) {
		t.Fatalf("expected to contain %q, got %q", event.TagReasoningUnknown, ev.Tag)
	}
}
