//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const temporalEventTailSlots = 1

var temporalSequenceTerms = map[string]struct{}{
	"chronological":   {},
	"chronologically": {},
	"chronology":      {},
	"sequence":        {},
	"timeline":        {},
}

type temporalEventOrder int

const (
	temporalEventAscending temporalEventOrder = iota
	temporalEventDescending
)

func rankResultsByTemporalEventCoverage(
	query string,
	results []*memory.Entry,
) []*memory.Entry {
	order, ok := temporalOrderingDirection(query)
	if !ok {
		return nil
	}
	queryTerms := focusedTermSet(query)
	ranked := make([]*memory.Entry, 0, len(results))
	for _, entry := range results {
		if entry == nil || entry.Memory == nil ||
			imemory.EffectiveKind(entry.Memory) != memory.KindEpisode ||
			entry.Memory.EventTime == nil ||
			strings.TrimSpace(entry.Memory.Location) == "" ||
			!termSetsOverlap(queryTerms,
				focusedTermSet(entry.Memory.Location)) {
			continue
		}
		ranked = append(ranked, entry)
	}
	if len(ranked) < 2 {
		return nil
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		left := ranked[i]
		right := ranked[j]
		if !left.Memory.EventTime.Equal(*right.Memory.EventTime) {
			if order == temporalEventDescending {
				return left.Memory.EventTime.After(*right.Memory.EventTime)
			}
			return left.Memory.EventTime.Before(*right.Memory.EventTime)
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			if order == temporalEventDescending {
				return left.CreatedAt.After(right.CreatedAt)
			}
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return false
	})
	return ranked
}

func temporalOrderingDirection(
	query string,
) (temporalEventOrder, bool) {
	tokens := focusedTokens(query)
	hasSequenceCue := false
	earliest := -1
	latest := -1
	order := false
	reverse := false
	for index, token := range tokens {
		if _, ok := temporalSequenceTerms[token]; ok {
			hasSequenceCue = true
		}
		switch token {
		case "order":
			order = true
		case "reverse":
			reverse = true
		case "earliest", "oldest":
			if earliest < 0 {
				earliest = index
			}
		case "latest", "newest":
			if latest < 0 {
				latest = index
			}
		}
	}
	if !hasSequenceCue && !(earliest >= 0 && latest >= 0) &&
		!(order && reverse) {
		return temporalEventAscending, false
	}
	if reverse || latest >= 0 && (earliest < 0 || latest < earliest) {
		return temporalEventDescending, true
	}
	return temporalEventAscending, true
}

func termSetsOverlap(left, right map[string]struct{}) bool {
	for term := range left {
		if _, ok := right[term]; ok {
			return true
		}
	}
	return false
}

func backfillTemporalEventTail(
	base []*memory.Entry,
	diverse []*memory.Entry,
	maxResults int,
	slots int,
) []*memory.Entry {
	if maxResults <= 0 || slots <= 0 || len(base) == 0 || len(diverse) == 0 {
		return base
	}
	limit := min(maxResults, len(base))
	if limit == len(base) {
		return base
	}
	result := append([]*memory.Entry(nil), base[:limit]...)
	seen := make(map[string]struct{}, len(result))
	for _, entry := range result {
		if entry != nil {
			seen[entry.ID] = struct{}{}
		}
	}
	coveredEvents := make(map[string]struct{}, len(result))
	for _, entry := range result {
		if key, ok := temporalEventCoverageKey(entry); ok {
			coveredEvents[key] = struct{}{}
		}
	}
	tail := make(map[string]*memory.Entry, len(base)-limit)
	for _, entry := range base[limit:] {
		if entry != nil && entry.ID != "" {
			tail[entry.ID] = entry
		}
	}
	candidates := make([]*memory.Entry, 0, slots)
	for _, entry := range diverse {
		if entry == nil || entry.ID == "" {
			continue
		}
		if _, exists := seen[entry.ID]; exists {
			continue
		}
		coverageKey, ok := temporalEventCoverageKey(entry)
		if !ok {
			continue
		}
		if _, covered := coveredEvents[coverageKey]; covered {
			continue
		}
		baseEntry, exists := tail[entry.ID]
		if !exists {
			continue
		}
		candidates = append(candidates, baseEntry)
		seen[entry.ID] = struct{}{}
		coveredEvents[coverageKey] = struct{}{}
		if len(candidates) == slots {
			break
		}
	}
	if len(candidates) == 0 {
		return result
	}
	replace := min(len(candidates), len(result))
	copy(result[len(result)-replace:], candidates[:replace])
	return result
}

func temporalEventCoverageKey(entry *memory.Entry) (string, bool) {
	if entry == nil || entry.Memory == nil ||
		imemory.EffectiveKind(entry.Memory) != memory.KindEpisode ||
		entry.Memory.EventTime == nil {
		return "", false
	}
	location := strings.Join(focusedTokens(entry.Memory.Location), " ")
	if location == "" {
		return "", false
	}
	return entry.Memory.EventTime.UTC().Format("2006-01-02") + "|" + location, true
}
