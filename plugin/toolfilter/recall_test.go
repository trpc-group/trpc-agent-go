//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type controlledEmbedder struct {
	mu      sync.Mutex
	calls   map[string]int
	started chan string
	release chan struct{}
}

type uncooperativeEmbedder struct {
	started chan struct{}
	release chan struct{}
}

func (e *uncooperativeEmbedder) GetEmbedding(_ context.Context, _ string) ([]float64, error) {
	close(e.started)
	<-e.release
	return []float64{1, 2}, nil
}

func TestEmbedCache_TimeoutStopsWaitingWithoutReleasingPermit(t *testing.T) {
	e := &uncooperativeEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	cache := newEmbedCache(e)
	done := make(chan []float64, 1)
	go func() { done <- cache.vector(context.Background(), "stuck") }()
	<-e.started
	select {
	case result := <-done:
		require.Nil(t, result)
	case <-time.After(embeddingTimeout + 5*time.Second):
		close(e.release)
		t.Fatal("caller did not stop waiting after embedding timeout")
	}
	require.Len(t, cache.permits, 1)
	close(e.release)
	require.Eventually(t, func() bool { return len(cache.permits) == 0 }, time.Second, time.Millisecond)
	require.Equal(t, []float64{1, 2}, cache.vector(context.Background(), "stuck"))
}

func (e *controlledEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.mu.Lock()
	e.calls[text]++
	e.mu.Unlock()
	e.started <- text
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.release:
		return []float64{1, 2}, nil
	}
}

func TestEmbedCache_CanceledWaiterDoesNotCancelSharedWork(t *testing.T) {
	e := &controlledEmbedder{calls: make(map[string]int), started: make(chan string, 4), release: make(chan struct{})}
	cache := newEmbedCache(e)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan []float64, 1)
	go func() { first <- cache.vector(ctx, "same") }()
	<-e.started
	cancel()
	select {
	case got := <-first:
		require.Nil(t, got)
	case <-time.After(time.Second):
		t.Fatal("canceled caller did not stop waiting")
	}
	second := make(chan []float64, 1)
	go func() { second <- cache.vector(context.Background(), "same") }()
	other := make(chan []float64, 1)
	go func() { other <- cache.vector(context.Background(), "other") }()
	select {
	case text := <-e.started:
		require.Equal(t, "other", text)
	case <-time.After(time.Second):
		t.Fatal("different text did not execute concurrently")
	}
	close(e.release)
	require.Equal(t, []float64{1, 2}, <-second)
	require.Equal(t, []float64{1, 2}, <-other)
	e.mu.Lock()
	defer e.mu.Unlock()
	require.Equal(t, 1, e.calls["same"])
}

func TestTokenize(t *testing.T) {
	t.Parallel()

	tokens := tokenize("Find Cheap_Flights2 你好世界 카나다 한글!")
	require.Equal(t, map[string]struct{}{
		"find":           {},
		"cheap_flights2": {},
		"你":              {},
		"好":              {},
		"世":              {},
		"界":              {},
		"카":              {},
		"나":              {},
		"다":              {},
		"한":              {},
		"글":              {},
	}, tokens)
}

func TestLexicalScore_SubstringAndDistinctTokens(t *testing.T) {
	t.Parallel()
	user := tokenize("search search")
	require.Equal(t, 1.0, lexicalScore(user, "websearch", "engine"))
}

func TestLexicalScore_NameAndDescriptionCountOnce(t *testing.T) {
	t.Parallel()

	user := tokenize("flights")
	nameOnly := lexicalScore(user, "flights", "nothing matches here")
	descOnly := lexicalScore(user, "no match", "flights")
	require.InDelta(t, 1.0, nameOnly, 1e-9)
	require.InDelta(t, 1.0, descOnly, 1e-9)
	require.InDelta(t, 0.0, lexicalScore(user, "calendar", "events"), 1e-9)
}

