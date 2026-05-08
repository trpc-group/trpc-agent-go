//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

type catalogEntry struct {
	Name        string
	Description string
	SearchText  string
	LimitBucket string
	Tool        tool.Tool
}

type rankedEntry struct {
	Entry catalogEntry
	Score float64
}

type indexedDoc struct {
	entry    catalogEntry
	termFreq map[string]int
	docLen   float64
}

type localIndex struct {
	docs      []indexedDoc
	docFreq   map[string]int
	avgDocLen float64
	analyzer  Analyzer
}

// DefaultAnalyzer returns the default lexical analyzer used by DeferredToolSet.
func DefaultAnalyzer() Analyzer {
	return AnalyzerFunc(defaultAnalyze)
}

func newLocalIndex(entries []catalogEntry, analyzer Analyzer) *localIndex {
	if analyzer == nil {
		analyzer = DefaultAnalyzer()
	}
	idx := &localIndex{
		docs:     make([]indexedDoc, 0, len(entries)),
		docFreq:  make(map[string]int),
		analyzer: analyzer,
	}
	if len(entries) == 0 {
		return idx
	}
	var totalLen float64
	for _, entry := range entries {
		tokens := analyzer.Analyze(entry.SearchText)
		tf := make(map[string]int, len(tokens))
		seen := make(map[string]struct{}, len(tokens))
		for _, token := range tokens {
			if token == "" {
				continue
			}
			tf[token]++
			if _, ok := seen[token]; ok {
				continue
			}
			idx.docFreq[token]++
			seen[token] = struct{}{}
		}
		docLen := 0.0
		for _, count := range tf {
			docLen += float64(count)
		}
		totalLen += docLen
		idx.docs = append(idx.docs, indexedDoc{
			entry:    entry,
			termFreq: tf,
			docLen:   docLen,
		})
	}
	idx.avgDocLen = totalLen / float64(len(idx.docs))
	if idx.avgDocLen <= 0 {
		idx.avgDocLen = 1
	}
	return idx
}

func (idx *localIndex) Search(query string, limit int) []rankedEntry {
	if idx == nil || len(idx.docs) == 0 || limit <= 0 {
		return nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	queryTokens := uniqueTokens(idx.analyzer.Analyze(query))
	if len(queryTokens) == 0 {
		return nil
	}
	queryLower := strings.ToLower(query)
	scored := make([]rankedEntry, 0, len(idx.docs))
	totalDocs := float64(len(idx.docs))
	for _, doc := range idx.docs {
		score := 0.0
		for _, token := range queryTokens {
			tf := float64(doc.termFreq[token])
			if tf == 0 {
				continue
			}
			df := float64(idx.docFreq[token])
			idf := math.Log(1 + (totalDocs-df+0.5)/(df+0.5))
			denom := tf + bm25K1*(1-bm25B+bm25B*(doc.docLen/idx.avgDocLen))
			if denom == 0 {
				continue
			}
			score += idf * ((tf * (bm25K1 + 1)) / denom)
		}
		score += lexicalBoost(doc.entry, queryLower)
		if score <= 0 {
			continue
		}
		scored = append(scored, rankedEntry{
			Entry: doc.entry,
			Score: score,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Entry.Name < scored[j].Entry.Name
		}
		return scored[i].Score > scored[j].Score
	})
	return applyBucketLimit(scored, limit)
}

func applyBucketLimit(results []rankedEntry, limit int) []rankedEntry {
	if len(results) <= limit {
		return results
	}
	perBucketLimit := limit / 2
	if perBucketLimit < 1 {
		perBucketLimit = 1
	}
	selected := make([]rankedEntry, 0, limit)
	selectedByName := make(map[string]struct{}, limit)
	bucketCount := make(map[string]int)
	for _, item := range results {
		if len(selected) >= limit {
			break
		}
		if _, ok := selectedByName[item.Entry.Name]; ok {
			continue
		}
		bucket := strings.TrimSpace(item.Entry.LimitBucket)
		if bucket != "" && bucketCount[bucket] >= perBucketLimit {
			continue
		}
		selected = append(selected, item)
		selectedByName[item.Entry.Name] = struct{}{}
		if bucket != "" {
			bucketCount[bucket]++
		}
	}
	if len(selected) >= limit {
		return selected
	}
	for _, item := range results {
		if len(selected) >= limit {
			break
		}
		if _, ok := selectedByName[item.Entry.Name]; ok {
			continue
		}
		selected = append(selected, item)
		selectedByName[item.Entry.Name] = struct{}{}
	}
	return selected
}

func lexicalBoost(entry catalogEntry, queryLower string) float64 {
	if queryLower == "" {
		return 0
	}
	nameLower := strings.ToLower(entry.Name)
	descLower := strings.ToLower(entry.Description)
	searchLower := strings.ToLower(entry.SearchText)
	boost := 0.0
	switch {
	case nameLower == queryLower:
		boost += 8
	case strings.HasPrefix(nameLower, queryLower):
		boost += 4
	case strings.Contains(nameLower, queryLower):
		boost += 2
	}
	if descLower != "" && strings.Contains(descLower, queryLower) {
		boost += 0.5
	}
	if boost == 0 && strings.Contains(searchLower, queryLower) {
		boost += 0.25
	}
	return boost
}

func uniqueTokens(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]string, 0, len(tokens))
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		out = append(out, token)
		seen[token] = struct{}{}
	}
	return out
}

