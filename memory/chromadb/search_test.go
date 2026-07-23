//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestServiceSearchMemoriesUsesCosineScoreAndThreshold(t *testing.T) {
	embedder := &testEmbedder{
		dimension: 2,
		values: map[string][]float64{
			"alpha": {1, 0},
			"beta":  {0.8, 0.6},
			"gamma": {-1, 0},
			"query": {1, 0},
		},
	}
	service, _ := newTestChromaService(t, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	for _, content := range []string{"alpha", "beta", "gamma"} {
		require.NoError(t, service.AddMemory(ctx, userKey, content, nil))
	}

	results, err := service.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "alpha", results[0].Memory.Memory)
	assert.InDelta(t, 1, results[0].Score, 0.0001)
	assert.Equal(t, "beta", results[1].Memory.Memory)
	assert.InDelta(t, 0.8, results[1].Score, 0.0001)

	results, err = service.SearchMemories(
		ctx,
		userKey,
		"query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "query", SimilarityThreshold: 0.9,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "alpha", results[0].Memory.Memory)
}

func TestServiceSearchMemoriesKindFallback(t *testing.T) {
	embedder := &testEmbedder{
		dimension: 2,
		values: map[string][]float64{
			"episode": {1, 0},
			"fact-a":  {0.9, 0.43589},
			"fact-b":  {0.8, 0.6},
			"query":   {1, 0},
		},
	}
	service, _ := newTestChromaService(t, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := time.Now().UTC()
	require.NoError(t, service.AddMemory(
		ctx,
		userKey,
		"episode",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind: memory.KindEpisode, EventTime: &eventTime,
		}),
	))
	require.NoError(t, service.AddMemory(ctx, userKey, "fact-a", nil))
	require.NoError(t, service.AddMemory(ctx, userKey, "fact-b", nil))

	results, err := service.SearchMemories(
		ctx,
		userKey,
		"query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "query", Kind: memory.KindEpisode, KindFallback: true, MaxResults: 3,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, memory.KindEpisode, results[0].Memory.Kind)
	assert.Equal(t, "episode", results[0].Memory.Memory)
	assert.Equal(t, 4, embedder.callCount())
}

func TestServiceSearchMemoriesTimeFilterIncludesFacts(t *testing.T) {
	embedder := &testEmbedder{dimension: 2}
	service, _ := newTestChromaService(t, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	boundary := time.Date(2025, 10, 1, 12, 0, 0, 0, time.UTC)
	early := boundary.Add(-time.Hour)
	late := boundary.Add(time.Hour)
	require.NoError(t, service.AddMemory(ctx, userKey, "stable fact", nil))
	require.NoError(t, service.AddMemory(
		ctx, userKey, "early event", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &early}),
	))
	require.NoError(t, service.AddMemory(
		ctx, userKey, "late event", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &late}),
	))

	results, err := service.SearchMemories(
		ctx,
		userKey,
		"query",
		memory.WithSearchOptions(memory.SearchOptions{Query: "query", TimeAfter: &boundary}),
	)
	require.NoError(t, err)
	contents := resultContents(results)
	assert.Contains(t, contents, "stable fact")
	assert.Contains(t, contents, "late event")
	assert.NotContains(t, contents, "early event")
}

func TestServiceSearchMemoriesHybridAddsExactKeywordMatch(t *testing.T) {
	embedder := &testEmbedder{
		dimension: 2,
		values: map[string][]float64{
			"general memory":       {1, 0},
			"device code is ZX-42": {-1, 0},
			"ZX-42":                {1, 0},
		},
	}
	service, _ := newTestChromaService(t, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "general memory", nil))
	require.NoError(t, service.AddMemory(ctx, userKey, "device code is ZX-42", nil))

	results, err := service.SearchMemories(
		ctx,
		userKey,
		"ZX-42",
		memory.WithSearchOptions(memory.SearchOptions{Query: "ZX-42", HybridSearch: true}),
	)
	require.NoError(t, err)
	contents := resultContents(results)
	assert.Contains(t, contents, "general memory")
	assert.Contains(t, contents, "device code is ZX-42")
	for _, result := range results {
		assert.Greater(t, result.Score, 0.0)
		assert.Less(t, result.Score, 0.1)
	}
}

