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
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type assistantModelStep struct {
	calls []model.ToolCall
	err   error
}

type assistantTestModel struct {
	steps    []assistantModelStep
	requests []*model.Request
}

func (m *assistantTestModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	stepIndex := len(m.requests)
	m.requests = append(m.requests, request)
	if stepIndex >= len(m.steps) {
		return nil, errors.New("unexpected model call")
	}
	step := m.steps[stepIndex]
	if step.err != nil {
		return nil, step.err
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{Choices: []model.Choice{{
		Message: model.Message{ToolCalls: step.calls},
	}}}
	close(responses)
	return responses, nil
}

func (m *assistantTestModel) Info() model.Info {
	return model.Info{Name: "assistant-test-model"}
}

func TestAssistantEpisodeExtractionIsOptIn(t *testing.T) {
	disabled := NewExtractor(nil).(*memoryExtractor)
	if disabled.assistantEpisodeExtraction {
		t.Fatal("assistant episode extraction is enabled by default")
	}
	if _, ok := disabled.Metadata()[metadataKeyConversationExtraction]; ok {
		t.Fatal("default extractor reports assistant episode metadata")
	}

	enabled := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	if !enabled.assistantEpisodeExtraction {
		t.Fatal("assistant episode extraction was not enabled")
	}
	if got := enabled.Metadata()[metadataKeyConversationExtraction]; got != assistantEpisodeMetadataValue {
		t.Fatalf("assistant episode metadata = %v, want %q", got, assistantEpisodeMetadataValue)
	}
}

func TestAssistantEpisodeExtractionDisabledPreservesRequest(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}}}
	ext := NewExtractor(m)
	messages := []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("- Alpha\n- Beta"),
	}
	if _, err := ext.Extract(context.Background(), messages, nil); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
	if _, ok := m.requests[0].Tools[assistantEpisodeToolName]; ok {
		t.Fatal("default request exposes the assistant episode tool")
	}
	if !requestContainsRoleContent(m.requests[0], model.RoleAssistant, "- Alpha\n- Beta") {
		t.Fatal("default request no longer contains the assistant message")
	}
}

func TestAssistantEpisodeExtractionDisabledPreservesEmptyEnabledToolsBehavior(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}}}
	ext := NewExtractor(m).(*memoryExtractor)
	ext.SetEnabledTools(map[string]struct{}{})

	if _, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Remember nothing from this request."),
	}, nil); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got := len(m.requests[0].Tools); got != len(backgroundTools) {
		t.Fatalf("tool count = %d, want %d", got, len(backgroundTools))
	}
}

func TestAssistantEpisodeExtractionSkipsWeakCandidate(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	_, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Thanks for the help."),
		model.NewAssistantMessage("You're welcome."),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
	if requestContainsRole(m.requests[0], model.RoleAssistant) {
		t.Fatal("ordinary extraction received an assistant message")
	}
	if _, ok := m.requests[0].Tools[assistantEpisodeToolName]; ok {
		t.Fatal("ordinary extraction exposes the assistant episode tool")
	}
}

func TestAssistantEpisodeExtractionUsesConditionalSecondStage(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{calls: []model.ToolCall{makeToolCall(memory.AddToolName, []byte(`{
			"memory":"User wants options for a compact kitchen."
		}`))}},
		{calls: []model.ToolCall{assistantEpisodeToolCall(`{
			"memory":"When the user requested compact-kitchen options, the assistant recommended Alpha and Beta.",
			"topics":["compact kitchen","recommendations"]
		}`)}},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	reference := time.Date(2026, time.July, 21, 10, 30, 0, 0, time.UTC)
	operations, err := ext.Extract(
		WithReferenceDate(context.Background(), reference),
		[]model.Message{
			model.NewUserMessage("Recommend options for a compact kitchen."),
			model.NewAssistantMessage("- Alpha\n- Beta"),
		},
		nil,
	)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if requestContainsRole(m.requests[0], model.RoleAssistant) {
		t.Fatal("ordinary extraction received an assistant message")
	}
	if got := len(m.requests[1].Tools); got != 1 {
		t.Fatalf("second-stage tool count = %d, want 1", got)
	}
	if _, ok := m.requests[1].Tools[assistantEpisodeToolName]; !ok {
		t.Fatal("second stage does not expose the assistant episode tool")
	}
	if len(operations) != 2 {
		t.Fatalf("operation count = %d, want 2", len(operations))
	}
	assistantOperation := operations[1]
	if assistantOperation.MemoryKind != memory.KindEpisode {
		t.Fatalf("assistant kind = %q, want %q", assistantOperation.MemoryKind, memory.KindEpisode)
	}
	if !strings.HasPrefix(assistantOperation.Memory, assistantEpisodePrefix) {
		t.Fatalf("assistant memory = %q", assistantOperation.Memory)
	}
	if !reflect.DeepEqual(
		assistantOperation.Participants,
		[]string{"User", "Assistant"},
	) {
		t.Fatalf("participants = %#v", assistantOperation.Participants)
	}
	if assistantOperation.EventTime == nil ||
		!assistantOperation.EventTime.Equal(reference) {
		t.Fatalf("event time = %v, want %v", assistantOperation.EventTime, reference)
	}
}

