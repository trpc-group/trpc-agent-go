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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func TestUpdatePolicyFromMetadata(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want extractor.UpdatePolicy
	}{
		{name: "missing", want: extractor.UpdatePolicyReconcile},
		{name: "reconcile", raw: "reconcile", want: extractor.UpdatePolicyReconcile},
		{name: "typed add only", raw: extractor.UpdatePolicyAddOnly, want: extractor.UpdatePolicyAddOnly},
		{name: "removed history policy", raw: "history-preserving", want: extractor.UpdatePolicyReconcile},
		{name: "unknown", raw: "custom", want: extractor.UpdatePolicyReconcile},
		{name: "wrong type", raw: 42, want: extractor.UpdatePolicyReconcile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := map[string]any{}
			if tt.raw != nil {
				metadata[extractorMetadataUpdatePolicy] = tt.raw
			}
			ext := &mockExtractor{metadata: metadata}
			assert.Equal(t, tt.want, updatePolicyFromMetadata(ext))
			worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, nil)
			assert.Equal(t, tt.want, worker.updatePolicy)
		})
	}
	assert.Equal(t, extractor.UpdatePolicyReconcile, updatePolicyFromMetadata(nil))
}

func TestAssistantResultPolicyPreservesDistinctResult(t *testing.T) {
	stored := []*memory.Entry{{
		ID: "tips",
		Memory: &memory.Memory{
			Memory: "Tips: learn Ruby, Python, and PHP.",
		},
		Score: 0.8,
	}}
	incoming := []*extractor.Operation{{
		Type:   extractor.OperationAdd,
		Memory: "Resources: Codecademy, FreeCodeCamp, and The Odin Project.",
	}}
	op := newMockOperator()
	op.searchResults = stored
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, op)

	ordinary := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), incoming, stored,
	)
	require.Len(t, ordinary, 1)
	assert.Equal(t, extractor.OperationUpdate, ordinary[0].Type)
	assert.Equal(t, "tips", ordinary[0].MemoryID)

	assistantResult := worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(), incoming, stored,
	)
	require.Len(t, assistantResult, 1)
	assert.Equal(t, extractor.OperationAdd, assistantResult[0].Type)
	assert.Empty(t, assistantResult[0].MemoryID)

	assert.Nil(t, worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(), nil, stored,
	))
	worker.updatePolicy = extractor.UpdatePolicyAddOnly
	assistantResult = worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(), []*extractor.Operation{{
			Type:     extractor.OperationUpdate,
			MemoryID: "tips",
			Memory:   "Updated recommendation.",
		}}, stored,
	)
	require.Len(t, assistantResult, 1)
	assert.Equal(t, extractor.OperationAdd, assistantResult[0].Type)
	assert.Empty(t, assistantResult[0].MemoryID)
}

func TestAssistantResultPolicy_StrictEnrichmentUpdates(t *testing.T) {
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
	in := []*extractor.Operation{{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		Topics:     []string{"Alice", "Bob", "visit", "time"},
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())

	out := worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(), in, existing,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "alice-visit", out[0].MemoryID)
	assert.Equal(t, &newTime, out[0].EventTime)
	assert.Contains(t, out[0].Topics, "time")
}

func TestAssistantResultPolicy_ChangedStateRemainsAdditive(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "job",
		Memory: &memory.Memory{
			Memory: "Works at Acme as an engineer.",
			Kind:   memory.KindFact,
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())

	for _, operationType := range []extractor.OperationType{
		extractor.OperationAdd,
		extractor.OperationUpdate,
	} {
		t.Run(string(operationType), func(t *testing.T) {
			memoryID := ""
			if operationType == extractor.OperationUpdate {
				memoryID = "job"
			}
			in := []*extractor.Operation{{
				Type:       operationType,
				MemoryID:   memoryID,
				Memory:     "Now works at Globex as an engineer.",
				MemoryKind: memory.KindFact,
			}}
			out := worker.applyAssistantResultPolicy(
				context.Background(), reconcileUserKey(), in, existing,
			)
			require.Len(t, out, 1)
			assert.Equal(t, extractor.OperationAdd, out[0].Type)
			assert.Empty(t, out[0].MemoryID)
		})
	}
}

