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
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/assistantmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type assistantModelStep struct {
	calls         []model.ToolCall
	err           error
	before        func()
	waitForCancel bool
}

type assistantTestModel struct {
	steps    []assistantModelStep
	requests []*model.Request
}

func (m *assistantTestModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	stepIndex := len(m.requests)
	m.requests = append(m.requests, request)
	if stepIndex >= len(m.steps) {
		return nil, errors.New("unexpected model call")
	}
	step := m.steps[stepIndex]
	if step.before != nil {
		step.before()
	}
	if step.waitForCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
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
	if assistantmemory.Enabled(disabled) {
		t.Fatal("default extractor reports assistant episode capability")
	}

	enabled := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	if !enabled.assistantEpisodeExtraction {
		t.Fatal("assistant episode extraction was not enabled")
	}
	if !assistantmemory.Enabled(enabled) {
		t.Fatal("enabled extractor does not report assistant episode capability")
	}
	if !reflect.DeepEqual(disabled.Metadata(), enabled.Metadata()) {
		t.Fatal("assistant option changed descriptive extractor metadata")
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

func TestAssistantEpisodeExtractionFallsBackWhenWorkerCapabilityIsHidden(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	ctx := assistantmemory.WithWorkerConfiguration(context.Background(), false)

	if _, err := ext.Extract(ctx, []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("- Alpha\n- Beta"),
	}, nil); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
	if !requestContainsRoleContent(m.requests[0], model.RoleAssistant, "- Alpha\n- Beta") {
		t.Fatal("worker-disabled fallback omitted the ordinary assistant message")
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

func TestAssistantEpisodeOrdinaryStagePreservesContentWithoutDestructiveTools(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	ext.SetEnabledTools(map[string]struct{}{memory.AddToolName: {}})
	const content = "Recommend two options."
	if _, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage(content),
		model.NewAssistantMessage("No structured answer."),
	}, nil); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !requestContainsRoleContent(m.requests[0], model.RoleUser, content) {
		t.Fatalf("ordinary request changed user content: %#v", m.requests[0].Messages)
	}
}

func TestAssistantEpisodeOrdinaryStagePreservesContentWithDestructiveTools(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	const content = "Remember that I like coffee."
	if _, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage(content),
		model.NewAssistantMessage("Understood."),
	}, nil); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !requestContainsRoleContent(m.requests[0], model.RoleUser, content) {
		t.Fatalf("ordinary request changed user content: %#v", m.requests[0].Messages)
	}
	for _, message := range m.requests[0].Messages {
		if strings.Contains(message.Content, "[source_user_index=") {
			t.Fatalf("ordinary request leaked source label: %q", message.Content)
		}
	}
}

func TestAssistantEpisodeExtractionIncludesStoredEpisodesInOrdinaryStage(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	existing := []*memory.Entry{
		{
			ID:     "ordinary",
			Memory: &memory.Memory{Memory: "User likes compact kitchens."},
		},
		{
			ID: "assistant",
			Memory: &memory.Memory{Memory: assistantEpisodePrefix +
				"The assistant recommended Alpha and Beta."},
		},
	}

	_, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("I still like compact kitchens."),
	}, existing)

	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !requestContainsRoleSubstring(
		m.requests[0], model.RoleSystem, "User likes compact kitchens.",
	) {
		t.Fatal("ordinary stage omitted an ordinary existing memory")
	}
	if !requestContainsRoleSubstring(
		m.requests[0], model.RoleSystem, "The assistant recommended Alpha and Beta.",
	) {
		t.Fatal("ordinary stage omitted a stored assistant episode")
	}
}

func TestAssistantEpisodeExtractionDoesNotApplyEarlierForgetToLaterExchange(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}, {}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())

	_, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Please clear all my memories."),
		model.NewAssistantMessage("Understood."),
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)

	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if !requestContainsRoleSubstring(
		m.requests[1], model.RoleUser, "Recommend two options.",
	) {
		t.Fatal("assistant request omitted the later exchange")
	}
	if requestContainsRoleSubstring(
		m.requests[1], model.RoleUser, "clear all my memories",
	) {
		t.Fatal("assistant request included the earlier clear request")
	}
}

func TestAssistantEpisodeExtractionDoesNotInterpretForgetWording(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}, {}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())

	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		model.NewUserMessage("Please clear all my memories."),
		model.NewAssistantMessage("Understood."),
	}, nil)

	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if len(operations) != 0 {
		t.Fatalf("operations = %#v, want none", operations)
	}
}

