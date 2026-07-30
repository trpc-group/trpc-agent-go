//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package ranking combines backend search rankings with shared memory-aware
// ranking signals.
package ranking

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const defaultHybridCandidateRatio = 3

// HybridCandidateLimit returns the number of candidates each hybrid
// sub-search should retrieve before ranking produces the final result limit.
func HybridCandidateLimit(limit int) int {
	if limit <= 0 {
		return limit
	}
	maxInt := int(^uint(0) >> 1)
	if limit > maxInt/defaultHybridCandidateRatio {
		return maxInt
	}
	return limit * defaultHybridCandidateRatio
}

// MergeHybrid combines backend-provided vector and keyword rankings with
// shared query-aware rankings. The latter only reorder candidates already
// retrieved by the backend.
func MergeHybrid(
	query string,
	vectorResults []*memory.Entry,
	keywordResults []*memory.Entry,
	k int,
	maxResults int,
) []*memory.Entry {
	rankings := make([][]*memory.Entry, 0, 4)
	if len(vectorResults) > 0 {
		rankings = append(rankings, vectorResults)
	}
	if len(keywordResults) > 0 {
		rankings = append(rankings, keywordResults)
	}
	if asksForAssistantResult(query) {
		if focused := rankResultsByFocusedPassage(
			query, vectorResults,
		); len(focused) > 0 {
			rankings = append(rankings, focused)
		}
	}
	if provenance := rankResultsByAssistantResultIntent(
		query, vectorResults, keywordResults,
	); len(provenance) > 0 {
		rankings = append(rankings, provenance)
	}
	switch len(rankings) {
	case 0:
		return nil
	case 1:
		return rankings[0]
	default:
		return imemory.MergeRankedResults(rankings, k, maxResults)
	}
}