func TestAssistantEpisodeExtractionFailureKeepsOrdinaryOperations(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{calls: []model.ToolCall{makeToolCall(memory.AddToolName, []byte(`{
			"memory":"User wants two recommendations."
		}`))}},
		{err: errors.New("assistant model unavailable")},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 1 || operations[0].Memory != "User wants two recommendations." {
		t.Fatalf("ordinary operations = %#v", operations)
	}
}

func TestAssistantEpisodeExtractionRespectsEnabledTools(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	ext.SetEnabledTools(map[string]struct{}{})
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("operations = %#v, want none", operations)
	}
	if len(m.requests) != 0 {
		t.Fatalf("model calls = %d, want 0", len(m.requests))
	}
}

func TestAssistantEpisodeExtractionSkipsAfterForget(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{calls: []model.ToolCall{
		makeToolCall(memory.ClearToolName, []byte(`{}`)),
	}}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Forget everything, then recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
	if len(operations) != 1 || operations[0].Type != OperationClear {
		t.Fatalf("operations = %#v", operations)
	}
}

func TestAssistantEpisodeExtractionRejectsUngroundedNumber(t *testing.T) {
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	_, err := ext.parseAssistantEpisode(
		context.Background(),
		map[string]any{argKeyMemory: "The assistant recommended 3 options."},
		"The user requested options. The assistant recommended Alpha and Beta.",
	)
	if err == nil {
		t.Fatal("ungrounded number was accepted")
	}
}

func TestAssistantEpisodeToolSchemaIsStorageCompatible(t *testing.T) {
	declaration := assistantEpisodeTool.Declaration()
	if got := len(declaration.InputSchema.Properties); got != 2 {
		t.Fatalf("property count = %d, want 2", got)
	}
	if !slices.Contains(declaration.InputSchema.Required, argKeyMemory) {
		t.Fatalf("required fields = %#v", declaration.InputSchema.Required)
	}
	for _, frameworkOwned := range []string{
		argKeyMemoryKind,
		argKeyEventTime,
		argKeyParticipants,
		argKeyLocation,
	} {
		if _, ok := declaration.InputSchema.Properties[frameworkOwned]; ok {
			t.Fatalf("schema exposes framework-owned field %q", frameworkOwned)
		}
	}
}

func TestStrongAssistantEpisodeCandidate(t *testing.T) {
	tests := []struct {
		name      string
		user      string
		assistant string
		want      bool
	}{
		{
			name:      "structured list",
			user:      "Recommend options.",
			assistant: "- Alpha\n- Beta",
			want:      true,
		},
		{
			name:      "quantity",
			user:      "How many options are available?",
			assistant: "There are 17 options.",
			want:      true,
		},
		{
			name:      "number repeated by user",
			user:      "Are there 17 options?",
			assistant: "There are 17 options.",
			want:      false,
		},
		{
			name:      "acknowledgment",
			user:      "Thanks.",
			assistant: "You're welcome.",
			want:      false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := strongAssistantEpisodeCandidate(test.user, test.assistant); got != test.want {
				t.Fatalf("candidate = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAssistantEpisodeSourceExcerptIsBoundedUTF8(t *testing.T) {
	input := "prefix-" + strings.Repeat("界", assistantEpisodeSourceMaxBytes) + "-suffix"
	excerpt := assistantEpisodeSourceExcerpt(input)
	if len(excerpt) > assistantEpisodeSourceMaxBytes {
		t.Fatalf("excerpt bytes = %d, want <= %d", len(excerpt), assistantEpisodeSourceMaxBytes)
	}
	if !utf8.ValidString(excerpt) {
		t.Fatal("excerpt is not valid UTF-8")
	}
	if !strings.HasPrefix(excerpt, "prefix-") || !strings.HasSuffix(excerpt, "-suffix") {
		t.Fatalf("excerpt does not preserve source boundaries: %q", excerpt)
	}
	if !strings.Contains(excerpt, assistantEpisodeTruncationMarker) {
		t.Fatal("excerpt does not report truncation")
	}
}

func assistantEpisodeToolCall(arguments string) model.ToolCall {
	return makeToolCall(assistantEpisodeToolName, []byte(arguments))
}

func requestContainsRole(request *model.Request, role model.Role) bool {
	for _, message := range request.Messages {
		if message.Role == role {
			return true
		}
	}
	return false
}

func requestContainsRoleContent(
	request *model.Request,
	role model.Role,
	content string,
) bool {
	for _, message := range request.Messages {
		if message.Role == role && message.Content == content {
			return true
		}
	}
	return false
}
