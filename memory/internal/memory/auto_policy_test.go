//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type countingOperator struct {
	*mockOperator
	searchCalls int
}

func (o *countingOperator) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	o.searchCalls++
	return o.mockOperator.SearchMemories(ctx, userKey, query, opts...)
}

func newExtractorWithOperation(
	t *testing.T,
	policy extractor.UpdatePolicy,
	op *extractor.Operation,
) *extractor.Extractor {
	t.Helper()
	args := make(map[string]any)
	var toolName string
	switch op.Type {
	case extractor.OperationAdd:
		toolName = memory.AddToolName
		args["memory"] = op.Memory
	case extractor.OperationUpdate:
		toolName = memory.UpdateToolName
		args["memory_id"] = op.MemoryID
		args["memory"] = op.Memory
	case extractor.OperationDelete:
		toolName = memory.DeleteToolName
		args["memory_id"] = op.MemoryID
	case extractor.OperationClear:
		toolName = memory.ClearToolName
	default:
		t.Fatalf("unsupported operation type %q", op.Type)
	}
	if len(op.Topics) > 0 {
		args["topics"] = op.Topics
	}
	if op.MemoryKind != "" {
		args["memory_kind"] = string(op.MemoryKind)
	}
	if op.EventTime != nil {
		args["event_time"] = op.EventTime.Format(time.RFC3339Nano)
	}
	if len(op.Participants) > 0 {
		args["participants"] = op.Participants
	}
	if op.Location != "" {
		args["location"] = op.Location
	}
	payload, err := json.Marshal(args)
	require.NoError(t, err)
	return extractor.NewExtractor(
		newMockModelWithToolCalls([]model.ToolCall{{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      toolName,
				Arguments: payload,
			},
		}}),
		extractor.WithUpdatePolicy(policy),
	)
}

func TestUpdatePolicyFor_UsesBuiltInExtractorPolicy(t *testing.T) {
	for _, policy := range []extractor.UpdatePolicy{
		extractor.UpdatePolicyCompatible,
		extractor.UpdatePolicyStrict,
		extractor.UpdatePolicyAddOnly,
	} {
		builtin := extractor.NewExtractor(
			nil,
			extractor.WithUpdatePolicy(policy),
		)
		assert.Equal(t, policy, updatePolicyFor(builtin))
	}
	assert.Equal(t, extractor.UpdatePolicyCompatible, updatePolicyFor(&mockExtractor{}))
}

func TestStrictPolicy_AliceTimeEnrichmentUpdates(t *testing.T) {
	oldTime := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	existing := []*memory.Entry{{
		ID: "alice-visit",
		Memory: &memory.Memory{
			Memory:    "Alice visited Bob on December 1st, 2025.",
			Topics:    []string{"Alice", "Bob", "visit"},
			Kind:      memory.KindEpisode,
			EventTime: &oldTime,
		},
	}}
	op := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		Topics:     []string{"Alice", "Bob", "visit"},
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcileStrictOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "alice-visit", out[0].MemoryID)
	assert.Equal(t, &newTime, out[0].EventTime)
}

func TestStrictPolicy_ExactDuplicateIgnoresTopicDrift(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "same",
		Memory: &memory.Memory{
			Memory: "User likes coffee.",
			Topics: []string{"coffee"},
			Kind:   memory.KindFact,
		},
	}}
	op := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "  USER likes coffee  ",
		Topics:     []string{"coffee", "preferences"},
		MemoryKind: memory.KindFact,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcileStrictOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	assert.Empty(t, out)
}

