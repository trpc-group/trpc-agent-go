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
	searchCalls   int
	searchQueries []string
}

type failFirstAddOperator struct {
	*mockOperator
	attempts int
}

type customUpdatePolicyExtractor struct {
	*mockExtractor
	metadata map[string]any
}

func (e *customUpdatePolicyExtractor) Metadata() map[string]any {
	return e.metadata
}

type decoratedExtractor struct {
	extractor.MemoryExtractor
}

func (o *countingOperator) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	o.searchCalls++
	o.searchQueries = append(o.searchQueries, query)
	return o.mockOperator.SearchMemories(ctx, userKey, query, opts...)
}

func (o *failFirstAddOperator) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryText string,
	topics []string,
	opts ...memory.AddOption,
) error {
	o.attempts++
	if o.attempts == 1 {
		return assert.AnError
	}
	return o.mockOperator.AddMemory(ctx, userKey, memoryText, topics, opts...)
}

func newExtractorWithOperation(
	t *testing.T,
	policy extractor.UpdatePolicy,
	op *extractor.Operation,
) extractor.MemoryExtractor {
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

func TestUpdatePolicyFor_PreservesPolicyThroughDecorator(t *testing.T) {
	for _, policy := range []extractor.UpdatePolicy{
		extractor.UpdatePolicyMergeSimilar,
		extractor.UpdatePolicyPreserveHistory,
		extractor.UpdatePolicyAppendOnly,
	} {
		builtin := extractor.NewExtractor(
			nil,
			extractor.WithUpdatePolicy(policy),
		)
		assert.Equal(t, policy, updatePolicyFor(builtin))
		assert.Equal(t, policy, updatePolicyFor(&decoratedExtractor{
			MemoryExtractor: builtin,
		}))
	}
	assert.Equal(t, extractor.UpdatePolicyMergeSimilar, updatePolicyFor(nil))
	assert.Equal(t, extractor.UpdatePolicyMergeSimilar, updatePolicyFor(&mockExtractor{}))
	assert.Equal(t, extractor.UpdatePolicyMergeSimilar, updatePolicyFor(
		&customUpdatePolicyExtractor{mockExtractor: &mockExtractor{}},
	))
	assert.Equal(t, extractor.UpdatePolicyMergeSimilar, updatePolicyFor(
		&customUpdatePolicyExtractor{
			mockExtractor: &mockExtractor{},
			metadata: map[string]any{
				"update_policy": extractor.UpdatePolicyPreserveHistory,
			},
		},
	))
	assert.Equal(t, extractor.UpdatePolicyMergeSimilar, updatePolicyFor(
		&customUpdatePolicyExtractor{
			mockExtractor: &mockExtractor{},
			metadata: map[string]any{
				"trpc-agent-go/memory-extractor/update-policy": extractor.UpdatePolicyPreserveHistory,
			},
		},
	))
}

func TestUpdatePolicyMergeSimilar_KeepsOperationFailuresBestEffort(t *testing.T) {
	ext := &customUpdatePolicyExtractor{
		mockExtractor: &mockExtractor{ops: []*extractor.Operation{
			{Type: extractor.OperationAdd, Memory: "User likes tea."},
			{Type: extractor.OperationAdd, Memory: "User likes coffee."},
		}},
		metadata: map[string]any{},
	}
	operator := &failFirstAddOperator{mockOperator: newMockOperator()}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)

	err := worker.createAutoMemory(
		context.Background(),
		reconcileUserKey(),
		[]model.Message{model.NewUserMessage("I like tea and coffee.")},
	)

	require.NoError(t, err)
	assert.Equal(t, 2, operator.attempts)
	assert.Equal(t, 1, operator.addCalls)
}

func TestUpdatePolicies_PersistenceFailureRetriesSameEvents(t *testing.T) {
	for _, policy := range []extractor.UpdatePolicy{
		extractor.UpdatePolicyPreserveHistory,
		extractor.UpdatePolicyAppendOnly,
	} {
		t.Run(string(policy), func(t *testing.T) {
			ext := &customUpdatePolicyExtractor{
				mockExtractor: &mockExtractor{ops: []*extractor.Operation{{
					Type: extractor.OperationAdd, Memory: "User likes tea.",
				}}},
			}
			operator := &failFirstAddOperator{mockOperator: newMockOperator()}
			worker := NewAutoMemoryWorker(
				AutoMemoryConfig{Extractor: ext}, operator,
			)
			worker.updatePolicy = policy
			sess := newTestSession("app", "user")
			first := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
			appendSessionMessage(sess, first, model.NewUserMessage("I like tea."))

			require.Error(t, worker.EnqueueJob(context.Background(), sess))
			assert.True(t, readLastExtractAt(sess).IsZero())

			require.NoError(t, worker.EnqueueJob(context.Background(), sess))
			assert.True(t, readLastExtractAt(sess).Equal(first))
			assert.Equal(t, 2, operator.attempts)
			assert.Equal(t, 1, operator.addCalls)
		})
	}
}