func TestRecallRank_DeterministicOrderAndAbort(t *testing.T) {
	t.Parallel()

	candidates := []scoredCandidate{
		{name: "beta", description: "book flights"},
		{name: "alpha", description: "book flights"},
		{name: "gamma", description: "calendar events"},
	}

	// Tied scores fall back to name order.
	ranked := recallRank(context.Background(), "book flights", candidates, nil)
	require.Equal(t, "alpha", ranked[0].name)
	require.Equal(t, "beta", ranked[1].name)
	require.Equal(t, "gamma", ranked[2].name)

	// No user tokens: no recall at all. Tokens that overlap nothing rank
	// every candidate at zero, which the caller treats as abort.
	require.Empty(t, recallRank(context.Background(), "", candidates, nil))
	ranked = recallRank(context.Background(), "你好", candidates, nil)
	require.Equal(t, 0.0, ranked[0].score)
}

func TestRecallRank_EmbeddingFusion(t *testing.T) {
	t.Parallel()

	// Keyword order: alpha, beta, delta, gamma (gamma and delta tie at 0,
	// delta wins by name). Vector order: gamma, alpha, beta, delta. RRF
	// fuses the two so gamma — keyword rank 4, vector rank 1 — beats beta.
	embeddings := map[string][]float64{
		"book flights now":       {1, 0},
		"alpha\nflights now":     {0.9, 0.2},
		"beta\nbook":             {0.5, 0.8},
		"gamma\nzzz unrelated":   {1, 0.05},
		"delta\ncalendar events": {0.05, 1},
	}
	cache := newEmbedCache(&stubEmbedder{vectors: embeddings})

	candidates := []scoredCandidate{
		{name: "alpha", description: "flights now"},
		{name: "beta", description: "book"},
		{name: "gamma", description: "zzz unrelated"},
		{name: "delta", description: "calendar events"},
	}

	keywordOnly := recallRank(context.Background(), "book flights now", candidates, nil)
	require.Equal(t, "beta", keywordOnly[1].name)

	fused := recallRank(context.Background(), "book flights now", candidates, cache)
	require.Equal(t, "alpha", fused[0].name)
	require.Equal(t, "gamma", fused[1].name)
}

func TestRecallRank_EmbeddingWithoutKeywordTokens(t *testing.T) {
	candidates := []scoredCandidate{
		{name: "alpha", description: "first"},
		{name: "beta", description: "second"},
		{name: "missing", description: "no vector"},
	}
	for _, query := range []string{"مرحبا", "привет", "x"} {
		t.Run(query, func(t *testing.T) {
			emb := &stubEmbedder{vectors: map[string][]float64{
				query: {1, 0}, "alpha\nfirst": {0, 1}, "beta\nsecond": {1, 0}, "missing\nno vector": {0, -1},
			}}
			ranked := recallRank(context.Background(), query, candidates, newEmbedCache(emb))
			require.Len(t, ranked, 3)
			require.Equal(t, "beta", ranked[0].name)
			require.Greater(t, ranked[0].score, ranked[1].score)
			require.Positive(t, ranked[1].score)
			delete(emb.vectors, "missing\nno vector")
			require.Empty(t, recallRank(context.Background(), query, candidates, newEmbedCache(emb)))
		})
	}
	require.Empty(t, recallRank(context.Background(), "x", candidates, nil))
	require.Empty(t, recallRank(context.Background(), "x", candidates,
		newEmbedCache(&stubEmbedder{err: errors.New("embedding failed")})))
}

