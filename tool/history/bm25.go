//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package history

import (
	"math"
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

// BM25 tuning parameters.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// segmenter is a package-level gse.Segmenter instance,
// initialized lazily on first use with sync.Once.
var (
	seg     gse.Segmenter
	segOnce sync.Once
)

// initSegmenter loads the default dictionary once.
func initSegmenter() {
	segOnce.Do(func() {
		seg.SkipLog = true
		// Load default embedded dict for Chinese + English.
		seg.LoadDict()
	})
}

// tokenizeText splits text into lowercase tokens using gse
// for Chinese segmentation and unicode-aware splitting for
// other languages. Punctuation and whitespace are removed.
func tokenizeText(text string) []string {
	initSegmenter()
	raw := seg.CutSearch(text, true)
	tokens := make([]string, 0, len(raw))
	for _, w := range raw {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		lower := strings.ToLower(w)
		// Skip pure punctuation / whitespace tokens.
		if isPunct(lower) {
			continue
		}
		tokens = append(tokens, lower)
	}
	return tokens
}

// isPunct returns true if the string consists entirely of
// punctuation, symbols, or whitespace characters.
func isPunct(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// tokenFrequency counts how many times each token appears.
func tokenFrequency(tokens []string) map[string]int {
	freq := make(map[string]int, len(tokens))
	for _, t := range tokens {
		freq[t]++
	}
	return freq
}

// bm25Scorer computes BM25 relevance scores for a set of
// documents (events) against a query.
type bm25Scorer struct {
	// docCount is the total number of documents.
	docCount int
	// avgDL is the average document length in tokens.
	avgDL float64
	// df maps each token to the number of documents
	// containing that token.
	df map[string]int
	// docTokenFreqs holds per-document token frequencies.
	docTokenFreqs []map[string]int
	// docLengths holds per-document token counts.
	docLengths []int
}

// newBM25Scorer builds a scorer from pre-tokenized documents.
// Each element of docTokens is the token list for one
// document.
func newBM25Scorer(docTokens [][]string) *bm25Scorer {
	n := len(docTokens)
	scorer := &bm25Scorer{
		docCount:      n,
		df:            make(map[string]int),
		docTokenFreqs: make([]map[string]int, n),
		docLengths:    make([]int, n),
	}

	totalLen := 0
	for i, tokens := range docTokens {
		scorer.docLengths[i] = len(tokens)
		totalLen += len(tokens)
		freq := tokenFrequency(tokens)
		scorer.docTokenFreqs[i] = freq
		for t := range freq {
			scorer.df[t]++
		}
	}
	if n > 0 {
		scorer.avgDL = float64(totalLen) / float64(n)
	}
	return scorer
}

// idf computes the inverse document frequency for a term.
//
//	idf(t) = ln((N - df(t) + 0.5) / (df(t) + 0.5) + 1)
func (s *bm25Scorer) idf(term string) float64 {
	df := s.df[term]
	num := float64(s.docCount-df) + 0.5
	den := float64(df) + 0.5
	return math.Log(num/den + 1.0)
}

// score computes the BM25 score of document i against the
// given query tokens.
func (s *bm25Scorer) score(
	docIdx int, queryTokens []string,
) float64 {
	if docIdx < 0 || docIdx >= s.docCount {
		return 0
	}
	freq := s.docTokenFreqs[docIdx]
	dl := float64(s.docLengths[docIdx])

	total := 0.0
	for _, qt := range queryTokens {
		tf := float64(freq[qt])
		if tf == 0 {
			continue
		}
		idfVal := s.idf(qt)
		num := tf * (bm25K1 + 1)
		den := tf + bm25K1*(1-bm25B+bm25B*dl/s.avgDL)
		total += idfVal * num / den
	}
	return total
}