func TestPreserveHistoryPolicy_AliceTimeEnrichmentUpdates(t *testing.T) {
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
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "alice-visit", out[0].MemoryID)
	assert.Equal(t, &newTime, out[0].EventTime)
}

func TestPreserveHistoryPolicy_ExactDuplicateIgnoresTopicDrift(t *testing.T) {
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
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	assert.Empty(t, out)
}

func TestPreserveHistoryPolicy_FiltersExactBatchDuplicate(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	first := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "User likes coffee.",
		Topics:     []string{"coffee"},
		MemoryKind: memory.KindFact,
	}
	last := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     " user likes COFFEE ",
		Topics:     []string{"coffee", "preference"},
		MemoryKind: memory.KindFact,
	}
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{first, last}, nil, nil,
	)
	require.Len(t, out, 1)
	assert.Same(t, first, out[0])
}

func TestPreserveHistoryPolicy_KeepsDistinctBatchAdds(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	ops := []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "Alice likes coffee."},
		{Type: extractor.OperationAdd, Memory: "Alice likes dark coffee."},
		{Type: extractor.OperationAdd, Memory: "Alice likes dark roast coffee."},
	}
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), ops, nil, nil,
	)
	require.Equal(t, ops, out)
}

func TestPreserveHistoryPolicy_ChangesRemainAdditive(t *testing.T) {
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
			out := worker.reconcilePreserveHistoryOps(
				context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
			)
			require.Len(t, out, 1)
			assert.Equal(t, extractor.OperationAdd, out[0].Type)
		})
	}
}

func TestPreserveHistoryPolicy_DifferentEventDateRemainsAdditive(t *testing.T) {
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
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
}