func TestServiceSearchMemoriesDeduplicatesContent(t *testing.T) {
	embedder := &testEmbedder{dimension: 2}
	service, _ := newTestChromaService(t, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "Alice likes coffee every morning", nil))
	require.NoError(t, service.AddMemory(ctx, userKey, "Alice likes coffee every morning.", nil))

	results, err := service.SearchMemories(
		ctx,
		userKey,
		"coffee",
		memory.WithSearchOptions(memory.SearchOptions{Query: "coffee", Deduplicate: true}),
	)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestServiceSearchMemoriesHybridCandidateLimitIsIndependent(t *testing.T) {
	embedder := &testEmbedder{
		dimension: 2,
		values: map[string][]float64{
			"code ZX-42 first":  {-1, 0},
			"code ZX-42 second": {-1, 0},
			"ZX-42":             {1, 0},
		},
	}
	service, _ := newTestChromaService(
		t,
		embedder,
		WithMemoryLimit(10),
		WithHybridCandidateLimit(1),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "code ZX-42 first", nil))
	require.NoError(t, service.AddMemory(ctx, userKey, "code ZX-42 second", nil))

	results, err := service.SearchMemories(
		ctx,
		userKey,
		"ZX-42",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "ZX-42", HybridSearch: true, MaxResults: 10,
		}),
	)

	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestServiceSearchMemoriesReturnsDenseWhenKeywordReadFails(t *testing.T) {
	embedder := &testEmbedder{dimension: 2}
	service, fake := newTestChromaService(t, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "dense result", nil))
	fake.status["get"] = 500

	results, err := service.SearchMemories(
		ctx,
		userKey,
		"dense",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "dense", HybridSearch: true,
		}),
	)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "dense result", results[0].Memory.Memory)
}

func TestServiceSearchMemoriesReturnsDenseError(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 2})
	fake.status["query"] = 400

	results, err := service.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		"query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "query", HybridSearch: true,
		}),
	)

	assert.Nil(t, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestDecodeQueryResponseRejectsMalformedColumns(t *testing.T) {
	document := "memory"
	distance := float32(0.2)
	documents := [][]*string{{&document}}
	metadatas := [][]map[string]any{{}}
	distances := [][]*float32{{&distance}}
	tests := []struct {
		name     string
		response *queryRecordsResponse
		match    string
	}{
		{name: "nil", response: nil, match: "nil response"},
		{
			name: "missing batches",
			response: &queryRecordsResponse{
				IDs: responseField[[][]string]{
					value: [][]string{{"id"}}, present: true,
				},
				Documents: &documents,
				Metadatas: &metadatas, Distances: &distances,
			},
			match: "column length mismatch",
		},
		{
			name: "missing distance",
			response: &queryRecordsResponse{
				IDs: responseField[[][]string]{
					value: [][]string{{"id"}}, present: true,
				},
				Documents: &documents,
				Metadatas: &[][]map[string]any{{validTestMetadata()}},
				Distances: &[][]*float32{{nil}},
			},
			match: "has no distance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeQueryResponse(tt.response)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

func TestClampScore(t *testing.T) {
	assert.Equal(t, 0.0, clampScore(-0.5))
	assert.Equal(t, 0.5, clampScore(0.5))
	assert.Equal(t, 1.0, clampScore(1.5))
}

func resultContents(entries []*memory.Entry) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Memory.Memory)
	}
	return result
}

func validTestMetadata() map[string]any {
	now := time.Now().UTC().UnixNano()
	return map[string]any{
		metadataSchemaVersionKey: schemaVersion,
		metadataAppNameKey:       "app",
		metadataUserIDKey:        "user",
		metadataKindKey:          string(memory.KindFact),
		metadataHasEventTimeKey:  false,
		metadataCreatedAtKey:     now,
		metadataUpdatedAtKey:     now,
		metadataDeletedAtKey:     notDeletedAtNS,
	}
}
