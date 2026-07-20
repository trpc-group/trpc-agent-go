//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const minimumFocusedPassageMatches = 2

var enumeratedPassagePattern = regexp.MustCompile(
	`(^|[[:space:]])[(]?[0-9]{1,2}[.)][[:space:]]*`,
)

var focusedPassageStopWords = map[string]struct{}{
	"a": {}, "about": {}, "after": {}, "again": {}, "all": {},
	"also": {}, "am": {}, "an": {}, "and": {}, "any": {},
	"are": {}, "as": {}, "assistant": {}, "at": {}, "be": {},
	"been": {}, "before": {}, "being": {}, "but": {}, "by": {},
	"can": {}, "conversation": {}, "could": {}, "did": {},
	"discuss": {}, "do": {}, "does": {}, "for": {}, "follow": {},
	"from": {}, "had": {}, "has": {}, "have": {}, "he": {},
	"her": {}, "here": {}, "him": {}, "his": {}, "how": {},
	"i": {}, "if": {}, "in": {}, "is": {}, "it": {},
	"its": {}, "learn": {}, "me": {}, "mention": {}, "my": {},
	"of": {}, "on": {}, "or": {}, "our": {}, "please": {},
	"previous": {}, "recommend": {}, "remember": {}, "remind": {},
	"result": {}, "say": {}, "she": {}, "should": {}, "specific": {},
	"tell": {}, "that": {}, "the": {}, "their": {}, "them": {},
	"then": {}, "there": {}, "these": {}, "they": {}, "this": {},
	"those": {}, "to": {}, "up": {}, "us": {}, "want": {},
	"was": {}, "we": {}, "were": {}, "what": {}, "when": {},
	"where": {}, "which": {}, "who": {}, "why": {}, "will": {},
	"with": {}, "would": {}, "you": {}, "your": {},
}

type focusedPassageResult struct {
	entry        *memory.Entry
	matchedTerms int
	passageTerms int
}

func rankResultsByFocusedPassage(
	query string,
	results []*memory.Entry,
) []*memory.Entry {
	queryTerms := focusedQueryTerms(query)
	if len(queryTerms) < minimumFocusedPassageMatches {
		return nil
	}

	ranked := make([]focusedPassageResult, 0, len(results))
	for _, entry := range results {
		if entry == nil || entry.Memory == nil {
			continue
		}
		matched, passageTerms := bestFocusedPassageMatch(
			queryTerms, entry.Memory.Memory,
		)
		if matched < minimumFocusedPassageMatches {
			continue
		}
		ranked = append(ranked, focusedPassageResult{
			entry:        entry,
			matchedTerms: matched,
			passageTerms: passageTerms,
		})
	}
	if len(ranked) == 0 {
		return nil
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].matchedTerms != ranked[j].matchedTerms {
			return ranked[i].matchedTerms > ranked[j].matchedTerms
		}
		return ranked[i].passageTerms < ranked[j].passageTerms
	})
	result := make([]*memory.Entry, 0, len(ranked))
	for _, item := range ranked {
		result = append(result, item.entry)
	}
	return result
}

func focusedQueryTerms(query string) map[string]struct{} {
	segments := splitFocusedPassages(query)
	for i := len(segments) - 1; i >= 0; i-- {
		if terms := focusedTermSet(segments[i]); len(terms) >= minimumFocusedPassageMatches {
			return terms
		}
	}
	return focusedTermSet(query)
}

func bestFocusedPassageMatch(
	queryTerms map[string]struct{},
	text string,
) (int, int) {
	bestMatched := 0
	bestTerms := 0
	for _, passage := range splitFocusedPassages(text) {
		terms := focusedTermSet(passage)
		matched := 0
		for term := range terms {
			if _, ok := queryTerms[term]; ok {
				matched++
			}
		}
		if matched > bestMatched ||
			(matched == bestMatched && matched > 0 &&
				(bestTerms == 0 || len(terms) < bestTerms)) {
			bestMatched = matched
			bestTerms = len(terms)
		}
	}
	return bestMatched, bestTerms
}

func splitFocusedPassages(text string) []string {
	text = enumeratedPassagePattern.ReplaceAllString(text, "\n")
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '\n', '\r', '.', ';', '!', '?':
			return true
		default:
			return false
		}
	})
}

func focusedTermSet(text string) map[string]struct{} {
	terms := make(map[string]struct{})
	for _, term := range focusedTokens(text) {
		if _, stop := focusedPassageStopWords[term]; stop || len(term) < 2 {
			continue
		}
		terms[term] = struct{}{}
	}
	return terms
}

func focusedTokens(text string) []string {
	var normalized strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			normalized.WriteRune(r)
		case r == '-':
			// Treat compounds such as "back-end" as one retrieval term.
		default:
			normalized.WriteByte(' ')
		}
	}
	fields := strings.Fields(normalized.String())
	for i, field := range fields {
		fields[i] = stemFocusedToken(field)
	}
	return fields
}

func stemFocusedToken(term string) string {
	switch {
	case len(term) > 5 && strings.HasSuffix(term, "ing"):
		term = strings.TrimSuffix(term, "ing")
		if len(term) > 2 && term[len(term)-1] == term[len(term)-2] {
			term = term[:len(term)-1]
		}
	case len(term) > 4 && strings.HasSuffix(term, "ied"):
		term = strings.TrimSuffix(term, "ied") + "y"
	case len(term) > 4 && strings.HasSuffix(term, "ed"):
		term = strings.TrimSuffix(term, "ed")
	case len(term) > 4 && strings.HasSuffix(term, "ies"):
		term = strings.TrimSuffix(term, "ies") + "y"
	case len(term) > 3 && strings.HasSuffix(term, "s") &&
		!strings.HasSuffix(term, "ss"):
		term = strings.TrimSuffix(term, "s")
	}
	return term
}
