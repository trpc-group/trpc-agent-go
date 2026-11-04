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
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Candidates missed by recall are not evaluated by the decision model. The
// keyword path is deterministic; an optional embedding path (fused by RRF)
// adds synonym and cross-language recall.

// rrfK is the reciprocal rank fusion constant.
const rrfK = 60.0

const embeddingTimeout = 30 * time.Second

const (
	userEmbeddingCacheSize      = 128
	candidateEmbeddingCacheSize = 1024
	maxEmbeddingConcurrency     = 8
)

// tokenize extracts lowercase Latin/digit words of at least two characters
// and individual CJK runes.
func tokenize(text string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var word strings.Builder
	flush := func() {
		if word.Len() >= 2 {
			tokens[word.String()] = struct{}{}
		}
		word.Reset()
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case isWordRune(r):
			word.WriteRune(r)
		case isCJKRune(r):
			flush()
			tokens[string(r)] = struct{}{}
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func isWordRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_'
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// lexicalScore counts distinct query tokens occurring in a candidate's name
// or description, including substring matches.
func lexicalScore(userTokens map[string]struct{}, name, description string) float64 {
	if len(userTokens) == 0 {
		return 0
	}
	score := 0.0
	haystack := strings.ToLower(name + " " + description)
	for token := range userTokens {
		if strings.Contains(haystack, token) {
			score++
		}
	}
	return score
}

// scoredCandidate carries one recall candidate with its fused score.
type scoredCandidate struct {
	name        string
	description string
	score       float64
}

// embedCache reuses recently computed user and candidate vectors across turns.
// It is safe for concurrent use.
type embedCache struct {
	embedder interface {
		GetEmbedding(ctx context.Context, text string) ([]float64, error)
	}
	users      *runCache[[]float64]
	candidates *runCache[[]float64]
	mu         sync.Mutex
	inflight   map[string]*embeddingCall
	permits    chan struct{}
}

type embeddingCall struct {
	done   chan struct{}
	vector []float64
}

func newEmbedCache(embedder interface {
	GetEmbedding(ctx context.Context, text string) ([]float64, error)
}) *embedCache {
	return &embedCache{
		embedder:   embedder,
		users:      newRunCache[[]float64](userEmbeddingCacheSize),
		candidates: newRunCache[[]float64](candidateEmbeddingCacheSize),
		inflight:   make(map[string]*embeddingCall),
		permits:    make(chan struct{}, maxEmbeddingConcurrency),
	}
}

// vector returns the cached embedding of text, computing it when needed.
// Errors fail open: a missing vector removes the embedding rank only.
func (c *embedCache) vector(ctx context.Context, text string) []float64 {
	if c == nil || c.embedder == nil || text == "" {
		return nil
	}
	return c.cachedVector(ctx, text, c.users)
}

func (c *embedCache) candidateVector(ctx context.Context, text string) []float64 {
	if c == nil || c.embedder == nil || text == "" {
		return nil
	}
	return c.cachedVector(ctx, text, c.candidates)
}

func (c *embedCache) cachedVector(ctx context.Context, text string, cache *runCache[[]float64]) []float64 {
	v, ok := cache.get(text)
	if ok {
		return v
	}
	if ctx.Err() != nil {
		return nil
	}
	c.mu.Lock()
	if v, ok := cache.get(text); ok {
		c.mu.Unlock()
		return v
	}
	call := c.inflight[text]
	if call == nil {
		select {
		case c.permits <- struct{}{}:
		default:
			c.mu.Unlock()
			return nil
		}
		call = &embeddingCall{done: make(chan struct{})}
		c.inflight[text] = call
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), embeddingTimeout)
		go func() {
			defer cancel()
			v, err := c.embedder.GetEmbedding(workCtx, text)
			if err == nil && len(v) != 0 {
				cache.set(text, v)
				call.vector = v
			}
			c.mu.Lock()
			delete(c.inflight, text)
			<-c.permits
			close(call.done)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil
	case <-call.done:
		v := call.vector
		if len(v) != 0 {
			cache.set(text, v)
		}
		return v
	}
}

// recallRank ranks candidates using keyword and optional embedding recall.
// When both paths are available their rankings are fused with reciprocal rank
// fusion. Ties are broken by name for determinism.
func recallRank(
	ctx context.Context,
	userMessage string,
	candidates []scoredCandidate,
	embeddings *embedCache,
) []scoredCandidate {
	userTokens := tokenize(userMessage)
	if len(candidates) == 0 {
		return nil
	}

	// Keyword ranking.
	var keyword []scoredCandidate
	if len(userTokens) != 0 {
		keyword = make([]scoredCandidate, len(candidates))
		copy(keyword, candidates)
		for i := range keyword {
			keyword[i].score = lexicalScore(userTokens, keyword[i].name, keyword[i].description)
		}
		sortScored(keyword)
	}

	// Embedding ranking, when available.
	var userVec []float64
	if embeddings != nil {
		userVec = embeddings.vector(ctx, userMessage)
	}

	if userVec == nil {
		return keyword
	}

	type vectorCandidate struct {
		candidate scoredCandidate
		sim       float64
	}
	vectors := make([][]float64, len(candidates))
	jobs := make(chan int)
	var workers sync.WaitGroup
	workerCount := min(maxEmbeddingConcurrency, len(candidates))
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				cand := candidates[index]
				vectors[index] = embeddings.candidateVector(ctx, candidateText(cand.name, cand.description))
			}
		}()
	}
dispatch:
	for i := range candidates {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- i:
		}
	}
	close(jobs)
	workers.Wait()
	vectorRank := make([]vectorCandidate, 0, len(candidates))
	for i, cand := range candidates {
		vec := vectors[i]
		if vec == nil {
			continue
		}
		vectorRank = append(vectorRank, vectorCandidate{
			candidate: cand,
			sim:       cosine(userVec, vec),
		})
	}
	if len(vectorRank) == 0 {
		sortScored(keyword)
		return keyword
	}
	sort.Slice(vectorRank, func(i, j int) bool {
		if vectorRank[i].sim != vectorRank[j].sim {
			return vectorRank[i].sim > vectorRank[j].sim
		}
		return vectorRank[i].candidate.name < vectorRank[j].candidate.name
	})
	if len(userTokens) == 0 {
		out := make([]scoredCandidate, len(vectorRank))
		for rank, cand := range vectorRank {
			out[rank] = cand.candidate
			out[rank].score = 1 / (rrfK + float64(rank+1))
		}
		return out
	}

	// Fuse the two rankings by reciprocal rank fusion.
	fused := make(map[string]float64, len(candidates))
	for rank, cand := range keyword {
		fused[cand.name] += 1 / (rrfK + float64(rank+1))
	}
	for rank, cand := range vectorRank {
		fused[cand.candidate.name] += 1 / (rrfK + float64(rank+1))
	}
	out := make([]scoredCandidate, len(keyword))
	copy(out, keyword)
	for i := range out {
		out[i].score = fused[out[i].name]
	}
	sortScored(out)
	return out
}

func candidateText(name, description string) string {
	return name + "\n" + description
}

func sortScored(candidates []scoredCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})
}

// cosine computes the cosine similarity of two vectors of equal length.
func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