func TestStrictPolicy_ChangesRemainAdditive(t *testing.T) {
	tests := []struct {
		name    string
		oldText string
		newText string
		oldTime *time.Time
		newTime *time.Time
	}{
		{
			name:    "changed employer",
			oldText: "User works at Acme as an engineer.",
			newText: "User now works at Globex as an engineer.",
		},
		{
			name:    "single letter employer",
			oldText: "User works at A.",
			newText: "User works at B.",
		},
		{
			name:    "new negation",
			oldText: "User drinks coffee every morning.",
			newText: "User does not drink coffee every morning.",
		},
		{
			name: "single attribute replaced in long text",
			oldText: "Alice keeps the important family travel folder in the green cabinet " +
				"beside the upstairs bedroom window for future trips.",
			newText: "Alice keeps the important family travel folder in the red cabinet " +
				"beside the upstairs bedroom window for future trips.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := []*memory.Entry{{
				ID: "stored",
				Memory: &memory.Memory{
					Memory:    test.oldText,
					Kind:      memory.KindFact,
					EventTime: test.oldTime,
				},
			}}
			op := &extractor.Operation{
				Type:       extractor.OperationAdd,
				Memory:     test.newText,
				MemoryKind: memory.KindFact,
				EventTime:  test.newTime,
			}
			worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
			out := worker.reconcileStrictOps(
				context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
			)
			require.Len(t, out, 1)
			assert.Equal(t, extractor.OperationAdd, out[0].Type)
		})
	}
}

func TestStrictPolicy_DifferentEventDateRemainsAdditive(t *testing.T) {
	oldTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 12, 2, 16, 0, 0, 0, time.UTC)
	existing := []*memory.Entry{{
		ID: "visit-one",
		Memory: &memory.Memory{
			Memory:    "Alice visited Bob at 4pm on December 1st, 2025.",
			Kind:      memory.KindEpisode,
			EventTime: &oldTime,
		},
	}}
	op := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 2nd, 2025.",
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcileStrictOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
}

func TestStrictPolicy_UnsafeModelUpdateBecomesAdd(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "job",
		Memory: &memory.Memory{
			Memory: "User works at Acme.",
			Kind:   memory.KindFact,
		},
	}}
	op := &extractor.Operation{
		Type:       extractor.OperationUpdate,
		MemoryID:   "job",
		Memory:     "User now works at Globex.",
		MemoryKind: memory.KindFact,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcileStrictOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestAddOnlyPolicy_ConvertsUpdateToAdd(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	op := &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "old",
		Memory:   "new content",
	}
	out := worker.applyAddOnlyPolicy(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestCompatiblePolicy_PreservesEveryOperationType(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyCompatible)
	ops := []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "new memory"},
		{Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "updated memory"},
		{Type: extractor.OperationDelete, MemoryID: "stored"},
		{Type: extractor.OperationClear},
	}
	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops, nil, nil,
	)
	require.Len(t, out, len(ops))
	for index, op := range ops {
		assert.Equal(t, op.Type, out[index].Type)
	}
}

func TestStrictPolicy_OperationContract(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyStrict)
	ops := []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "new memory"},
		{Type: extractor.OperationUpdate, MemoryID: "missing", Memory: "updated memory"},
		{Type: extractor.OperationDelete, MemoryID: "stored"},
		{Type: extractor.OperationClear},
	}
	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops, nil,
		[]model.Message{model.NewUserMessage("I have changed my preferences.")},
	)
	require.Len(t, out, 2)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Equal(t, extractor.OperationAdd, out[1].Type)

	out = worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops[2:], nil,
		[]model.Message{model.NewUserMessage("Please forget everything about me.")},
	)
	require.Len(t, out, 2)
	assert.Equal(t, extractor.OperationDelete, out[0].Type)
	assert.Equal(t, extractor.OperationClear, out[1].Type)
}

func TestAddOnlyPolicy_OperationContract(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyAddOnly)
	existing := []*memory.Entry{{
		ID: "stored",
		Memory: &memory.Memory{
			Memory: "existing memory",
			Kind:   memory.KindFact,
		},
	}}
	ops := []*extractor.Operation{
		nil,
		{Type: extractor.OperationAdd, Memory: "existing memory"},
		{Type: extractor.OperationAdd, Memory: "new memory"},
		{Type: extractor.OperationAdd, Memory: " NEW memory! "},
		{Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "existing memory"},
		{Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "updated memory"},
		{Type: extractor.OperationDelete, MemoryID: "stored"},
		{Type: extractor.OperationClear},
		{Type: extractor.OperationType("unknown")},
	}
	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops, existing, nil,
	)
	require.Len(t, out, 2)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Equal(t, "new memory", out[0].Memory)
	assert.Equal(t, extractor.OperationAdd, out[1].Type)
	assert.Equal(t, "updated memory", out[1].Memory)
	assert.Empty(t, out[1].MemoryID)
}