func TestAssistantEpisodeExtractionUsesConditionalSecondStage(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{calls: []model.ToolCall{makeToolCall(memory.AddToolName, []byte(`{
			"memory":"User wants options for a compact kitchen."
		}`))}},
		{calls: []model.ToolCall{assistantEpisodeToolCall(`{
			"pair_id":"pair-1",
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
	if got := m.requests[1].Messages[0].Content; got != assistantEpisodeSystemPrompt {
		t.Fatalf("default second-stage prompt changed:\n%s", got)
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
		t.Fatalf("operations = %#v, want ordinary operation", operations)
	}
}

func TestAssistantEpisodeExtractionUsesCustomPromptInBothStages(t *testing.T) {
	const customPolicy = "Never retain medical information from any conversation."
	const customPrompt = customPolicy + " Current date: {current_date}."
	reference := time.Date(2026, time.August, 5, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name      string
		configure func(*memoryExtractor)
	}{
		{
			name: "constructor option",
			configure: func(e *memoryExtractor) {
				WithPrompt(customPrompt)(e)
			},
		},
		{
			name: "setter",
			configure: func(e *memoryExtractor) {
				e.SetPrompt(customPrompt)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := &assistantTestModel{steps: []assistantModelStep{{}, {}}}
			ext := NewExtractor(m, WithAssistantEpisodeExtraction()).(*memoryExtractor)
			test.configure(ext)
			_, err := ext.Extract(WithReferenceDate(context.Background(), reference), []model.Message{
				model.NewUserMessage("Recommend two options."),
				model.NewAssistantMessage("1. Alpha\n2. Beta"),
			}, nil)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if len(m.requests) != 2 {
				t.Fatalf("model calls = %d, want 2", len(m.requests))
			}
			for requestIndex, request := range m.requests {
				if !requestContainsRoleSubstring(request, model.RoleSystem, customPolicy) ||
					!requestContainsRoleSubstring(request, model.RoleSystem, "Current date: 2026-08-05.") {
					t.Fatalf("request %d does not contain rendered custom prompt", requestIndex)
				}
				if requestContainsRoleSubstring(request, model.RoleSystem, "{current_date}") {
					t.Fatalf("request %d contains an unrendered current_date variable", requestIndex)
				}
			}
		})
	}
}

func TestAssistantEpisodeExtractionProcessesAllEligiblePairs(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{},
		{calls: []model.ToolCall{
			assistantEpisodeToolCall(`{
				"pair_id":"pair-1",
				"memory":"The assistant recommended Alpha and Beta."
			}`),
			assistantEpisodeToolCall(`{
				"pair_id":"pair-2",
				"memory":"The assistant recommended Gamma and Delta."
			}`),
		}},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("Let me check."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		model.NewUserMessage("Suggest two alternatives."),
		model.NewAssistantMessage("1. Gamma\n2. Delta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if !requestContainsRoleSubstring(
		m.requests[1],
		model.RoleSystem,
		"at most once for each labeled pair",
	) {
		t.Fatal("assistant stage did not use the multi-pair prompt")
	}
	if !requestContainsRoleSubstring(m.requests[1], model.RoleAssistant, "1. Alpha\n2. Beta") ||
		requestContainsRoleSubstring(m.requests[1], model.RoleAssistant, "Let me check.") {
		t.Fatal("batch assistant stage did not use the final reply for its first user turn")
	}
	if !requestContainsRoleSubstring(m.requests[1], model.RoleAssistant, "1. Gamma\n2. Delta") {
		t.Fatal("batch assistant stage is not associated with the second user turn")
	}
	if len(operations) != 2 {
		t.Fatalf("operations = %#v, want 2 assistant episodes", operations)
	}
	for _, expected := range []string{"Alpha and Beta", "Gamma and Delta"} {
		if !slices.ContainsFunc(operations, func(operation *Operation) bool {
			return strings.Contains(operation.Memory, expected)
		}) {
			t.Fatalf("operations do not contain %q: %#v", expected, operations)
		}
	}
}

func TestAssistantEpisodeExtractionKeepsInBudgetEligiblePairs(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}, {}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	const pairCount = 17
	messages := make([]model.Message, 0, pairCount*2)
	for i := 0; i < pairCount; i++ {
		messages = append(
			messages,
			model.NewUserMessage(fmt.Sprintf("Recommend two options for case %d.", i)),
			model.NewAssistantMessage(fmt.Sprintf("1. Alpha-%d\n2. Beta-%d", i, i)),
		)
	}

	operations, err := ext.Extract(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("operations = %#v, want none", operations)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if !requestContainsRoleSubstring(m.requests[1], model.RoleUser, "pair-17 user request") {
		t.Fatal("assistant request dropped an eligible pair")
	}
	declaration := m.requests[1].Tools[assistantEpisodeToolName].Declaration()
	pairIDs := declaration.InputSchema.Properties[assistantEpisodePairIDKey].Enum
	if len(pairIDs) != pairCount || pairIDs[pairCount-1] != "pair-17" {
		t.Fatalf("pair id enum = %#v, want all %d pairs", pairIDs, pairCount)
	}
}

func TestAssistantEpisodeExtractionBoundsCandidateWork(t *testing.T) {
	tests := []struct {
		name      string
		pairCount int
		userText  func(int) string
		answer    func(int) string
		wantPairs int
	}{
		{
			name:      "pair count",
			pairCount: assistantEpisodeRequestMaxPairs + 1,
			userText: func(i int) string {
				return fmt.Sprintf("Recommend two options for case %d.", i)
			},
			answer: func(i int) string {
				return fmt.Sprintf("1. Alpha-%d\n2. Beta-%d", i, i)
			},
			wantPairs: assistantEpisodeRequestMaxPairs,
		},
		{
			name:      "source bytes",
			pairCount: 5,
			userText: func(i int) string {
				return fmt.Sprintf("Recommend two options for case %d.\n", i) +
					strings.Repeat("u", assistantEpisodeSourceMaxBytes)
			},
			answer: func(i int) string {
				return fmt.Sprintf("1. Alpha-%d\n2. Beta-%d\n", i, i) +
					strings.Repeat("a", assistantEpisodeSourceMaxBytes)
			},
			wantPairs: assistantEpisodeRequestMaxSourceBytes /
				(2 * assistantEpisodeSourceMaxBytes),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := &assistantTestModel{steps: []assistantModelStep{{}, {}}}
			ext := NewExtractor(m, WithAssistantEpisodeExtraction())
			messages := make([]model.Message, 0, test.pairCount*2)
			for i := 0; i < test.pairCount; i++ {
				messages = append(
					messages,
					model.NewUserMessage(test.userText(i)),
					model.NewAssistantMessage(test.answer(i)),
				)
			}

			operations, err := ext.Extract(context.Background(), messages, nil)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if len(operations) != 0 {
				t.Fatalf("operations = %#v, want none", operations)
			}
			if len(m.requests) != 2 {
				t.Fatalf("model calls = %d, want 2", len(m.requests))
			}
			declaration := m.requests[1].Tools[assistantEpisodeToolName].Declaration()
			pairIDs := declaration.InputSchema.Properties[assistantEpisodePairIDKey].Enum
			if len(pairIDs) != test.wantPairs {
				t.Fatalf("pair id count = %d, want %d", len(pairIDs), test.wantPairs)
			}
			if requestContainsRoleSubstring(
				m.requests[1], model.RoleUser,
				fmt.Sprintf("case %d", test.wantPairs),
			) {
				t.Fatal("assistant request includes a candidate past its budget")
			}
		})
	}
}

func TestAssistantEpisodeExtractionTreatsEveryUserTurnAsBoundary(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{},
		{calls: []model.ToolCall{assistantEpisodeToolCall(`{
			"pair_id":"pair-1",
			"memory":"The assistant recommended Alpha and Beta."
		}`)}},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	imageOnlyUser := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeImage},
		},
	}
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		imageOnlyUser,
		model.NewAssistantMessage("1. Gamma\n2. Delta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if !requestContainsRoleSubstring(m.requests[1], model.RoleAssistant, "1. Alpha\n2. Beta") ||
		requestContainsRoleSubstring(m.requests[1], model.RoleAssistant, "1. Gamma\n2. Delta") {
		t.Fatal("assistant reply crossed a non-text user-turn boundary")
	}
	if len(operations) != 1 || !strings.Contains(operations[0].Memory, "Alpha and Beta") {
		t.Fatalf("operations = %#v, want the first user turn only", operations)
	}
}

func TestAssistantEpisodeExtractionDeadlineKeepsOrdinaryOperations(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{calls: []model.ToolCall{makeToolCall(memory.AddToolName, []byte(`{
			"memory":"User wants two recommendations."
		}`))}},
		{waitForCancel: true},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	messages := []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		model.NewUserMessage("Suggest two alternatives."),
		model.NewAssistantMessage("1. Gamma\n2. Delta"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	operations, err := ext.Extract(ctx, messages, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("parent context ended before ordinary operations returned: %v", ctx.Err())
	}
	if len(operations) != 1 || operations[0].Memory != "User wants two recommendations." {
		t.Fatalf("operations = %#v, want ordinary operation", operations)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
}

func TestAssistantEpisodeExtractionReturnsOrdinaryStageError(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{
		err: errors.New("ordinary extraction failed"),
	}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "ordinary extraction failed") {
		t.Fatalf("error = %v, want ordinary extraction failure", err)
	}
	if operations != nil {
		t.Fatalf("operations = %#v, want nil", operations)
	}
}

func TestAssistantEpisodeExtractionPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &assistantTestModel{steps: []assistantModelStep{
		{},
		{before: cancel, err: context.Canceled},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(ctx, []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if operations != nil {
		t.Fatalf("operations = %#v, want nil", operations)
	}
}

func TestAssistantEpisodeExtractionAllowsNoAssistantOperation(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}, {}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if len(operations) != 0 {
		t.Fatalf("operations = %#v, want none", operations)
	}
}

func TestAssistantEpisodeExtractionAllowsAssistantOnlyInput(t *testing.T) {
	m := &assistantTestModel{}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 0 {
		t.Fatalf("model calls = %d, want 0", len(m.requests))
	}
	if len(operations) != 0 {
		t.Fatalf("operations = %#v, want none", operations)
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

func TestAssistantEpisodeExtractionSkipsSecondStageWithoutAddTool(t *testing.T) {
	tests := []struct {
		name         string
		policy       UpdatePolicy
		wantRequests int
	}{
		{
			name:         "ordinary delete remains available",
			policy:       UpdatePolicyMergeSimilar,
			wantRequests: 1,
		},
		{
			name:         "policy intersection removes all tools",
			policy:       UpdatePolicyAppendOnly,
			wantRequests: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := &assistantTestModel{steps: []assistantModelStep{{}}}
			ext := NewExtractor(
				m,
				WithAssistantEpisodeExtraction(),
				WithUpdatePolicy(test.policy),
			).(*memoryExtractor)
			ext.SetEnabledTools(map[string]struct{}{
				memory.DeleteToolName: {},
			})

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
			if len(m.requests) != test.wantRequests {
				t.Fatalf("model calls = %d, want %d", len(m.requests), test.wantRequests)
			}
		})
	}
}

func TestAssistantEpisodeOrdinaryStageUsesUpdatePolicyTools(t *testing.T) {
	tests := []struct {
		name        string
		policy      UpdatePolicy
		wantTools   int
		wantAddOnly bool
	}{
		{name: "merge similar", policy: UpdatePolicyMergeSimilar, wantTools: len(backgroundTools)},
		{name: "preserve history", policy: UpdatePolicyPreserveHistory, wantTools: len(backgroundTools)},
		{name: "append only", policy: UpdatePolicyAppendOnly, wantTools: 1, wantAddOnly: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := &assistantTestModel{steps: []assistantModelStep{{}}}
			ext := NewExtractor(
				m,
				WithUpdatePolicy(test.policy),
				WithAssistantEpisodeExtraction(),
			)
			_, err := ext.Extract(context.Background(), []model.Message{
				model.NewUserMessage("Remember this preference."),
			}, nil)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if got := len(m.requests[0].Tools); got != test.wantTools {
				t.Fatalf("tool count = %d, want %d", got, test.wantTools)
			}
			if test.wantAddOnly {
				if _, ok := m.requests[0].Tools[memory.AddToolName]; !ok {
					t.Fatal("append-only ordinary stage omitted memory_add")
				}
				for _, name := range []string{
					memory.UpdateToolName,
					memory.DeleteToolName,
					memory.ClearToolName,
				} {
					if _, ok := m.requests[0].Tools[name]; ok {
						t.Fatalf("append-only ordinary stage exposed %s", name)
					}
				}
			}
		})
	}
}

func TestAssistantEpisodeExtractionSkipsAfterClearOperation(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{calls: []model.ToolCall{
		makeToolCall(memory.ClearToolName, []byte(`{"source_user_index":1}`)),
	}}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
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

func TestAssistantEpisodeExtractionSkipsAfterDeleteWithoutSource(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{
		calls: []model.ToolCall{makeToolCall(memory.DeleteToolName, []byte(`{
			"memory_id":"stale"
		}`))},
	}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
	if len(operations) != 1 || operations[0].Type != OperationDelete {
		t.Fatalf("operations = %#v", operations)
	}
}

func TestAssistantEpisodeExtractionKeepsPairUnrelatedToDelete(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{calls: []model.ToolCall{makeToolCall(memory.DeleteToolName, []byte(`{
			"memory_id":"birthday",
			"affected_source_user_indexes":[]
		}`))}},
		{calls: []model.ToolCall{assistantEpisodeToolCall(`{
			"pair_id":"pair-1",
			"memory":"The assistant recommended Alpha and Beta."
		}`)}},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two products."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		model.NewUserMessage("Forget my birthday."),
		model.NewAssistantMessage("Done."),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if len(operations) != 2 || operations[0].Type != OperationDelete ||
		operations[1].Type != OperationAdd {
		t.Fatalf("operations = %#v", operations)
	}
}

func TestAssistantEpisodeExtractionSkipsPairCoveredByDelete(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{
		calls: []model.ToolCall{makeToolCall(memory.DeleteToolName, []byte(`{
			"memory_id":"recommendation",
			"affected_source_user_indexes":[1]
		}`))},
	}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two products."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		model.NewUserMessage("Forget that recommendation."),
		model.NewAssistantMessage("Done."),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
	if len(operations) != 1 || operations[0].Type != OperationDelete {
		t.Fatalf("operations = %#v", operations)
	}
}

func TestAssistantEpisodeExtractionContinuesAfterEarlierClear(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{calls: []model.ToolCall{makeToolCall(
			memory.ClearToolName,
			[]byte(`{"source_user_index":1}`),
		)}},
		{calls: []model.ToolCall{assistantEpisodeToolCall(`{
			"pair_id":"pair-1",
			"memory":"The assistant recommended Alpha and Beta."
		}`)}},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Clear all memories."),
		model.NewAssistantMessage("Understood."),
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
	if len(operations) != 2 || operations[0].Type != OperationClear ||
		operations[1].Type != OperationAdd {
		t.Fatalf("operations = %#v", operations)
	}
}

func TestAssistantEpisodeExtractionSkipsPairBeforeLaterClear(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{calls: []model.ToolCall{
		makeToolCall(memory.ClearToolName, []byte(`{"source_user_index":2}`)),
	}}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		model.NewUserMessage("Clear all memories."),
		model.NewAssistantMessage("Understood."),
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

func TestAssistantEpisodeExtractionUserWordingDoesNotControlOptionalStage(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{}, {}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	_, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Please forget my old recommendation."),
		model.NewAssistantMessage("Understood."),
		model.NewUserMessage("Never mind, keep it."),
		model.NewAssistantMessage("Understood."),
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(m.requests))
	}
}

func TestAssistantEpisodeOperationSourceIndex(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		userCount int
		want      int
		wantOK    bool
	}{
		{"valid", `{"source_user_index":2}`, 3, 2, true},
		{"missing", `{}`, 3, 0, false},
		{"out of range", `{"source_user_index":4}`, 3, 0, false},
		{"malformed", `{`, 3, 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			call := makeToolCall(memory.ClearToolName, []byte(test.arguments))
			got, ok := assistantEpisodeOperationSourceIndex(call, test.userCount)
			if got != test.want || ok != test.wantOK {
				t.Fatalf("source index = (%d, %v), want (%d, %v)", got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestAssistantEpisodeDeleteSourceIndexes(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		userCount int
		want      []int
		wantOK    bool
	}{
		{"empty", `{"affected_source_user_indexes":[]}`, 3, []int{}, true},
		{"sorted unique", `{"affected_source_user_indexes":[2,1,2]}`, 3, []int{1, 2}, true},
		{"missing", `{}`, 3, nil, false},
		{"null", `{"affected_source_user_indexes":null}`, 3, nil, false},
		{"out of range", `{"affected_source_user_indexes":[4]}`, 3, nil, false},
		{"wrong type", `{"affected_source_user_indexes":1}`, 3, nil, false},
		{"malformed", `{`, 3, nil, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			call := makeToolCall(memory.DeleteToolName, []byte(test.arguments))
			got, ok := assistantEpisodeDeleteSourceIndexes(call, test.userCount)
			if !reflect.DeepEqual(got, test.want) || ok != test.wantOK {
				t.Fatalf("source indexes = (%v, %v), want (%v, %v)",
					got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestAssistantEpisodeOrdinaryDestructiveToolsRequireSource(t *testing.T) {
	tools := assistantEpisodeOrdinaryTools(backgroundTools, 2)
	clearDeclaration := tools[memory.ClearToolName].Declaration()
	clearProperty := clearDeclaration.InputSchema.Properties[assistantEpisodeSourceIndexKey]
	if clearProperty == nil ||
		!slices.Contains(clearDeclaration.InputSchema.Required, assistantEpisodeSourceIndexKey) {
		t.Fatal("memory_clear does not require its source user index")
	}
	if got, want := clearProperty.Enum, []any{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("memory_clear source enum = %#v, want %#v", got, want)
	}

	deleteDeclaration := tools[memory.DeleteToolName].Declaration()
	deleteProperty := deleteDeclaration.InputSchema.Properties[assistantEpisodeAffectedSourceIndexesKey]
	if deleteProperty == nil || !slices.Contains(
		deleteDeclaration.InputSchema.Required,
		assistantEpisodeAffectedSourceIndexesKey,
	) {
		t.Fatal("memory_delete does not require affected source indexes")
	}
	if deleteProperty.Type != "array" || deleteProperty.Items == nil {
		t.Fatalf("memory_delete source schema = %#v", deleteProperty)
	}
	if got, want := deleteProperty.Items.Enum, []any{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("memory_delete source enum = %#v, want %#v", got, want)
	}
	for _, name := range []string{memory.DeleteToolName, memory.ClearToolName} {
		for _, key := range []string{
			assistantEpisodeSourceIndexKey,
			assistantEpisodeAffectedSourceIndexesKey,
		} {
			if _, ok := backgroundTools[name].Declaration().InputSchema.Properties[key]; ok {
				t.Fatalf("request-local property %s mutated shared %s", key, name)
			}
		}
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

func TestExtractAssistantEpisodeRejectsInvalidArguments(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{
		calls: []model.ToolCall{
			assistantEpisodeToolCall(`{"memory":`),
			assistantEpisodeToolCall(`{
				"pair_id":"pair-1",
				"memory":"The assistant recommended Alpha and Beta."
			}`),
		},
	}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	_, operations, err := ext.extractAssistantEpisodes(
		context.Background(),
		[]assistantEpisodePair{{
			user:      model.NewUserMessage("Recommend two options."),
			assistant: model.NewAssistantMessage("1. Alpha\n2. Beta"),
		}},
	)
	if err != nil {
		t.Fatalf("error = %v, want content rejection to be non-fatal", err)
	}
	if len(operations) != 1 || !strings.Contains(operations[0].Memory, "Alpha and Beta") {
		t.Fatalf("operations = %#v, want later valid call", operations)
	}
}

func TestAssistantEpisodeExtractionSkipsInvalidContentAndKeepsOrdinaryOperations(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{calls: []model.ToolCall{makeToolCall(memory.AddToolName, []byte(`{
			"memory":"User wants two recommendations."
		}`))}},
		{calls: []model.ToolCall{assistantEpisodeToolCall(`{
				"pair_id":"pair-1",
				"memory":"The assistant recommended 99 options."
		}`)}},
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
		t.Fatalf("operations = %#v, want ordinary operation only", operations)
	}
}

func TestAssistantEpisodeExtractionBatchSkipsOnlyInvalidPair(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{},
		{calls: []model.ToolCall{
			assistantEpisodeToolCall(`{
				"pair_id":"pair-1",
				"memory":"The assistant recommended 99 options."
			}`),
			assistantEpisodeToolCall(`{
				"pair_id":"pair-2",
				"memory":"The assistant recommended Gamma and Delta."
			}`),
		}},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		model.NewUserMessage("Suggest two alternatives."),
		model.NewAssistantMessage("1. Gamma\n2. Delta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 1 || !strings.Contains(operations[0].Memory, "Gamma and Delta") {
		t.Fatalf("operations = %#v, want valid pair only", operations)
	}
}

func TestAssistantEpisodeExtractionBatchRejectsInvalidCallsIndependently(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{
		{},
		{calls: []model.ToolCall{
			makeToolCall(memory.AddToolName, []byte(`{"memory":"ignored"}`)),
			assistantEpisodeToolCall(`{"pair_id":`),
			assistantEpisodeToolCall(`{
				"pair_id":"unknown",
				"memory":"The assistant recommended Alpha and Beta."
			}`),
			assistantEpisodeToolCall(`{
				"pair_id":"pair-1",
				"memory":"The assistant recommended Alpha and Beta."
			}`),
			assistantEpisodeToolCall(`{
				"pair_id":"pair-1",
				"memory":"The assistant recommended a duplicate result."
			}`),
			assistantEpisodeToolCall(`{
				"pair_id":"pair-2",
				"memory":"The assistant recommended Gamma and Delta."
			}`),
		}},
	}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction())
	operations, err := ext.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Recommend two options."),
		model.NewAssistantMessage("1. Alpha\n2. Beta"),
		model.NewUserMessage("Suggest two alternatives."),
		model.NewAssistantMessage("1. Gamma\n2. Delta"),
	}, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 2 {
		t.Fatalf("operations = %#v, want one result per valid pair", operations)
	}
	if !strings.Contains(operations[0].Memory, "Alpha and Beta") ||
		!strings.Contains(operations[1].Memory, "Gamma and Delta") {
		t.Fatalf("operations are not ordered by source pair: %#v", operations)
	}
}

func TestExtractAssistantEpisodeUsesFirstValidCall(t *testing.T) {
	m := &assistantTestModel{steps: []assistantModelStep{{calls: []model.ToolCall{
		makeToolCall(memory.AddToolName, []byte(`{"memory":"ignored"}`)),
		assistantEpisodeToolCall(`{
			"pair_id":"pair-1",
			"memory":"The assistant recommended Alpha and Beta."
		}`),
		assistantEpisodeToolCall(`{
			"pair_id":"pair-1",
			"memory":"The assistant recommended Gamma and Delta."
		}`),
	}}}}
	ext := NewExtractor(m, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	_, operations, err := ext.extractAssistantEpisodes(
		context.Background(),
		[]assistantEpisodePair{{
			user:      model.NewUserMessage("Recommend two options."),
			assistant: model.NewAssistantMessage("1. Alpha\n2. Beta"),
		}},
	)
	if err != nil {
		t.Fatalf("extract assistant episode: %v", err)
	}
	want := assistantEpisodePrefix + "The assistant recommended Alpha and Beta."
	if len(operations) != 1 || operations[0].Memory != want {
		t.Fatalf("operations = %#v, want memory %q", operations, want)
	}
}

func TestParseAssistantEpisodeValidation(t *testing.T) {
	ext := NewExtractor(nil, WithAssistantEpisodeExtraction()).(*memoryExtractor)
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "missing memory",
			args: map[string]any{},
			want: "memory is required",
		},
		{
			name: "oversized memory",
			args: map[string]any{
				argKeyMemory: strings.Repeat("x", assistantEpisodeMaxBytes+1),
			},
			want: "exceeds 4096 bytes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ext.parseAssistantEpisode(
				context.Background(),
				test.args,
				"The assistant recommended 17 options.",
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	op, err := ext.parseAssistantEpisode(
		context.Background(),
		map[string]any{
			argKeyMemory: "The assistant recommended 17 options.",
			argKeyTopics: []any{"recommendations", "options"},
		},
		"The assistant recommended 17 options.",
	)
	if err != nil {
		t.Fatalf("parse grounded episode: %v", err)
	}
	if op.EventTime != nil {
		t.Fatalf("event time = %v, want nil", op.EventTime)
	}
	if !reflect.DeepEqual(op.Topics, []string{"recommendations", "options"}) {
		t.Fatalf("topics = %#v", op.Topics)
	}
}

func TestSelectAssistantEpisodePairs(t *testing.T) {
	toolAssistant := model.NewAssistantMessage("1. Alpha\n2. Beta")
	toolAssistant.ToolID = "tool-call"
	callingAssistant := model.NewAssistantMessage("1. Alpha\n2. Beta")
	callingAssistant.ToolCalls = []model.ToolCall{{}}
	tests := []struct {
		name     string
		messages []model.Message
	}{
		{
			name:     "no assistant",
			messages: []model.Message{model.NewUserMessage("Recommend options.")},
		},
		{
			name:     "assistant tool message",
			messages: []model.Message{model.NewUserMessage("Recommend options."), toolAssistant},
		},
		{
			name:     "assistant making tool call",
			messages: []model.Message{model.NewUserMessage("Recommend options."), callingAssistant},
		},
		{
			name:     "empty assistant",
			messages: []model.Message{model.NewUserMessage("Recommend options."), model.NewAssistantMessage("")},
		},
		{
			name:     "assistant without user",
			messages: []model.Message{model.NewAssistantMessage("1. Alpha\n2. Beta")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if pairs := selectAssistantEpisodePairs(test.messages); len(pairs) != 0 {
				t.Fatalf("pairs = %#v, want none", pairs)
			}
		})
	}
}

func TestValidateAssistantEpisodeQuantities(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		memoryText string
		wantError  bool
	}{
		{
			name:       "equivalent normalized quantities",
			source:     "The result was -5°C, cost $5, weighed 1,000.25 kg, measured 5 m, lasted 5 min, held 5 ml, and reached 20%.",
			memoryText: "The result was -5 °C, cost USD 5, weighed 1000.25 kilograms, measured 5 meters, lasted 5 minutes, held 5 milliliters, and reached 20 percent.",
		},
		{
			name:       "ordinary count nouns are not units",
			source:     "The assistant proposed 3 meals.",
			memoryText: "The assistant provided 3 recipes.",
		},
		{
			name:       "date separators are not negative signs",
			source:     "The event happened on 2023-04-15.",
			memoryText: "The recorded date was 2023-04-15.",
		},
		{
			name:       "removed negative sign",
			source:     "The temperature was -5°C.",
			memoryText: "The temperature was 5°C.",
			wantError:  true,
		},
		{
			name:       "removed sign before currency",
			source:     "The adjustment was -$5.",
			memoryText: "The adjustment was $5.",
			wantError:  true,
		},
		{
			name:       "equivalent sign placement around currency",
			source:     "The adjustment was -$5.",
			memoryText: "The adjustment was $-5.",
		},
		{
			name:       "equivalent decimal currency",
			source:     "The price was $5.00.",
			memoryText: "The price was USD 5.",
		},
		{
			name:       "changed currency",
			source:     "The price was $5.",
			memoryText: "The price was €5.",
			wantError:  true,
		},
		{
			name:       "changed unit",
			source:     "The package weighed 5 kg.",
			memoryText: "The package weighed 5 lb.",
			wantError:  true,
		},
		{
			name:       "removed percentage",
			source:     "The discount was 20%.",
			memoryText: "The discount was 20.",
			wantError:  true,
		},
		{
			name:       "changed time unit",
			source:     "The plan takes 5 days.",
			memoryText: "The plan takes 5 weeks.",
			wantError:  true,
		},
		{
			name:       "changed prefix-sharing time unit",
			source:     "The plan takes 5 minutes.",
			memoryText: "The plan takes 5 months.",
			wantError:  true,
		},
		{
			name:       "removed time unit",
			source:     "The plan takes 5 minutes.",
			memoryText: "The plan takes 5.",
			wantError:  true,
		},
		{
			name:       "percentage followed by text",
			source:     "The offer is 20%off.",
			memoryText: "The offer is 20 off.",
			wantError:  true,
		},
		{
			name:       "equivalent compound unit",
			source:     "The speed was 5 km/h.",
			memoryText: "The speed was 5 kilometers/hour.",
		},
		{
			name:       "changed compound unit",
			source:     "The speed was 5 km/h.",
			memoryText: "The speed was 5 km/s.",
			wantError:  true,
		},
		{
			name:       "attached identifier cannot hide a changed number",
			source:     "The selected model is item5.",
			memoryText: "The selected model is item6.",
			wantError:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAssistantEpisodeQuantities(test.memoryText, test.source)
			if test.wantError && err == nil {
				t.Fatal("quantity change was accepted")
			}
			if !test.wantError && err != nil {
				t.Fatalf("valid quantities were rejected: %v", err)
			}
		})
	}
}

func TestParseAssistantEpisodeQuantityBoundariesAndCurrencies(t *testing.T) {
	if _, ok := parseAssistantEpisodeQuantity("", nil); ok {
		t.Fatal("invalid match indexes were accepted")
	}

	parse := func(text string) (assistantEpisodeQuantity, bool) {
		t.Helper()
		match := assistantEpisodeQuantityPattern.FindStringSubmatchIndex(text)
		if match == nil {
			t.Fatalf("quantity pattern did not match %q", text)
		}
		return parseAssistantEpisodeQuantity(text, match)
	}
	for _, text := range []string{"item5", "5items"} {
		quantity, ok := parse(text)
		if !ok || quantity.value != "5" {
			t.Fatalf("identifier-embedded quantity %q was not grounded: %#v", text, quantity)
		}
	}

	tests := []struct {
		text string
		want assistantEpisodeQuantity
	}{
		{
			text: "5 EUR",
			want: assistantEpisodeQuantity{value: "5", currency: "eur"},
		},
		{
			text: "$5 EUR",
			want: assistantEpisodeQuantity{value: "5", currency: "usd/eur"},
		},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			got, ok := parse(test.text)
			if !ok {
				t.Fatalf("quantity %q was rejected", test.text)
			}
			if got != test.want {
				t.Fatalf("quantity = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNormalizeAssistantEpisodeNumber(t *testing.T) {
	tests := map[string]string{
		"1000":     "1000",
		"1,000":    "1000",
		"1,000.25": "1000.25",
		"5.00":     "5",
		"5.20":     "5.2",
		"1234,567": "1234,567",
		"1,23":     "1,23",
	}
	for input, want := range tests {
		if got := normalizeAssistantEpisodeNumber(input); got != want {
			t.Fatalf("normalize %q = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeAssistantEpisodeCurrency(t *testing.T) {
	tests := map[string]string{
		"$":       "usd",
		" usd ":   "usd",
		"€":       "eur",
		"GBP":     "gbp",
		"¥":       "yen-yuan",
		"JPY":     "jpy",
		"RMB":     "cny",
		"unknown": "",
	}
	for input, want := range tests {
		if got := normalizeAssistantEpisodeCurrency(input); got != want {
			t.Fatalf("normalize %q = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeAssistantEpisodeUnit(t *testing.T) {
	tests := map[string]string{
		"percentage":  "%",
		"° C":         "°c",
		"°F":          "°f",
		"°K":          "°k",
		"kilograms":   "kg",
		"milligrams":  "mg",
		"grams":       "g",
		"pounds":      "lb",
		"kilometres":  "km",
		"miles":       "mi",
		"centimetres": "cm",
		"millimetres": "mm",
		"metres":      "m",
		"millilitres": "ml",
		"litres":      "l",
		"hours":       "h",
		"minutes":     "min",
		"seconds":     "s",
		"days":        "day",
		"weeks":       "week",
		"months":      "month",
		"years":       "year",
		"km / hours":  "km/h",
		"unknown":     "",
		"km/unknown":  "",
		"km/h/s":      "",
	}
	for input, want := range tests {
		if got := normalizeAssistantEpisodeUnit(input); got != want {
			t.Fatalf("normalize %q = %q, want %q", input, got, want)
		}
	}
}

func TestAssistantEpisodeMessageTextUsesTextParts(t *testing.T) {
	text := " part text "
	empty := "  "
	message := model.Message{
		Content: " body text ",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeImage},
			{Type: model.ContentTypeText},
			{Type: model.ContentTypeText, Text: &empty},
			{Type: model.ContentTypeText, Text: &text},
		},
	}
	if got, want := assistantEpisodeMessageText(message), "body text\npart text"; got != want {
		t.Fatalf("message text = %q, want %q", got, want)
	}
	message.Content = ""
	if got, want := assistantEpisodeMessageText(message), "part text"; got != want {
		t.Fatalf("content-part text = %q, want %q", got, want)
	}
}

func TestAssistantEpisodeToolAvailability(t *testing.T) {
	tests := []struct {
		name    string
		enabled map[string]struct{}
		want    bool
	}{
		{name: "not configured", want: true},
		{name: "empty", enabled: map[string]struct{}{}, want: false},
		{
			name:    "add enabled",
			enabled: map[string]struct{}{memory.AddToolName: {}},
			want:    true,
		},
		{
			name:    "different tool enabled",
			enabled: map[string]struct{}{memory.ClearToolName: {}},
			want:    false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ext := &memoryExtractor{enabledTools: test.enabled}
			if got := ext.assistantEpisodeAddEnabled(); got != test.want {
				t.Fatalf("assistant add enabled = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAssistantEpisodeToolSchemaIsStorageCompatible(t *testing.T) {
	declaration := newAssistantEpisodeTool([]assistantEpisodeSource{{id: "pair-1"}}).Declaration()
	if got := len(declaration.InputSchema.Properties); got != 3 {
		t.Fatalf("property count = %d, want 3", got)
	}
	if !slices.Contains(declaration.InputSchema.Required, argKeyMemory) ||
		!slices.Contains(declaration.InputSchema.Required, assistantEpisodePairIDKey) {
		t.Fatalf("required fields = %#v", declaration.InputSchema.Required)
	}
	if got := declaration.InputSchema.Properties[assistantEpisodePairIDKey].Enum; !reflect.DeepEqual(got, []any{"pair-1"}) {
		t.Fatalf("pair id enum = %#v", got)
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
			name:      "structured response without list",
			user:      "Recommend options.",
			assistant: "Alpha is the best option.",
			want:      false,
		},
		{
			name:      "quantity",
			user:      "How many options are available?",
			assistant: "There are 17 options.",
			want:      true,
		},
		{
			name:      "number repeated by user",
			user:      "How many of the 17 options are available?",
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
	const suffix = "-suffix!"
	input := "prefix-" + strings.Repeat("界", assistantEpisodeSourceMaxBytes) + suffix
	excerpt := assistantEpisodeSourceExcerpt(input)
	if len(excerpt) > assistantEpisodeSourceMaxBytes {
		t.Fatalf("excerpt bytes = %d, want <= %d", len(excerpt), assistantEpisodeSourceMaxBytes)
	}
	if !utf8.ValidString(excerpt) {
		t.Fatal("excerpt is not valid UTF-8")
	}
	if !strings.HasPrefix(excerpt, "prefix-") || !strings.HasSuffix(excerpt, suffix) {
		t.Fatalf("excerpt does not preserve source boundaries: %q", excerpt)
	}
	if !strings.Contains(excerpt, assistantEpisodeTruncationMarker) {
		t.Fatal("excerpt does not report truncation")
	}
}

func TestAssistantEpisodeSourceExcerptPreservesInteriorInvalidUTF8(t *testing.T) {
	input := []byte(strings.Repeat("x", assistantEpisodeSourceMaxBytes*2))
	input[17] = 0xff
	input[len(input)-17] = 0xfe

	excerpt := assistantEpisodeSourceExcerpt(string(input))
	if len(excerpt) > assistantEpisodeSourceMaxBytes {
		t.Fatalf("excerpt bytes = %d, want <= %d", len(excerpt), assistantEpisodeSourceMaxBytes)
	}
	if !strings.Contains(excerpt, string([]byte{0xff})) {
		t.Fatal("excerpt discarded an invalid byte inside the retained head")
	}
	if !strings.Contains(excerpt, string([]byte{0xfe})) {
		t.Fatal("excerpt discarded an invalid byte inside the retained tail")
	}
	if !strings.Contains(excerpt, assistantEpisodeTruncationMarker) {
		t.Fatal("excerpt does not report truncation")
	}
}

func TestAssistantEpisodeSourceExcerptRepairsOnlySplitRune(t *testing.T) {
	const text = "a界b"
	if got := trimSplitUTF8End(text, 2); got != 1 {
		t.Fatalf("head boundary = %d, want 1", got)
	}
	if got := trimSplitUTF8Start(text, 2); got != 4 {
		t.Fatalf("tail boundary = %d, want 4", got)
	}
	if got := trimSplitUTF8End(text, 1); got != 1 {
		t.Fatalf("safe head boundary = %d, want 1", got)
	}
	if got := trimSplitUTF8Start(text, len(text)); got != len(text) {
		t.Fatalf("terminal tail boundary = %d, want %d", got, len(text))
	}

	invalid := string([]byte{'a', 0x80, 0x80, 0x80, 0x80, 'b'})
	if got := trimSplitUTF8End(invalid, 4); got != 4 {
		t.Fatalf("invalid head boundary = %d, want 4", got)
	}
	if got := trimSplitUTF8Start(invalid, 4); got != 4 {
		t.Fatalf("invalid tail boundary = %d, want 4", got)
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

func requestContainsRoleSubstring(
	request *model.Request,
	role model.Role,
	content string,
) bool {
	for _, message := range request.Messages {
		if message.Role == role && strings.Contains(message.Content, content) {
			return true
		}
	}
	return false
}
