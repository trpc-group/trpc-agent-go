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
	"fmt"
	"sync"
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

func TestServiceSearchMemoriesRejectsOutOfRangeTimeFilters(t *testing.T) {
	beforeMinimum := time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)
	afterMaximum := time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		opts memory.SearchOptions
	}{
		{
			name: "time after before minimum",
			opts: memory.SearchOptions{Query: "query", TimeAfter: &beforeMinimum},
		},
		{
			name: "time before after maximum",
			opts: memory.SearchOptions{Query: "query", TimeBefore: &afterMaximum},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _ := newTestChromaService(t, &testEmbedder{dimension: 2})

			results, err := service.SearchMemories(
				context.Background(),
				memory.UserKey{AppName: "app", UserID: "user"},
				"query",
				memory.WithSearchOptions(tt.opts),
			)

			require.Error(t, err)
			assert.Nil(t, results)
		})
	}
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

func TestServiceSearchMemoriesHybridKeepsDenseCandidatesBelowCosineThreshold(t *testing.T) {
	embedder := &testEmbedder{
		dimension: 2,
		values: map[string][]float64{
			"general memory":       {0.2, 0.979795897},
			"device code is ZX-42": {1, 0},
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
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "ZX-42", HybridSearch: true, SimilarityThreshold: 0.9,
			MaxResults: 10,
		}),
	)

	require.NoError(t, err)
	contents := resultContents(results)
	assert.Contains(t, contents, "device code is ZX-42")
	assert.Contains(t, contents, "general memory")
}

func TestServiceSearchMemoriesSerializesHybridPaginationWithLocalDelete(t *testing.T) {
	service, fake := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithHybridCandidateLimit(defaultReadPageSize+1),
	)
	scope := recordScope{appName: "app", userID: "user"}
	const recordCount = defaultReadPageSize + 1
	lastID := fmt.Sprintf("id-%03d", recordCount-1)
	for i := 0; i < recordCount; i++ {
		content := fmt.Sprintf("ordinary memory %03d", i)
		if i == recordCount-1 {
			content = "device code is ZX-42"
		}
		record := newAddRecord(
			scope,
			content,
			nil,
			nil,
			time.Unix(int64(i), 0).UTC(),
		)
		record.entry.ID = fmt.Sprintf("id-%03d", i)
		putFakeRecord(fake, record)
	}
	fake.mu.Lock()
	fake.records[lastID].embedding = []float32{-1, 0, 0}
	fake.mu.Unlock()
	secondPageStarted := make(chan struct{})
	releaseSecondPage := make(chan struct{})
	var blockOnce sync.Once
	fake.getHook = func(request getRecordsRequest) {
		if len(request.IDs) > 0 || request.Offset == nil ||
			*request.Offset != defaultReadPageSize {
			return
		}
		blockOnce.Do(func() {
			close(secondPageStarted)
			<-releaseSecondPage
		})
	}
	type searchResult struct {
		entries []*memory.Entry
		err     error
	}
	searchDone := make(chan searchResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		entries, err := service.SearchMemories(
			ctx,
			memory.UserKey{AppName: scope.appName, UserID: scope.userID},
			"ZX-42",
			memory.WithSearchOptions(memory.SearchOptions{
				Query: "ZX-42", HybridSearch: true, MaxResults: 2,
			}),
		)
		searchDone <- searchResult{entries: entries, err: err}
	}()
	select {
	case <-secondPageStarted:
	case <-ctx.Done():
		t.Fatal("second hybrid-search page did not start")
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseSecondPage)
		})
	}
	defer release()
	lock := service.writeLock(scope)
	searchHoldsLock := !lock.TryLock()
	if !searchHoldsLock {
		lock.Unlock()
	}
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- service.DeleteMemory(ctx, memory.Key{
			AppName: scope.appName, UserID: scope.userID, MemoryID: "id-000",
		})
	}()
	var result searchResult
	var deleteErr error
	if searchHoldsLock {
		release()
		result = <-searchDone
		deleteErr = <-deleteDone
	} else {
		deleteErr = <-deleteDone
		release()
		result = <-searchDone
	}

	require.NoError(t, deleteErr)
	require.NoError(t, result.err)
	assert.Contains(t, resultContents(result.entries), "device code is ZX-42")
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