func TestRecallRank_BoundsConcurrentCandidateEmbeddings(t *testing.T) {
	e := &controlledEmbedder{calls: make(map[string]int), started: make(chan string, 64), release: make(chan struct{})}
	cache := newEmbedCache(e)
	cache.users.set("query", []float64{1, 2})
	candidates := make([]scoredCandidate, 24)
	for i := range candidates {
		candidates[i] = scoredCandidate{name: fmt.Sprintf("candidate %02d", i)}
	}
	done := make(chan []scoredCandidate, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- recallRank(context.Background(), "query", candidates, cache) }()
	}
	defer func() {
		select {
		case <-e.release:
		default:
			close(e.release)
		}
	}()
	for i := 0; i < 8; i++ {
		select {
		case <-e.started:
		case <-time.After(2 * time.Second):
			t.Fatal("candidate embeddings did not run concurrently")
		}
	}
	select {
	case <-e.started:
		t.Fatal("embedding concurrency exceeded the per-filter limit")
	case <-time.After(100 * time.Millisecond):
	}
	close(e.release)
	for i := 0; i < 2; i++ {
		select {
		case ranked := <-done:
			require.Len(t, ranked, len(candidates))
		case <-time.After(2 * time.Second):
			t.Fatal("recall did not complete")
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	require.Len(t, e.calls, len(candidates))
	for _, count := range e.calls {
		require.Equal(t, 1, count)
	}
}

func TestRecallRank_CancellationStopsWaitingForCandidates(t *testing.T) {
	e := &controlledEmbedder{calls: make(map[string]int), started: make(chan string, 32), release: make(chan struct{})}
	cache := newEmbedCache(e)
	cache.users.set("query", []float64{1, 2})
	candidates := make([]scoredCandidate, 24)
	for i := range candidates {
		candidates[i] = scoredCandidate{name: fmt.Sprintf("candidate %02d", i)}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(e.release)
	done := make(chan struct{})
	go func() {
		recallRank(ctx, "query", candidates, cache)
		close(done)
	}()
	for i := 0; i < 8; i++ {
		select {
		case <-e.started:
		case <-time.After(2 * time.Second):
			t.Fatal("candidate embedding did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recall did not stop waiting after cancellation")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	require.Len(t, e.calls, 8)
}

func TestEmbedCache_FailsOpenAndCaches(t *testing.T) {
	t.Parallel()

	stub := &stubEmbedder{vectors: map[string][]float64{"cached": {1, 1}}}
	cache := newEmbedCache(stub)

	require.NotNil(t, cache.vector(context.Background(), "cached"))
	require.Equal(t, 1, stub.calls()) // second read hits the cache
	require.NotNil(t, cache.vector(context.Background(), "cached"))
	require.Equal(t, 1, stub.calls())

	stub.err = errors.New("embedding failed")
	require.Nil(t, cache.vector(context.Background(), "broken"))
	// Failed embeddings are not cached: the next call retries.
	require.Nil(t, cache.vector(context.Background(), "broken"))
	require.Equal(t, 3, stub.calls())
}

func TestEmbedCache_RejectsMissWhenFull(t *testing.T) {
	e := &stubEmbedder{vectors: map[string][]float64{"cached": {1}, "miss": {2}}}
	cache := newEmbedCache(e)
	cache.users.set("cached", []float64{1})
	for i := 0; i < maxEmbeddingConcurrency; i++ {
		cache.permits <- struct{}{}
	}
	require.Equal(t, []float64{1}, cache.vector(context.Background(), "cached"))
	done := make(chan []float64, 1)
	go func() { done <- cache.vector(context.Background(), "miss") }()
	select {
	case got := <-done:
		require.Nil(t, got)
	case <-time.After(time.Second):
		for i := 0; i < maxEmbeddingConcurrency; i++ {
			<-cache.permits
		}
		t.Fatal("cache miss waited for an embedding permit")
	}
	for i := 0; i < maxEmbeddingConcurrency; i++ {
		<-cache.permits
	}
	require.Equal(t, 0, e.calls())
	require.Equal(t, []float64{2}, cache.vector(context.Background(), "miss"))
}

func TestEmbedCache_SharesInFlightWhenFull(t *testing.T) {
	e := &controlledEmbedder{calls: make(map[string]int), started: make(chan string, 1), release: make(chan struct{})}
	cache := newEmbedCache(e)
	for i := 1; i < maxEmbeddingConcurrency; i++ {
		cache.permits <- struct{}{}
	}
	done := make(chan []float64, 2)
	go func() { done <- cache.vector(context.Background(), "shared") }()
	select {
	case <-e.started:
	case <-time.After(time.Second):
		close(e.release)
		t.Fatal("embedding did not start")
	}
	go func() { done <- cache.vector(context.Background(), "shared") }()
	select {
	case <-done:
		close(e.release)
		t.Fatal("shared embedding returned before it completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(e.release)
	for i := 0; i < 2; i++ {
		select {
		case v := <-done:
			require.NotEmpty(t, v)
		case <-time.After(time.Second):
			t.Fatal("shared embedding did not complete")
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	require.Equal(t, 1, e.calls["shared"])
}

func TestEmbedCache_BoundsUserAndCandidateVectors(t *testing.T) {
	stub := &stubEmbedder{vectors: make(map[string][]float64)}
	cache := newEmbedCache(stub)
	ctx := context.Background()
	stub.vectors["candidate"] = []float64{1}
	require.NotNil(t, cache.candidateVector(ctx, "candidate"))
	for i := 0; i <= 128; i++ {
		text := fmt.Sprintf("user %d", i)
		stub.vectors[text] = []float64{1}
		require.NotNil(t, cache.vector(ctx, text))
	}
	calls := stub.calls()
	require.NotNil(t, cache.candidateVector(ctx, "candidate"))
	require.Equal(t, calls, stub.calls(), "user messages must not evict candidate vectors")
	require.NotNil(t, cache.vector(ctx, "user 0"))
	require.Equal(t, calls+1, stub.calls(), "old user vector must be evicted")
	require.NotNil(t, cache.vector(ctx, "user 2"))
	stub.vectors["user new"] = []float64{1}
	require.NotNil(t, cache.vector(ctx, "user new"))
	calls = stub.calls()
	require.NotNil(t, cache.vector(ctx, "user 2"))
	require.Equal(t, calls, stub.calls(), "recently accessed vector must be retained")
	require.NotNil(t, cache.vector(ctx, "user 3"))
	require.Equal(t, calls+1, stub.calls(), "least recently used vector must be evicted")

	for i := 0; i <= 1024; i++ {
		text := fmt.Sprintf("candidate %d", i)
		stub.vectors[text] = []float64{1}
		require.NotNil(t, cache.candidateVector(ctx, text))
	}
	calls = stub.calls()
	require.NotNil(t, cache.candidateVector(ctx, "candidate"))
	require.Equal(t, calls+1, stub.calls(), "old candidate vector must be evicted")
	require.NotNil(t, cache.vector(ctx, "user 0"))
	require.Equal(t, calls+1, stub.calls(), "candidate vectors must not evict user messages")
}

func TestCosine(t *testing.T) {
	t.Parallel()

	require.InDelta(t, 1.0, cosine([]float64{1, 0}, []float64{2, 0}), 1e-9)
	require.InDelta(t, 0.0, cosine([]float64{1, 0}, []float64{0, 1}), 1e-9)
	require.InDelta(t, 0.0, cosine([]float64{1, 0}, []float64{1}), 1e-9)
	require.InDelta(t, 0.0, cosine(nil, nil), 1e-9)
}

// stubEmbedder is a deterministic embedding source for tests.
type stubEmbedder struct {
	mu      sync.Mutex
	vectors map[string][]float64
	err     error
	n       int
}

func (e *stubEmbedder) GetEmbedding(
	ctx context.Context,
	text string,
) ([]float64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.n++
	if e.err != nil {
		return nil, e.err
	}
	return e.vectors[text], nil
}

func (e *stubEmbedder) calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n
}

func (e *stubEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	v, err := e.GetEmbedding(ctx, text)
	return v, nil, err
}

func (e *stubEmbedder) GetDimensions() int { return 0 }