func TestExplicitDestructiveRequest(t *testing.T) {
	tests := []struct {
		name        string
		messages    []model.Message
		allowDelete bool
		allowClear  bool
	}{
		{
			name:        "explicit delete",
			messages:    []model.Message{model.NewUserMessage("Please forget my coffee preference.")},
			allowDelete: true,
		},
		{
			name:        "explicit clear",
			messages:    []model.Message{model.NewUserMessage("Could you please clear all my memories?")},
			allowDelete: true,
			allowClear:  true,
		},
		{
			name:        "specific delete cannot authorize clear",
			messages:    []model.Message{model.NewUserMessage("Delete my coffee preference.")},
			allowDelete: true,
		},
		{
			name:     "negated request",
			messages: []model.Message{model.NewUserMessage("Please do not delete my coffee preference.")},
		},
		{
			name:     "assistant request is ignored",
			messages: []model.Message{model.NewAssistantMessage("Please forget everything about the user.")},
		},
		{
			name:        "explicit chinese delete",
			messages:    []model.Message{model.NewUserMessage("请删除我的咖啡偏好。")},
			allowDelete: true,
		},
		{
			name:        "explicit chinese clear",
			messages:    []model.Message{model.NewUserMessage("请清空所有记忆。")},
			allowDelete: true,
			allowClear:  true,
		},
		{
			name:     "negated chinese request",
			messages: []model.Message{model.NewUserMessage("请不要删除我的咖啡偏好。")},
		},
		{
			name: "latest negation wins",
			messages: []model.Message{
				model.NewUserMessage("Please clear all my memories."),
				model.NewUserMessage("Do not clear my memories."),
			},
		},
		{
			name: "latest specific request narrows clear",
			messages: []model.Message{
				model.NewUserMessage("Please clear all my memories."),
				model.NewUserMessage("Actually, please delete only my coffee preference."),
			},
			allowDelete: true,
		},
		{
			name:        "partial clear does not authorize clear",
			messages:    []model.Message{model.NewUserMessage("Clear everything except my coffee preference.")},
			allowDelete: true,
		},
		{
			name:        "partial chinese clear does not authorize clear",
			messages:    []model.Message{model.NewUserMessage("请清空除了咖啡偏好以外的所有记忆。")},
			allowDelete: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.allowDelete, hasExplicitDestructiveRequest(
				test.messages, extractor.OperationDelete,
			))
			assert.Equal(t, test.allowClear, hasExplicitDestructiveRequest(
				test.messages, extractor.OperationClear,
			))
		})
	}
	assert.False(t, hasExplicitDestructiveRequest(
		[]model.Message{model.NewUserMessage("Please forget my coffee preference.")},
		extractor.OperationType("unknown"),
	))
}

func newPolicyWorker(policy extractor.UpdatePolicy) *AutoMemoryWorker {
	return NewAutoMemoryWorker(AutoMemoryConfig{
		Extractor: extractor.NewExtractor(
			nil,
			extractor.WithUpdatePolicy(policy),
		),
	}, newMockOperator())
}

