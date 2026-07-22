//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAssistantEpisodeExtractionIsOptIn(t *testing.T) {
	ext := NewExtractor(nil).(*memoryExtractor)
	if ext.assistantEpisodeExtraction {
		t.Fatal("assistant episode extraction is enabled by default")
	}
	if _, ok := ext.Metadata()[metadataKeyConversationExtraction]; ok {
		t.Fatal("default extractor reports assistant episode metadata")
	}
	if _, ok := ext.extractionTools()[assistantEpisodeToolName]; ok {
		t.Fatal("default extractor exposes assistant episode tool")
	}
	if prompt := ext.buildSystemPrompt(time.Time{}, nil); strings.Contains(prompt, assistantEpisodeToolName) {
		t.Fatalf("default extraction prompt exposes %q", assistantEpisodeToolName)
	}

	enabled := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	if !enabled.assistantEpisodeExtraction {
		t.Fatal("assistant episode extraction was not enabled")
	}
	if got := enabled.Metadata()[metadataKeyConversationExtraction]; got != assistantEpisodeMetadataValue {
		t.Fatalf("assistant episode metadata = %v, want %q", got, assistantEpisodeMetadataValue)
	}
}

func TestAssistantEpisodeExtractionPreservesMessages(t *testing.T) {
	toolCall := model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Name:      "lookup",
			Arguments: []byte(`{"query":"compact kitchen"}`),
		},
	}
	messages := []model.Message{
		model.NewUserMessage("Which option should I choose?"),
		{
			Role:      model.RoleAssistant,
			Content:   "Let me check.",
			ToolCalls: []model.ToolCall{toolCall},
		},
		{
			Role:    model.RoleTool,
			Content: "Alpha is available.",
		},
	}
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	got := ext.buildMessages(context.Background(), messages, nil)
	if len(got) != len(messages)+1 {
		t.Fatalf("request message count = %d, want %d", len(got), len(messages)+1)
	}
	if got[0].Role != model.RoleSystem {
		t.Fatalf("first request role = %q, want system", got[0].Role)
	}
	if !reflect.DeepEqual(got[1:], messages) {
		t.Fatalf("conversation messages changed: got %#v, want %#v", got[1:], messages)
	}
}

func TestAssistantEpisodeExtractionTool(t *testing.T) {
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	tools := ext.extractionTools()
	if tools[memory.AddToolName] != backgroundTools[memory.AddToolName] {
		t.Fatal("assistant episode extraction changed standard memory_add")
	}
	privateTool, ok := tools[assistantEpisodeToolName]
	if !ok {
		t.Fatal("assistant episode extraction did not add its private tool")
	}
	decl := privateTool.Declaration()
	if got := len(decl.InputSchema.Properties); got != 2 {
		t.Fatalf("assistant episode property count = %d, want 2", got)
	}
	if !slices.Contains(decl.InputSchema.Required, argKeyMemory) {
		t.Fatalf("assistant episode declaration does not require %q", argKeyMemory)
	}
	for _, frameworkOwned := range []string{
		argKeyMemoryKind,
		argKeyEventTime,
		argKeyParticipants,
		argKeyLocation,
	} {
		if _, ok := decl.InputSchema.Properties[frameworkOwned]; ok {
			t.Fatalf("assistant episode declaration exposes framework-owned field %q", frameworkOwned)
		}
	}
}

func TestAssistantEpisodeExtractionRequiresMemoryAdd(t *testing.T) {
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	ext.SetEnabledTools(map[string]struct{}{
		memory.UpdateToolName: {},
	})
	if _, ok := ext.extractionTools()[assistantEpisodeToolName]; ok {
		t.Fatal("assistant episode tool is enabled while memory_add is disabled")
	}
	if prompt := ext.buildSystemPrompt(time.Time{}, nil); strings.Contains(prompt, assistantEpisodeToolName) {
		t.Fatal("assistant episode prompt is enabled while memory_add is disabled")
	}
	got := ext.parseToolCallWithMessages(context.Background(), assistantEpisodeToolCall(`{
		"memory":"The assistant supplied an answer."
	}`), []model.Message{
		model.NewAssistantMessage("An answer."),
	})
	if got != nil {
		t.Fatalf("disabled assistant episode operation = %#v, want nil", got)
	}
}

func TestAssistantEpisodeExtractionCreatesEpisode(t *testing.T) {
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	messages := []model.Message{
		model.NewUserMessage("Which products suit a compact kitchen?"),
		model.NewAssistantMessage("I recommended Alpha, Beta, and Gamma."),
	}
	reference := time.Date(2026, time.July, 21, 19, 30, 0, 0,
		time.FixedZone("UTC+8", 8*60*60))
	ctx := WithReferenceDate(context.Background(), reference)
	call := assistantEpisodeToolCall(`{
		"memory":"When the user asked for products suitable for a compact kitchen, the assistant recommended Alpha, Beta, and Gamma.",
		"topics":["compact kitchen","product recommendations"],
		"memory_kind":"fact",
		"event_time":"2099-01-01T00:00:00Z",
		"participants":["Invented Person"],
		"location":"Invented Place"
	}`)
	op := ext.parseToolCallWithMessages(ctx, call, messages)
	if op == nil {
		t.Fatal("valid assistant episode was rejected")
	}
	if op.Type != OperationAdd {
		t.Fatalf("operation type = %q, want %q", op.Type, OperationAdd)
	}
	wantMemory := assistantEpisodePrefix +
		"When the user asked for products suitable for a compact kitchen, " +
		"the assistant recommended Alpha, Beta, and Gamma."
	if op.Memory != wantMemory {
		t.Fatalf("memory = %q, want %q", op.Memory, wantMemory)
	}
	if op.MemoryKind != memory.KindEpisode {
		t.Fatalf("memory kind = %q, want %q", op.MemoryKind, memory.KindEpisode)
	}
	if !reflect.DeepEqual(op.Topics, []string{"compact kitchen", "product recommendations"}) {
		t.Fatalf("topics = %#v", op.Topics)
	}
	if !reflect.DeepEqual(op.Participants, []string{"User", "Assistant"}) {
		t.Fatalf("participants = %#v", op.Participants)
	}
	if op.Location != "" {
		t.Fatalf("location = %q, want empty", op.Location)
	}
	if op.EventTime == nil || !op.EventTime.Equal(reference.UTC()) {
		t.Fatalf("event time = %v, want %v", op.EventTime, reference.UTC())
	}
}

