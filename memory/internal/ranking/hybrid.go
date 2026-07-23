//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package ranking

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

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
	if focused := rankResultsByFocusedPassage(
		query, vectorResults,
	); len(focused) > 0 {
		rankings = append(rankings, focused)
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