func TestStrictPolicy_DoesNotSearchPerOperation(t *testing.T) {
	existing := &memory.Entry{
		ID:      "alice-visit",
		AppName: "app",
		UserID:  "u1",
		Memory: &memory.Memory{
			Memory: "Alice visited Bob on December 1st, 2025.",
			Kind:   memory.KindFact,
		},
	}
	baseOperator := newMockOperator()
	baseOperator.searchResults = []*memory.Entry{existing}
	operator := &countingOperator{mockOperator: baseOperator}
	ext := newExtractorWithOperation(t, extractor.UpdatePolicyStrict, &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		MemoryKind: memory.KindFact,
	})
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	require.NoError(t, worker.createAutoMemory(context.Background(), reconcileUserKey(), []model.Message{
		model.NewUserMessage("Alice visited Bob at 4pm on December 1st, 2025."),
	}))
	assert.Equal(t, 1, operator.searchCalls)
	assert.Equal(t, 1, operator.updateCalls)
}

func TestUpdatePolicies_PreserveCompatibleSearchBehavior(t *testing.T) {
	existing := &memory.Entry{
		ID: "stored",
		Memory: &memory.Memory{
			Memory: "User likes tea.",
			Kind:   memory.KindFact,
		},
	}
	tests := []struct {
		name        string
		policy      extractor.UpdatePolicy
		operation   *extractor.Operation
		searchCalls int
		addCalls    int
	}{
		{
			name:   "compatible keeps per-add reconciliation",
			policy: extractor.UpdatePolicyCompatible,
			operation: &extractor.Operation{
				Type: extractor.OperationAdd, Memory: "User likes tea.",
			},
			searchCalls: 2,
		},
		{
			name:   "add-only converts update without reconciliation",
			policy: extractor.UpdatePolicyAddOnly,
			operation: &extractor.Operation{
				Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "User likes coffee.",
			},
			searchCalls: 1,
			addCalls:    1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseOperator := newMockOperator()
			baseOperator.searchResults = []*memory.Entry{existing}
			operator := &countingOperator{mockOperator: baseOperator}
			ext := newExtractorWithOperation(t, test.policy, test.operation)
			worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
			require.NoError(t, worker.createAutoMemory(
				context.Background(),
				reconcileUserKey(),
				[]model.Message{model.NewUserMessage("I like tea.")},
			))
			assert.Equal(t, test.searchCalls, operator.searchCalls)
			assert.Equal(t, test.addCalls, operator.addCalls)
		})
	}
}

func TestPolicySearchQuery_IncludesAssistantAndBoundsUTF8(t *testing.T) {
	query := buildPolicySearchQuery([]model.Message{
		model.NewUserMessage("user fact"),
		model.NewAssistantMessage("assistant fact"),
		model.NewToolMessage("call", "tool", "ignored"),
		{
			Role:    model.RoleAssistant,
			Content: "assistant tool result ignored",
			ToolID:  "tool-call",
		},
		{
			Role:      model.RoleAssistant,
			Content:   "assistant tool call ignored",
			ToolCalls: []model.ToolCall{{Type: "function"}},
		},
	})
	assert.Contains(t, query, "user fact")
	assert.Contains(t, query, "assistant fact")
	assert.NotContains(t, query, "ignored")

	query = buildPolicySearchQuery([]model.Message{
		model.NewUserMessage(strings.Repeat("history ", maxAutoMemorySearchQueryBytes)),
		model.NewAssistantMessage(strings.Repeat("中文", maxAutoMemorySearchQueryBytes)),
	})
	assert.LessOrEqual(t, len(query), maxAutoMemorySearchQueryBytes)
	assert.True(t, utf8.ValidString(query))
}

func TestStrictPolicy_ToolGatesAndUnknownOperations(t *testing.T) {
	oldTime := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	existing := &memory.Entry{
		ID: "visit",
		Memory: &memory.Memory{
			Memory:    "Alice visited Bob on December 1st, 2025.",
			Kind:      memory.KindEpisode,
			EventTime: &oldTime,
		},
	}
	add := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}

	allDisabled := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{},
	}, newMockOperator())
	out := appendStrictAdd(
		context.Background(), allDisabled, reconcileUserKey(), nil, add,
		[]*memory.Entry{existing},
	)
	require.Equal(t, []*extractor.Operation{add}, out)

	addOnly := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{memory.AddToolName: {}},
	}, newMockOperator())
	out = appendStrictAdd(
		context.Background(), addOnly, reconcileUserKey(), nil, add,
		[]*memory.Entry{existing},
	)
	require.Equal(t, []*extractor.Operation{add}, out)

	unknown := &extractor.Operation{Type: extractor.OperationType("unknown")}
	out = allDisabled.reconcileStrictOps(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{nil, unknown},
		[]*memory.Entry{nil, {}, {ID: "missing-memory"}}, nil,
	)
	require.Equal(t, []*extractor.Operation{unknown}, out)
}

