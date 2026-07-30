//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package ranking

import (
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/assistantresult"
)

const (
	defaultHybridCandidateRatio = 3
	focusedTailSlots            = 1
)

// HybridCandidateLimit expands the candidate window only when an ordinary
// query has enough terms for focused tail ranking.
func HybridCandidateLimit(query string, limit int) int {
	if limit <= 0 || asksForAssistantResult(query) ||
		len(focusedQueryTerms(query)) < minimumFocusedPassageMatches {
		return limit
	}
	maxInt := int(^uint(0) >> 1)
	if limit > maxInt/defaultHybridCandidateRatio {
		return maxInt
	}
	return limit * defaultHybridCandidateRatio
}

type focusedTailResult struct {
	entry           *memory.Entry
	matchedTerms    int
	eventTimeTerms  int
	passageTerms    int
	temporalEpisode bool
}

func backfillFocusedTail(
	query string,
	base []*memory.Entry,
	candidates []*memory.Entry,
	maxResults int,
) []*memory.Entry {
	if maxResults <= focusedTailSlots || len(base) == 0 ||
		len(candidates) == 0 {
		return base
	}
	ranked := rankFocusedTail(query, candidates)
	if len(ranked) == 0 {
		return base
	}

	limit := min(maxResults, len(base))
	seen := make(map[string]struct{}, limit)
	for _, entry := range base[:limit] {
		if entry != nil && entry.ID != "" {
			seen[entry.ID] = struct{}{}
		}
	}

	var selected *memory.Entry
	for _, entry := range ranked {
		if entry == nil || entry.ID == "" {
			continue
		}
		if _, ok := seen[entry.ID]; ok {
			continue
		}
		selected = entry
		break
	}
	if selected == nil {
		return base
	}

	results := append([]*memory.Entry(nil), base[:limit]...)
	if len(results) < maxResults {
		return append(results, selected)
	}
	results[len(results)-1] = selected
	return results
}

func rankFocusedTail(
	query string,
	candidates []*memory.Entry,
) []*memory.Entry {
	queryTerms := focusedQueryTerms(query)
	if len(queryTerms) < minimumFocusedPassageMatches {
		return nil
	}

	ranked := make([]focusedTailResult, 0, len(candidates))
	for _, entry := range candidates {
		if entry == nil || entry.Memory == nil ||
			assistantresult.Is(entry.Memory.Memory) {
			continue
		}
		matched, passageTerms := bestFocusedPassageMatch(
			queryTerms, entry.Memory.Memory,
		)
		eventTimeTerms := focusedEventTimeMatches(queryTerms, entry)
		if matched < minimumFocusedPassageMatches &&
			(matched == 0 || eventTimeTerms == 0) {
			continue
		}
		ranked = append(ranked, focusedTailResult{
			entry:          entry,
			matchedTerms:   matched,
			eventTimeTerms: eventTimeTerms,
			passageTerms:   passageTerms,
			temporalEpisode: eventTimeTerms > 0 &&
				entry.Memory.Kind == memory.KindEpisode,
		})
	}
	if len(ranked) == 0 {
		return nil
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		left := ranked[i]
		right := ranked[j]
		leftTotal := left.matchedTerms + left.eventTimeTerms
		rightTotal := right.matchedTerms + right.eventTimeTerms
		if leftTotal != rightTotal {
			return leftTotal > rightTotal
		}
		if left.temporalEpisode != right.temporalEpisode {
			return left.temporalEpisode
		}
		if left.eventTimeTerms != right.eventTimeTerms {
			return left.eventTimeTerms > right.eventTimeTerms
		}
		if left.matchedTerms != right.matchedTerms {
			return left.matchedTerms > right.matchedTerms
		}
		return left.passageTerms < right.passageTerms
	})

	results := make([]*memory.Entry, 0, len(ranked))
	for _, item := range ranked {
		results = append(results, item.entry)
	}
	return results
}

func focusedEventTimeMatches(
	queryTerms map[string]struct{},
	entry *memory.Entry,
) int {
	if entry.Memory.EventTime == nil {
		return 0
	}
	eventTime := entry.Memory.EventTime.UTC()
	eventTerms := []string{
		strings.ToLower(eventTime.Format("January")),
		strings.ToLower(eventTime.Format("Jan")),
		strconv.Itoa(eventTime.Year()),
		eventTime.Format("01"),
		eventTime.Format("02"),
		eventTime.Format("20060102"),
	}
	matched := 0
	for _, term := range eventTerms {
		if _, ok := queryTerms[term]; ok {
			matched++
		}
	}
	return matched
}

func hybridResultHead(
	results []*memory.Entry,
	maxResults int,
) []*memory.Entry {
	if maxResults > 0 && len(results) > maxResults {
		return results[:maxResults]
	}
	return results
}

func hybridTailCandidates(
	vectorResults []*memory.Entry,
	keywordResults []*memory.Entry,
	maxResults int,
) []*memory.Entry {
	if maxResults <= 0 {
		return nil
	}
	candidates := make([]*memory.Entry, 0)
	seen := make(map[string]struct{})
	appendTail := func(results []*memory.Entry) {
		if len(results) <= maxResults {
			return
		}
		for _, entry := range results[maxResults:] {
			if entry == nil || entry.ID == "" {
				continue
			}
			if _, ok := seen[entry.ID]; ok {
				continue
			}
			seen[entry.ID] = struct{}{}
			candidates = append(candidates, entry)
		}
	}
	appendTail(vectorResults)
	appendTail(keywordResults)
	return candidates
}