func TestPreserveHistoryPolicy_UnsafeModelUpdateBecomesAdd(t *testing.T) {
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
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestAppendOnlyPolicy_ConvertsUpdateToAdd(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	op := &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "old",
		Memory:   "new content",
	}
	out := worker.applyAppendOnlyPolicy(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestMergeSimilarPolicy_PreservesEveryOperationType(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyMergeSimilar)
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

func TestPreserveHistoryPolicy_OperationContract(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyPreserveHistory)
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

func TestAppendOnlyPolicy_OperationContract(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyAppendOnly)
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

func newPolicyWorker(policy extractor.UpdatePolicy) *AutoMemoryWorker {
	return NewAutoMemoryWorker(AutoMemoryConfig{
		Extractor: extractor.NewExtractor(
			nil,
			extractor.WithUpdatePolicy(policy),
		),
	}, newMockOperator())
}

func TestPreserveHistoryPolicy_DoesNotSearchPerOperation(t *testing.T) {
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
	ext := newExtractorWithOperation(t, extractor.UpdatePolicyPreserveHistory, &extractor.Operation{
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

func TestUpdatePolicies_SearchBehavior(t *testing.T) {
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
		searchQuery string
	}{
		{
			name:   "mergeSimilar keeps per-add reconciliation",
			policy: extractor.UpdatePolicyMergeSimilar,
			operation: &extractor.Operation{
				Type: extractor.OperationAdd, Memory: "User likes tea.",
			},
			searchCalls: 2,
			searchQuery: "I like tea.",
		},
		{
			name:   "preserve history includes assistant context",
			policy: extractor.UpdatePolicyPreserveHistory,
			operation: &extractor.Operation{
				Type: extractor.OperationAdd, Memory: "User likes coffee.",
			},
			searchCalls: 1,
			addCalls:    1,
			searchQuery: "I like tea. Assistant-only detail.",
		},
		{
			name:   "append-only includes assistant context",
			policy: extractor.UpdatePolicyAppendOnly,
			operation: &extractor.Operation{
				Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "User likes coffee.",
			},
			searchCalls: 1,
			addCalls:    1,
			searchQuery: "I like tea. Assistant-only detail.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseOperator := newMockOperator()
			baseOperator.searchResults = []*memory.Entry{existing}
			operator := &countingOperator{mockOperator: baseOperator}
			ext := &decoratedExtractor{MemoryExtractor: newExtractorWithOperation(
				t, test.policy, test.operation,
			)}
			worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
			require.NoError(t, worker.createAutoMemory(
				context.Background(),
				reconcileUserKey(),
				[]model.Message{
					model.NewUserMessage("I like tea."),
					model.NewAssistantMessage("Assistant-only detail."),
				},
			))
			assert.Equal(t, test.searchCalls, operator.searchCalls)
			assert.Equal(t, test.addCalls, operator.addCalls)
			require.NotEmpty(t, operator.searchQueries)
			assert.Equal(t, test.searchQuery, operator.searchQueries[0])
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
		model.NewUserMessage(strings.Repeat("history ", maxPolicySearchQueryBytes)),
		model.NewAssistantMessage(strings.Repeat("中文", maxPolicySearchQueryBytes)),
	})
	assert.LessOrEqual(t, len(query), maxPolicySearchQueryBytes)
	assert.True(t, utf8.ValidString(query))
}

func TestPreserveHistoryPolicy_ToolGatesAndUnknownOperations(t *testing.T) {
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
	out := appendPreserveHistoryAdd(
		context.Background(), allDisabled, reconcileUserKey(), nil, add,
		[]*memory.Entry{existing},
	)
	require.Equal(t, []*extractor.Operation{add}, out)

	appendOnly := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{memory.AddToolName: {}},
	}, newMockOperator())
	out = appendPreserveHistoryAdd(
		context.Background(), appendOnly, reconcileUserKey(), nil, add,
		[]*memory.Entry{existing},
	)
	require.Equal(t, []*extractor.Operation{add}, out)

	unknown := &extractor.Operation{Type: extractor.OperationType("unknown")}
	out = allDisabled.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{nil, unknown},
		[]*memory.Entry{nil, {}, {ID: "missing-memory"}}, nil,
	)
	require.Equal(t, []*extractor.Operation{unknown}, out)
}

func TestPreserveHistoryPolicy_ModelUpdateDecisions(t *testing.T) {
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
	assert.Empty(t, appendPreserveHistoryUpdate(
		context.Background(), worker, reconcileUserKey(), nil, duplicate, existing,
	))

	enrichment := &extractor.Operation{
		Type:       extractor.OperationUpdate,
		MemoryID:   existing.ID,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}
	out := appendPreserveHistoryUpdate(
		context.Background(), worker, reconcileUserKey(), nil, enrichment, existing,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, existing.ID, out[0].MemoryID)

	updateDisabled := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{memory.AddToolName: {}},
	}, newMockOperator())
	out = appendPreserveHistoryUpdate(
		context.Background(), updateDisabled, reconcileUserKey(), nil, enrichment, existing,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestPreserveHistoryCandidateLess(t *testing.T) {
	entry := func(score float64) *memory.Entry {
		return &memory.Entry{Score: score}
	}
	tests := []struct {
		name  string
		left  *preserveHistoryCandidate
		right *preserveHistoryCandidate
		want  bool
	}{
		{
			name:  "duplicate wins",
			left:  &preserveHistoryCandidate{entry: entry(1)},
			right: &preserveHistoryCandidate{entry: entry(0), duplicate: true},
			want:  true,
		},
		{
			name:  "higher old coverage wins",
			left:  &preserveHistoryCandidate{entry: entry(1), oldCoverage: 0.95},
			right: &preserveHistoryCandidate{entry: entry(0), oldCoverage: 1},
			want:  true,
		},
		{
			name: "higher new coverage wins",
			left: &preserveHistoryCandidate{
				entry: entry(1), oldCoverage: 1, newCoverage: 0.8,
			},
			right: &preserveHistoryCandidate{
				entry: entry(0), oldCoverage: 1, newCoverage: 0.9,
			},
			want: true,
		},
		{
			name: "higher score wins",
			left: &preserveHistoryCandidate{
				entry: entry(0.7), oldCoverage: 1, newCoverage: 1,
			},
			right: &preserveHistoryCandidate{
				entry: entry(0.8), oldCoverage: 1, newCoverage: 1,
			},
			want: true,
		},
		{
			name:  "weaker candidate loses",
			left:  &preserveHistoryCandidate{entry: entry(1), duplicate: true},
			right: &preserveHistoryCandidate{entry: entry(0)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, preserveHistoryCandidateLess(test.left, test.right))
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

func TestClassifyPreserveHistoryCandidate_RejectsSemanticConflicts(t *testing.T) {
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
			assert.Nil(t, classifyPreserveHistoryCandidate(op(test.new), entry(test.old)))
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
			name:     "partial clear does not authorize clear",
			messages: []model.Message{model.NewUserMessage("Clear everything except my coffee preference.")},
		},
		{
			name:     "partial chinese clear does not authorize clear",
			messages: []model.Message{model.NewUserMessage("请清空除了咖啡偏好以外的所有记忆。")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := latestExplicitDestructiveRequest(test.messages)
			assert.Equal(t, test.allowDelete, request.explicit && !request.partial)
			assert.Equal(t, test.allowClear, request.clearAll && !request.partial)
		})
	}
}

func TestPreserveHistoryPolicy_DeleteAuthorization(t *testing.T) {
	entry := func(id, text string, topics ...string) *memory.Entry {
		return &memory.Entry{
			ID: id,
			Memory: &memory.Memory{
				Memory: text,
				Topics: topics,
			},
		}
	}
	deleteOps := func(ids ...string) []*extractor.Operation {
		operations := make([]*extractor.Operation, 0, len(ids))
		for _, id := range ids {
			operations = append(operations, &extractor.Operation{
				Type:     extractor.OperationDelete,
				MemoryID: id,
			})
		}
		return operations
	}

	coffee := entry("coffee", "User prefers dark roast coffee.", "coffee", "preference", "咖啡", "偏好")
	address := entry("address", "User has a home address in Shenzhen.", "home", "address", "住址")
	office := entry("office", "User converted a bedroom into a home office.", "home", "office")
	employer := entry("employer", "User's former employer was Acme.", "former employer", "Acme")
	hobby := entry("hobby", "User enjoys hiking.", "hiking")

	tests := []struct {
		name       string
		request    string
		existing   []*memory.Entry
		operations []*extractor.Operation
		wantIDs    []string
	}{
		{
			name:       "target bound",
			request:    "Please forget my coffee preference.",
			existing:   []*memory.Entry{coffee, address},
			operations: deleteOps("coffee", "address", "missing"),
			wantIDs:    []string{"coffee"},
		},
		{
			name:       "partial target match",
			request:    "Please forget my home address.",
			existing:   []*memory.Entry{address, office},
			operations: deleteOps("address", "office"),
			wantIDs:    []string{"address"},
		},
		{
			name:       "chinese target",
			request:    "请删除我的咖啡偏好。",
			existing:   []*memory.Entry{coffee, address},
			operations: deleteOps("coffee", "address"),
			wantIDs:    []string{"coffee"},
		},
		{
			name:     "scoped everything",
			request:  "Please forget everything about my former employer.",
			existing: []*memory.Entry{employer, hobby},
			operations: append(
				deleteOps("employer", "hobby"),
				&extractor.Operation{Type: extractor.OperationClear},
			),
			wantIDs: []string{"employer"},
		},
		{
			name:       "global clear authorizes delete",
			request:    "Please clear all my memories.",
			existing:   []*memory.Entry{coffee},
			operations: deleteOps("coffee"),
			wantIDs:    []string{"coffee"},
		},
		{
			name:     "qualified clear stays scoped",
			request:  "Please forget everything I told you about coffee.",
			existing: []*memory.Entry{coffee, address},
			operations: append(
				deleteOps("coffee", "address"),
				&extractor.Operation{Type: extractor.OperationClear},
			),
			wantIDs: []string{"coffee"},
		},
		{
			name:     "qualified chinese clear stays scoped",
			request:  "请删除关于咖啡的所有记忆。",
			existing: []*memory.Entry{coffee, address},
			operations: append(
				deleteOps("coffee", "address"),
				&extractor.Operation{Type: extractor.OperationClear},
			),
			wantIDs: []string{"coffee"},
		},
	}

	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := worker.reconcilePreserveHistoryOps(
				context.Background(),
				reconcileUserKey(),
				test.operations,
				test.existing,
				[]model.Message{model.NewUserMessage(test.request)},
			)
			ids := make([]string, 0, len(out))
			for _, operation := range out {
				assert.Equal(t, extractor.OperationDelete, operation.Type)
				ids = append(ids, operation.MemoryID)
			}
			assert.Equal(t, test.wantIDs, ids)
		})
	}
}
