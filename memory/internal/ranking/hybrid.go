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

// MergeHybrid combines backend-provided vector and keyword rankings within the
// requested result window. Ordinary queries may replace the last result with
// one strongly matched candidate from the overfetched tail.
func MergeHybrid(
	query string,
	vectorResults []*memory.Entry,
	keywordResults []*memory.Entry,
	k int,
	maxResults int,
) []*memory.Entry {
	vectorHead := hybridResultHead(vectorResults, maxResults)
	keywordHead := hybridResultHead(keywordResults, maxResults)
	assistantResultQuery := asksForAssistantResult(query)
	rankings := make([][]*memory.Entry, 0, 4)
	if len(vectorHead) > 0 {
		rankings = append(rankings, vectorHead)
	}
	if len(keywordHead) > 0 {
		rankings = append(rankings, keywordHead)
	}
	if assistantResultQuery {
		if focused := rankResultsByFocusedPassage(
			query, vectorHead,
		); len(focused) > 0 {
			rankings = append(rankings, focused)
		}
	}
	if provenance := rankResultsByAssistantResultIntent(
		query, vectorHead, keywordHead,
	); len(provenance) > 0 {
		rankings = append(rankings, provenance)
	}
	var results []*memory.Entry
	switch len(rankings) {
	case 0:
		return nil
	case 1:
		results = rankings[0]
	default:
		results = imemory.MergeRankedResults(rankings, k, maxResults)
	}
	if assistantResultQuery {
		return results
	}
	return backfillFocusedTail(
		query,
		results,
		hybridTailCandidates(vectorResults, keywordResults, maxResults),
		maxResults,
	)
}