func defaultAnalyze(text string) []string {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return nil
	}
	tokens := make([]string, 0, 16)
	var ascii []rune
	var cjk []rune
	flushASCII := func() {
		if len(ascii) == 0 {
			return
		}
		tokens = append(tokens, analyzeASCIIChunk(string(ascii))...)
		ascii = ascii[:0]
	}
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		tokens = append(tokens, analyzeCJKChunk(cjk)...)
		cjk = cjk[:0]
	}
	for _, r := range text {
		switch {
		case isCJKRune(r):
			flushASCII()
			cjk = append(cjk, r)
		case isWordRune(r):
			flushCJK()
			ascii = append(ascii, r)
		default:
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return tokens
}

func analyzeASCIIChunk(chunk string) []string {
	chunk = strings.TrimSpace(strings.ToLower(chunk))
	if chunk == "" {
		return nil
	}
	tokens := []string{chunk}
	fields := splitASCIIChunk(chunk)
	if len(fields) == 0 {
		return tokens
	}
	tokens = append(tokens, fields...)
	return tokens
}

func splitASCIIChunk(chunk string) []string {
	if chunk == "" {
		return nil
	}
	replaced := strings.NewReplacer(
		"_", " ",
		"-", " ",
		".", " ",
		"/", " ",
		":", " ",
	).Replace(chunk)
	parts := strings.Fields(replaced)
	out := make([]string, 0, len(parts)*2)
	for _, part := range parts {
		for _, split := range splitCamelCase(part) {
			if split != "" {
				out = append(out, split)
			}
		}
	}
	return out
}

func splitCamelCase(part string) []string {
	if part == "" {
		return nil
	}
	runes := []rune(part)
	out := make([]string, 0, 4)
	start := 0
	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		curr := runes[i]
		nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if unicode.IsLower(prev) && unicode.IsUpper(curr) ||
			unicode.IsDigit(prev) && unicode.IsLetter(curr) ||
			unicode.IsLetter(prev) && unicode.IsDigit(curr) ||
			unicode.IsUpper(prev) && unicode.IsUpper(curr) && nextLower {
			out = append(out, strings.ToLower(string(runes[start:i])))
			start = i
		}
	}
	out = append(out, strings.ToLower(string(runes[start:])))
	return out
}

func analyzeCJKChunk(chunk []rune) []string {
	if len(chunk) == 0 {
		return nil
	}
	out := make([]string, 0, len(chunk)*2)
	for _, r := range chunk {
		out = append(out, string(r))
	}
	if len(chunk) == 1 {
		return out
	}
	for i := 0; i < len(chunk)-1; i++ {
		out = append(out, string(chunk[i:i+2]))
	}
	return out
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == '/' || r == ':'
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.In(r, unicode.Hiragana, unicode.Katakana)
}
