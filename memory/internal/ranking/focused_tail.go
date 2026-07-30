//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package ranking

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

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
	queryTerms := focusedQueryTerms(query)
	if limit <= 0 || asksForAssistantResult(query) ||
		len(queryTerms) < minimumFocusedPassageMatches ||
		!focusedQueryHasCalendarReference(queryTerms) {
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
	selectedCopy := *selected
	selectedCopy.Score = focusedTailScore(results)
	if len(results) < maxResults {
		return append(results, &selectedCopy)
	}
	results[len(results)-1] = &selectedCopy
	return results
}

func focusedTailScore(results []*memory.Entry) float64 {
	minScore := math.Inf(1)
	for _, entry := range results {
		if entry == nil || math.IsNaN(entry.Score) {
			continue
		}
		minScore = min(minScore, entry.Score)
	}
	if math.IsInf(minScore, 1) {
		return 0
	}
	return math.Nextafter(minScore, math.Inf(-1))
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
		contentMatches, passageTerms := bestFocusedPassageMatchTerms(
			queryTerms, entry.Memory.Memory,
		)
		eventMatches := focusedEventTimeMatchTerms(queryTerms, entry)
		if len(eventMatches) == 0 {
			continue
		}
		distinctContentMatches := 0
		for term := range contentMatches {
			if _, temporal := eventMatches[term]; !temporal {
				distinctContentMatches++
			}
		}
		if distinctContentMatches == 0 {
			continue
		}
		ranked = append(ranked, focusedTailResult{
			entry:           entry,
			matchedTerms:    distinctContentMatches,
			eventTimeTerms:  len(eventMatches),
			passageTerms:    passageTerms,
			temporalEpisode: entry.Memory.Kind == memory.KindEpisode,
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

func focusedEventTimeMatchTerms(
	queryTerms map[string]struct{},
	entry *memory.Entry,
) map[string]struct{} {
	if entry.Memory.EventTime == nil {
		return nil
	}
	eventTime := entry.Memory.EventTime.UTC()
	eventTerms := []string{
		strings.ToLower(eventTime.Format("January")),
		strings.ToLower(eventTime.Format("Jan")),
		strings.ToLower(eventTime.Format("Monday")),
		strings.ToLower(eventTime.Format("Mon")),
		strconv.Itoa(eventTime.Year()),
		eventTime.Format("01"),
		eventTime.Format("02"),
		eventTime.Format("20060102"),
	}
	matched := make(map[string]struct{})
	for _, term := range eventTerms {
		if _, ok := queryTerms[term]; ok {
			matched[term] = struct{}{}
		}
	}
	return matched
}

var focusedCalendarTerms = map[string]struct{}{
	"january": {}, "jan": {}, "february": {}, "feb": {},
	"march": {}, "mar": {}, "april": {}, "apr": {},
	"may": {}, "june": {}, "jun": {}, "july": {}, "jul": {},
	"august": {}, "aug": {}, "september": {}, "sep": {}, "sept": {},
	"october": {}, "oct": {}, "november": {}, "nov": {},
	"december": {}, "dec": {},
	"monday": {}, "mon": {}, "tuesday": {}, "tue": {}, "tues": {},
	"wednesday": {}, "wed": {}, "thursday": {}, "thu": {}, "thur": {},
	"thurs": {}, "friday": {}, "fri": {}, "saturday": {}, "sat": {},
	"sunday": {}, "sun": {},
}

func focusedQueryHasCalendarReference(queryTerms map[string]struct{}) bool {
	for term := range queryTerms {
		if _, ok := focusedCalendarTerms[term]; ok {
			return true
		}
		switch len(term) {
		case 4:
			year, err := strconv.Atoi(term)
			if err == nil && year >= 1900 && year <= 2100 {
				return true
			}
		case 8:
			if _, err := time.Parse("20060102", term); err == nil {
				return true
			}
		}
	}
	return false
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