func TestAssistantEpisodeExtractionRequiresAssistantResponse(t *testing.T) {
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	call := assistantEpisodeToolCall(`{"memory":"A reusable answer."}`)
	tests := []struct {
		name     string
		messages []model.Message
	}{
		{
			name:     "user only",
			messages: []model.Message{model.NewUserMessage("A user request.")},
		},
		{
			name:     "empty assistant",
			messages: []model.Message{model.NewAssistantMessage("  ")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ext.parseToolCallWithMessages(context.Background(), call, test.messages); got != nil {
				t.Fatalf("ungrounded assistant episode = %#v, want nil", got)
			}
		})
	}
}

func TestAssistantEpisodeExtractionAcceptsTextContentPart(t *testing.T) {
	text := "A reusable answer."
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	op := ext.parseToolCallWithMessages(context.Background(), assistantEpisodeToolCall(`{
		"memory":"The assistant supplied a reusable answer."
	}`), []model.Message{
		{
			Role: model.RoleAssistant,
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText},
				{Type: model.ContentTypeImage},
				{Type: model.ContentTypeText, Text: &text},
			},
		},
	})
	if op == nil {
		t.Fatal("assistant text content part was not recognized")
	}
}

func TestAssistantEpisodeExtractionRejectsInvalidInput(t *testing.T) {
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	messages := []model.Message{model.NewAssistantMessage("Option Alpha is suitable.")}
	if got := ext.parseToolCallWithMessages(context.Background(), assistantEpisodeToolCall(`{
		"memory":"  "
	}`), messages); got != nil {
		t.Fatalf("empty assistant episode = %#v, want nil", got)
	}
	if got := ext.parseToolCallWithMessages(context.Background(), assistantEpisodeToolCall(`{`), messages); got != nil {
		t.Fatalf("malformed assistant episode = %#v, want nil", got)
	}

	args, err := json.Marshal(map[string]any{
		argKeyMemory: strings.Repeat("x", assistantEpisodeMaxBytes+1),
	})
	if err != nil {
		t.Fatalf("marshal oversized arguments: %v", err)
	}
	call := model.ToolCall{Function: model.FunctionDefinitionParam{
		Name:      assistantEpisodeToolName,
		Arguments: args,
	}}
	if got := ext.parseToolCallWithMessages(context.Background(), call, messages); got != nil {
		t.Fatalf("oversized assistant episode = %#v, want nil", got)
	}
}

func TestAssistantEpisodeExtractionRejectsCallWhenDisabled(t *testing.T) {
	ext := NewExtractor(nil).(*memoryExtractor)
	call := assistantEpisodeToolCall(`{
		"memory":"The assistant supplied an answer."
	}`)
	if got := ext.parseToolCallWithMessages(context.Background(), call, []model.Message{
		model.NewAssistantMessage("An answer."),
	}); got != nil {
		t.Fatalf("disabled assistant episode operation = %#v, want nil", got)
	}
}

func TestAssistantEpisodeExtractionEndToEnd(t *testing.T) {
	call := assistantEpisodeToolCall(`{
		"memory":"The assistant recommended Alpha for the compact kitchen."
	}`)
	mock := newMockModelWithToolCalls([]model.ToolCall{call})
	ext := NewExtractor(mock, WithAssistantEpisodeExtraction())
	ops, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("What suits a compact kitchen?"),
		model.NewAssistantMessage("I recommend Alpha."),
	}, nil)
	if err != nil {
		t.Fatalf("extract assistant episode: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("operation count = %d, want 1", len(ops))
	}
	if ops[0].MemoryKind != memory.KindEpisode {
		t.Fatalf("memory kind = %q, want %q", ops[0].MemoryKind, memory.KindEpisode)
	}
	if mock.lastRequest == nil {
		t.Fatal("model request was not captured")
	}
	if _, ok := mock.lastRequest.Tools[assistantEpisodeToolName]; !ok {
		t.Fatal("model request does not contain assistant episode tool")
	}
	if !strings.Contains(mock.lastRequest.Messages[0].Content, assistantEpisodeToolName) {
		t.Fatal("system prompt does not contain assistant episode policy")
	}
	if got := mock.lastRequest.Messages[1:3]; !reflect.DeepEqual(got, []model.Message{
		model.NewUserMessage("What suits a compact kitchen?"),
		model.NewAssistantMessage("I recommend Alpha."),
	}) {
		t.Fatalf("model conversation changed: %#v", got)
	}
}

func assistantEpisodeToolCall(arguments string) model.ToolCall {
	return model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Name:      assistantEpisodeToolName,
			Arguments: []byte(arguments),
		},
	}
}
