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

type policyExtractor struct {
	*mockExtractor
	updatePolicy extractor.UpdatePolicy
}

func (e *policyExtractor) UpdatePolicy() extractor.UpdatePolicy {
	return e.updatePolicy
}

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

func TestConservativePolicy_AliceTimeEnrichmentUpdates(t *testing.T) {
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
	out := worker.reconcileConservativeOps(context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "alice-visit", out[0].MemoryID)
	assert.Equal(t, &newTime, out[0].EventTime)
}

func TestConservativePolicy_ExactDuplicateIgnoresTopicDrift(t *testing.T) {
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
	out := worker.reconcileConservativeOps(context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing)
	assert.Empty(t, out)
}

func TestConservativePolicy_ChangesRemainAdditive(t *testing.T) {
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
			out := worker.reconcileConservativeOps(
				context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing,
			)
			require.Len(t, out, 1)
			assert.Equal(t, extractor.OperationAdd, out[0].Type)
		})
	}
}

func TestConservativePolicy_DifferentEventDateRemainsAdditive(t *testing.T) {
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
	out := worker.reconcileConservativeOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
}

func TestConservativePolicy_UnsafeModelUpdateBecomesAdd(t *testing.T) {
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
	out := worker.reconcileConservativeOps(context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestDisabledPolicy_ConvertsUpdateToAdd(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	op := &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "old",
		Memory:   "new content",
	}
	out := worker.disableExtractedUpdates(context.Background(), reconcileUserKey(), []*extractor.Operation{op})
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestConservativePolicy_DoesNotSearchPerOperation(t *testing.T) {
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
	ext := &policyExtractor{
		mockExtractor: &mockExtractor{ops: []*extractor.Operation{{
			Type:       extractor.OperationAdd,
			Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
			MemoryKind: memory.KindFact,
		}}},
		updatePolicy: extractor.UpdatePolicyConservative,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	require.NoError(t, worker.createAutoMemory(context.Background(), reconcileUserKey(), []model.Message{
		model.NewUserMessage("Alice visited Bob at 4pm on December 1st, 2025."),
	}))
	assert.Equal(t, 1, operator.searchCalls)
	assert.Equal(t, 1, operator.updateCalls)
}

func TestUpdatePolicies_PreserveLegacySearchBehavior(t *testing.T) {
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
			name:   "legacy keeps per-add reconciliation",
			policy: extractor.UpdatePolicyLegacy,
			operation: &extractor.Operation{
				Type: extractor.OperationAdd, Memory: "User likes tea.",
			},
			searchCalls: 2,
		},
		{
			name:   "disabled converts update without reconciliation",
			policy: extractor.UpdatePolicyDisabled,
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
			ext := &policyExtractor{
				mockExtractor: &mockExtractor{ops: []*extractor.Operation{test.operation}},
				updatePolicy:  test.policy,
			}
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

func TestAutoMemoryWorker_LegacyPersistenceFailureRemainsBestEffort(t *testing.T) {
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

func TestAutoMemoryWorker_ConservativePersistenceFailureDoesNotAdvanceWatermark(t *testing.T) {
	operator := newMockOperator()
	operator.addErr = assert.AnError
	ext := &policyExtractor{
		mockExtractor: &mockExtractor{ops: []*extractor.Operation{{
			Type:   extractor.OperationAdd,
			Memory: "User likes tea.",
		}}},
		updatePolicy: extractor.UpdatePolicyConservative,
	}
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