func TestAssistantResultPolicy_ExactDuplicateIsNoOp(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "coffee",
		Memory: &memory.Memory{
			Memory: "Likes coffee.",
			Kind:   memory.KindFact,
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{{Type: extractor.OperationAdd, Memory: " LIKES coffee "}},
		existing,
	)
	assert.Empty(t, out)
}

func TestAssistantResultPolicy_UpdateOperations(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "trip",
		Memory: &memory.Memory{
			Memory: "Alice visited Paris in May.",
			Kind:   memory.KindFact,
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())

	duplicate := worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{{
			Type:     extractor.OperationUpdate,
			MemoryID: "trip",
			Memory:   "Alice visited Paris in May.",
		}}, existing,
	)
	assert.Empty(t, duplicate)

	enrichment := worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{{
			Type:     extractor.OperationUpdate,
			MemoryID: "trip",
			Memory:   "Alice visited Paris in May 2025.",
		}}, existing,
	)
	require.Len(t, enrichment, 1)
	assert.Equal(t, extractor.OperationUpdate, enrichment[0].Type)
	assert.Equal(t, "trip", enrichment[0].MemoryID)
}

func TestAssistantResultCandidateLess(t *testing.T) {
	entry := func(score float64) *memory.Entry {
		return &memory.Entry{Score: score}
	}
	assert.True(t, assistantResultCandidateLess(
		&assistantResultCandidate{}, &assistantResultCandidate{duplicate: true},
	))
	assert.True(t, assistantResultCandidateLess(
		&assistantResultCandidate{oldCoverage: 0.8},
		&assistantResultCandidate{oldCoverage: 0.9},
	))
	assert.True(t, assistantResultCandidateLess(
		&assistantResultCandidate{oldCoverage: 0.9, newCoverage: 0.8},
		&assistantResultCandidate{oldCoverage: 0.9, newCoverage: 0.9},
	))
	assert.True(t, assistantResultCandidateLess(
		&assistantResultCandidate{oldCoverage: 0.9, newCoverage: 0.9, entry: entry(0.7)},
		&assistantResultCandidate{oldCoverage: 0.9, newCoverage: 0.9, entry: entry(0.8)},
	))
}

func TestMetadataIdentityCompatibleParticipantSubset(t *testing.T) {
	entry := &memory.Entry{Memory: &memory.Memory{
		Memory:       "Alice met Bob.",
		Kind:         memory.KindFact,
		Participants: []string{"Alice"},
	}}
	assert.True(t, metadataIdentityCompatible(
		&extractor.Operation{
			Memory:       "Alice met Bob in Paris.",
			MemoryKind:   memory.KindFact,
			Participants: []string{"Alice", "Bob"},
		},
		entry.Memory,
	))
	assert.False(t, metadataIdentityCompatible(
		&extractor.Operation{
			Memory:       "Bob met Carol.",
			MemoryKind:   memory.KindFact,
			Participants: []string{"Bob", "Carol"},
		},
		entry.Memory,
	))
}

func TestAddOnlyPolicy_EnforcesAllowedOperationsAndDeduplicates(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "coffee",
		Memory: &memory.Memory{
			Memory: "Likes coffee.",
			Kind:   memory.KindFact,
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	worker.updatePolicy = extractor.UpdatePolicyAddOnly
	in := []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "likes COFFEE"},
		{Type: extractor.OperationUpdate, MemoryID: "job", Memory: "Works at Globex", Topics: []string{"work"}},
		{Type: extractor.OperationDelete, MemoryID: "coffee"},
		{Type: extractor.OperationClear},
		{Type: extractor.OperationAdd, Memory: "Enjoys hiking", Topics: []string{"hiking"}},
		{Type: extractor.OperationAdd, Memory: "Enjoys hiking", Topics: []string{"duplicate topic drift"}},
	}

	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), in, existing,
	)
	require.Len(t, out, 2)
	for _, op := range out {
		assert.Equal(t, extractor.OperationAdd, op.Type)
		assert.Empty(t, op.MemoryID)
	}
	assert.Equal(t, "Works at Globex", out[0].Memory)
	assert.Equal(t, []string{"work"}, out[0].Topics)
	assert.Equal(t, "Enjoys hiking", out[1].Memory)
}