func TestStrictPolicy_ModelUpdateDecisions(t *testing.T) {
	oldTime := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	existing := &memory.Entry{
		ID: "visit",
		Memory: &memory.Memory{
			Memory:    "Alice visited Bob on December 1st, 2025.",
			Kind:      memory.KindEpisode,
			EventTime: &oldTime,
		},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())

	duplicate := &extractor.Operation{
		Type:       extractor.OperationUpdate,
		MemoryID:   existing.ID,
		Memory:     existing.Memory.Memory,
		MemoryKind: memory.KindEpisode,
		EventTime:  &oldTime,
	}
	assert.Empty(t, appendStrictUpdate(
		context.Background(), worker, reconcileUserKey(), nil, duplicate, existing,
	))

	enrichment := &extractor.Operation{
		Type:       extractor.OperationUpdate,
		MemoryID:   existing.ID,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}
	out := appendStrictUpdate(
		context.Background(), worker, reconcileUserKey(), nil, enrichment, existing,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, existing.ID, out[0].MemoryID)

	updateDisabled := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{memory.AddToolName: {}},
	}, newMockOperator())
	out = appendStrictUpdate(
		context.Background(), updateDisabled, reconcileUserKey(), nil, enrichment, existing,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestStrictCandidateLess(t *testing.T) {
	entry := func(score float64) *memory.Entry {
		return &memory.Entry{Score: score}
	}
	tests := []struct {
		name  string
		left  *strictCandidate
		right *strictCandidate
		want  bool
	}{
		{
			name:  "duplicate wins",
			left:  &strictCandidate{entry: entry(1)},
			right: &strictCandidate{entry: entry(0), duplicate: true},
			want:  true,
		},
		{
			name:  "higher old coverage wins",
			left:  &strictCandidate{entry: entry(1), oldCoverage: 0.95},
			right: &strictCandidate{entry: entry(0), oldCoverage: 1},
			want:  true,
		},
		{
			name: "higher new coverage wins",
			left: &strictCandidate{
				entry: entry(1), oldCoverage: 1, newCoverage: 0.8,
			},
			right: &strictCandidate{
				entry: entry(0), oldCoverage: 1, newCoverage: 0.9,
			},
			want: true,
		},
		{
			name: "higher score wins",
			left: &strictCandidate{
				entry: entry(0.7), oldCoverage: 1, newCoverage: 1,
			},
			right: &strictCandidate{
				entry: entry(0.8), oldCoverage: 1, newCoverage: 1,
			},
			want: true,
		},
		{
			name:  "weaker candidate loses",
			left:  &strictCandidate{entry: entry(1), duplicate: true},
			right: &strictCandidate{entry: entry(0)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, strictCandidateLess(test.left, test.right))
		})
	}
}

func TestExactMemoryDuplicate_MetadataContract(t *testing.T) {
	eventTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	otherTime := eventTime.Add(time.Hour)
	stored := &memory.Memory{
		Memory:       "Alice visited Bob.",
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice", "Bob"},
		Location:     "Paris",
	}
	base := extractor.Operation{
		Memory:       " alice VISITED bob ",
		MemoryKind:   memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"bob", "alice"},
		Location:     " paris ",
	}
	assert.True(t, exactMemoryDuplicate(&base, stored))

	tests := []struct {
		name   string
		mutate func(*extractor.Operation)
	}{
		{name: "text", mutate: func(op *extractor.Operation) { op.Memory = "different" }},
		{name: "kind", mutate: func(op *extractor.Operation) { op.MemoryKind = memory.KindFact }},
		{name: "time", mutate: func(op *extractor.Operation) { op.EventTime = &otherTime }},
		{name: "participants", mutate: func(op *extractor.Operation) { op.Participants = []string{"Alice"} }},
		{name: "location", mutate: func(op *extractor.Operation) { op.Location = "London" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			op := base
			test.mutate(&op)
			assert.False(t, exactMemoryDuplicate(&op, stored))
		})
	}
}

func TestMetadataIdentityCompatible(t *testing.T) {
	eventTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	otherDay := eventTime.Add(24 * time.Hour)
	stored := &memory.Memory{
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice"},
		Location:     "Paris",
	}
	base := extractor.Operation{
		MemoryKind:   memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice", "Bob"},
		Location:     " paris ",
	}
	assert.True(t, metadataIdentityCompatible(&base, stored))

	tests := []struct {
		name   string
		mutate func(*extractor.Operation)
	}{
		{name: "kind", mutate: func(op *extractor.Operation) { op.MemoryKind = memory.KindFact }},
		{name: "event date", mutate: func(op *extractor.Operation) { op.EventTime = &otherDay }},
		{name: "participants", mutate: func(op *extractor.Operation) { op.Participants = []string{"Bob"} }},
		{name: "location", mutate: func(op *extractor.Operation) { op.Location = "London" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			op := base
			test.mutate(&op)
			assert.False(t, metadataIdentityCompatible(&op, stored))
		})
	}
}

func TestClassifyStrictCandidate_RejectsSemanticConflicts(t *testing.T) {
	entry := func(text string) *memory.Entry {
		return &memory.Entry{
			ID: "candidate",
			Memory: &memory.Memory{
				Memory: text,
				Kind:   memory.KindFact,
			},
		}
	}
	op := func(text string) *extractor.Operation {
		return &extractor.Operation{
			Type:       extractor.OperationAdd,
			Memory:     text,
			MemoryKind: memory.KindFact,
		}
	}
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "critical value format changed",
			old:  "Alice records the detailed family appointment at 4:00 in the shared calendar for everyone to review before the weekly planning meeting.",
			new:  "Alice records the detailed family appointment in the shared calendar at 4 00 for everyone to review before the weekly planning meeting.",
		},
		{
			name: "negation count changed",
			old:  "Alice is not available for the detailed family planning meeting in the shared office calendar this week.",
			new:  "Alice is not not available for the detailed family planning meeting in the shared office calendar this week.",
		},
		{
			name: "new state change marker",
			old:  "Alice stores the detailed family travel plans in the shared office cabinet for everyone to review before each meeting.",
			new:  "Alice now stores the detailed family travel plans in the shared office cabinet for everyone to review before each meeting.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Nil(t, classifyStrictCandidate(op(test.new), entry(test.old)))
		})
	}
}

func TestPolicyComparisonHelpers(t *testing.T) {
	eventTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	sameTime := eventTime
	otherTime := eventTime.Add(time.Hour)
	assert.True(t, equalOptionalTime(nil, nil))
	assert.False(t, equalOptionalTime(nil, &eventTime))
	assert.True(t, equalOptionalTime(&eventTime, &sameTime))
	assert.False(t, equalOptionalTime(&eventTime, &otherTime))

	assert.True(t, equalStringSet([]string{" Alice ", "BOB"}, []string{"bob", "alice"}))
	assert.False(t, equalStringSet([]string{"Alice"}, []string{"Alice", "Bob"}))
	assert.False(t, equalStringSet([]string{"Alice", "Bob"}, []string{"Alice", "Carol"}))
	assert.True(t, isStringSubset([]string{"Alice"}, []string{"Bob", "alice"}))
	assert.False(t, isStringSubset([]string{"Alice"}, []string{"Bob"}))

	oldCoverage, newCoverage := directionalTokenCoverage("", "new memory")
	assert.Zero(t, oldCoverage)
	assert.Zero(t, newCoverage)
	assert.True(t, criticalValuesPreserved("Meeting at 4:00 pm", "Meeting at 4:00 pm today"))
	assert.False(t, criticalValuesPreserved("Meeting at 4:00 pm", "Meeting today"))
	assert.Equal(t, "not|not", negationSignature("Not ready and NOT available"))

	assert.Equal(t, 1, utf8PrefixBoundary("a中", 2))
	assert.Equal(t, 3, utf8SuffixBoundary("中a", 1))
}

func TestExecuteOperation_ReturnsPersistenceErrors(t *testing.T) {
	tests := []struct {
		name      string
		op        *extractor.Operation
		configure func(*mockOperator)
		want      string
	}{
		{
			name: "add",
			op:   &extractor.Operation{Type: extractor.OperationAdd, Memory: "memory"},
			configure: func(operator *mockOperator) {
				operator.addErr = assert.AnError
			},
			want: "add memory",
		},
		{
			name: "update",
			op: &extractor.Operation{
				Type: extractor.OperationUpdate, MemoryID: "memory-id", Memory: "memory",
			},
			configure: func(operator *mockOperator) {
				operator.updateErr = assert.AnError
			},
			want: "update memory",
		},
		{
			name: "delete",
			op:   &extractor.Operation{Type: extractor.OperationDelete, MemoryID: "memory-id"},
			configure: func(operator *mockOperator) {
				operator.deleteErr = assert.AnError
			},
			want: "delete memory",
		},
		{
			name: "clear",
			op:   &extractor.Operation{Type: extractor.OperationClear},
			configure: func(operator *mockOperator) {
				operator.clearErr = assert.AnError
			},
			want: "clear memories",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operator := newMockOperator()
			test.configure(operator)
			worker := NewAutoMemoryWorker(AutoMemoryConfig{}, operator)
			err := worker.executeOperation(context.Background(), reconcileUserKey(), test.op)
			assert.ErrorContains(t, err, test.want)
			assert.ErrorIs(t, err, assert.AnError)
		})
	}
}

func TestExecuteOperation_DeleteRetryIsIdempotent(t *testing.T) {
	operator := newMockOperator()
	operator.deleteErr = errors.New("memory with id memory-id not found")
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, operator)
	err := worker.executeOperation(
		context.Background(),
		reconcileUserKey(),
		&extractor.Operation{Type: extractor.OperationDelete, MemoryID: "memory-id"},
	)
	assert.NoError(t, err)
}

func TestAutoMemoryWorker_CompatiblePersistenceFailureRemainsBestEffort(t *testing.T) {
	operator := newMockOperator()
	operator.addErr = assert.AnError
	ext := &mockExtractor{ops: []*extractor.Operation{
		{
			Type:   extractor.OperationAdd,
			Memory: "User likes tea.",
		},
		{
			Type:     extractor.OperationUpdate,
			MemoryID: "existing-memory",
			Memory:   "User likes green tea.",
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	sess := newTestSession("app", "u1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("I like tea."))

	require.NoError(t, worker.EnqueueJob(context.Background(), sess))
	_, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.True(t, ok)
	assert.Equal(t, 1, operator.updateCalls)
}

func TestAutoMemoryWorker_StrictPersistenceFailureDoesNotAdvanceWatermark(t *testing.T) {
	operator := newMockOperator()
	operator.addErr = assert.AnError
	ext := newExtractorWithOperation(t, extractor.UpdatePolicyStrict, &extractor.Operation{
		Type:   extractor.OperationAdd,
		Memory: "User likes tea.",
	})
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	sess := newTestSession("app", "u1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("I like tea."))

	err := worker.EnqueueJob(context.Background(), sess)
	require.Error(t, err)
	_, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.False(t, ok)

	operator.addErr = nil
	require.NoError(t, worker.EnqueueJob(context.Background(), sess))
	_, ok = sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.True(t, ok)
}
