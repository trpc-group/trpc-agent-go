//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rouge

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Compute returns ROUGE scores for a single target and prediction pair.
// Compute returns an empty map when no ROUGE types are configured.
func Compute(ctx context.Context, target, prediction string, opt ...Option) (map[string]Score, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	opts := newOptions(opt...)
	if len(opts.rougeTypes) == 0 {
		return map[string]Score{}, nil
	}
	for _, rougeType := range opts.rougeTypes {
		if err := validateRougeType(rougeType); err != nil {
			return nil, err
		}
	}

	tok := opts.tokenizer
	if tok == nil {
		tok = newTokenizer(opts.useStemmer)
	}
	result := make(map[string]Score, len(opts.rougeTypes))
	onlyRougeLsum := len(opts.rougeTypes) == 1 && opts.rougeTypes[0] == "rougeLsum"
	var targetTokens, predTokens []string
	if !onlyRougeLsum {
		targetTokens = tok.Tokenize(target)
		predTokens = tok.Tokenize(prediction)
	}

	for _, rougeType := range opts.rougeTypes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch {
		case rougeType == "rougeL":
			result[rougeType] = scoreLCS(targetTokens, predTokens)
		case rougeType == "rougeLsum":
			score, err := scoreSummaryLCS(target, prediction, tok, opts.splitSummaries)
			if err != nil {
				return nil, err
			}
			result[rougeType] = score
		case strings.HasPrefix(rougeType, "rouge"):
			n, err := parseRougeN(rougeType)
			if err != nil {
				return nil, err
			}
			result[rougeType] = scoreNGrams(targetTokens, predTokens, n)
		default:
			return nil, fmt.Errorf("invalid rouge type: %s", rougeType)
		}
	}
	return result, nil
}

// computeMulti computes ROUGE scores for multiple targets and selects the maximum F-measure per type.
func computeMulti(ctx context.Context, targets []string, prediction string, opt ...Option) (map[string]Score, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("targets are empty")
	}
	opts := newOptions(opt...)
	best := make(map[string]Score, len(opts.rougeTypes))
	for i, target := range targets {
		scores, err := Compute(ctx, target, prediction, opt...)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			for k, v := range scores {
				best[k] = v
			}
			continue
		}
		for k, v := range scores {
			if v.FMeasure > best[k].FMeasure {
				best[k] = v
			}
		}
	}
	return best, nil
}

// validateRougeType validates a ROUGE type identifier such as rouge1, rougeL, or rougeLsum.
func validateRougeType(rougeType string) error {
	if rougeType == "rougeL" || rougeType == "rougeLsum" {
		return nil
	}
	_, err := parseRougeN(rougeType)
	return err
}

// parseRougeN parses a ROUGE-N type string and returns the N value.
func parseRougeN(rougeType string) (int, error) {
	if !strings.HasPrefix(rougeType, "rouge") {
		return 0, fmt.Errorf("invalid rouge type: %s", rougeType)
	}
	nStr := strings.TrimPrefix(rougeType, "rouge")
	if nStr == "" {
		return 0, fmt.Errorf("invalid rouge type: %s", rougeType)
	}
	n, err := strconv.Atoi(nStr)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid rouge type: %s", rougeType)
	}
	return n, nil
}

// scoreNGrams computes ROUGE-N precision, recall, and F-measure for tokenized inputs.
func scoreNGrams(targetTokens, predTokens []string, n int) Score {
	if len(targetTokens) == 0 || len(predTokens) == 0 {
		return Score{}
	}
	targetNGrams := createNGrams(targetTokens, n)
	predNGrams := createNGrams(predTokens, n)

	var intersection int
	var targetCount int
	for key, cnt := range targetNGrams {
		targetCount += cnt
		if predCnt, ok := predNGrams[key]; ok {
			if cnt < predCnt {
				intersection += cnt
			} else {
				intersection += predCnt
			}
		}
	}
	var predCount int
	for _, cnt := range predNGrams {
		predCount += cnt
	}

	precision := float64(intersection) / float64(maxInt(predCount, 1))
	recall := float64(intersection) / float64(maxInt(targetCount, 1))
	return Score{Precision: precision, Recall: recall, FMeasure: fMeasure(precision, recall)}
}

// createNGrams builds a multiset of n-grams keyed by a delimiter-joined token sequence.
func createNGrams(tokens []string, n int) map[string]int {
	if n <= 0 || len(tokens) < n {
		return map[string]int{}
	}
	ngrams := make(map[string]int, len(tokens)-n+1)
	for i := 0; i <= len(tokens)-n; i++ {
		key := strings.Join(tokens[i:i+n], "\x00")
		ngrams[key]++
	}
	return ngrams
}

// scoreLCS computes ROUGE-L precision, recall, and F-measure using the LCS length.
func scoreLCS(targetTokens, predTokens []string) Score {
	if len(targetTokens) == 0 || len(predTokens) == 0 {
		return Score{}
	}
	lcsLen := lcsLength(targetTokens, predTokens)
	precision := float64(lcsLen) / float64(len(predTokens))
	recall := float64(lcsLen) / float64(len(targetTokens))
	return Score{Precision: precision, Recall: recall, FMeasure: fMeasure(precision, recall)}
}

// lcsLength computes the length of the longest common subsequence.
func lcsLength(ref, can []string) int {
	if len(ref) == 0 || len(can) == 0 {
		return 0
	}
	prev := make([]int, len(can)+1)
	curr := make([]int, len(can)+1)
	for i := 1; i <= len(ref); i++ {
		curr[0] = 0
		for j := 1; j <= len(can); j++ {
			if ref[i-1] == can[j-1] {
				curr[j] = prev[j-1] + 1
				continue
			}
			if prev[j] >= curr[j-1] {
				curr[j] = prev[j]
			} else {
				curr[j] = curr[j-1]
			}
		}
		prev, curr = curr, prev
	}
	return prev[len(can)]
}

// maxInt returns the larger of a and b.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// scoreSummaryLCS computes rougeLsum using summary-level LCS aggregation.
func scoreSummaryLCS(target, prediction string, tok Tokenizer, splitSummaries bool) (Score, error) {
	targetSents, err := getSentences(target, splitSummaries)
	if err != nil {
		return Score{}, err
	}
	predSents, err := getSentences(prediction, splitSummaries)
	if err != nil {
		return Score{}, err
	}

	targetTokensList := make([][]string, 0, len(targetSents))
	for _, s := range targetSents {
		targetTokensList = append(targetTokensList, tok.Tokenize(s))
	}
	predTokensList := make([][]string, 0, len(predSents))
	for _, s := range predSents {
		predTokensList = append(predTokensList, tok.Tokenize(s))
	}

	return summaryLevelLCS(targetTokensList, predTokensList), nil
}

// getSentences returns sentence strings using either newline splitting or a sentence tokenizer.
func getSentences(text string, splitSummaries bool) ([]string, error) {
	var sents []string
	if splitSummaries {
		list, err := nltkSentTokenizeEnglish(text)
		if err != nil {
			return nil, err
		}
		sents = list
	} else {
		sents = strings.Split(text, "\n")
	}
	out := make([]string, 0, len(sents))
	for _, sent := range sents {
		if len(sent) == 0 {
			continue
		}
		out = append(out, sent)
	}
	return out, nil
}

// summaryLevelLCS computes rougeLsum and prevents double-counting matched tokens.
func summaryLevelLCS(refSent, canSent [][]string) Score {
	if len(refSent) == 0 || len(canSent) == 0 {
		return Score{}
	}

	m := 0
	for _, s := range refSent {
		m += len(s)
	}
	n := 0
	for _, s := range canSent {
		n += len(s)
	}
	if m == 0 || n == 0 {
		return Score{}
	}

	tokenCntsR := make(map[string]int)
	tokenCntsC := make(map[string]int)
	for _, s := range refSent {
		for _, tok := range s {
			tokenCntsR[tok]++
		}
	}
	for _, s := range canSent {
		for _, tok := range s {
			tokenCntsC[tok]++
		}
	}

	hits := 0
	for _, r := range refSent {
		lcsTokens := unionLCS(r, canSent)
		for _, tok := range lcsTokens {
			if tokenCntsC[tok] <= 0 || tokenCntsR[tok] <= 0 {
				continue
			}
			hits++
			tokenCntsC[tok]--
			tokenCntsR[tok]--
		}
	}

	recall := float64(hits) / float64(m)
	precision := float64(hits) / float64(n)
	return Score{Precision: precision, Recall: recall, FMeasure: fMeasure(precision, recall)}
}

// unionLCS returns the union of token indices from LCS matches across candidate sentences.
func unionLCS(ref []string, cans [][]string) []string {
	lcsList := make([][]int, 0, len(cans))
	for _, can := range cans {
		lcsList = append(lcsList, lcsInd(ref, can))
	}
	union := findUnion(lcsList)
	out := make([]string, 0, len(union))
	for _, idx := range union {
		out = append(out, ref[idx])
	}
	return out
}

// findUnion merges and sorts indices from multiple LCS paths.
func findUnion(lcsList [][]int) []int {
	seen := make(map[int]struct{})
	for _, lcs := range lcsList {
		for _, idx := range lcs {
			seen[idx] = struct{}{}
		}
	}
	union := make([]int, 0, len(seen))
	for idx := range seen {
		union = append(union, idx)
	}
	sort.Ints(union)
	return union
}

// lcsInd returns indices of one LCS between ref and can.
func lcsInd(ref, can []string) []int {
	table := lcsTable(ref, can)
	return backtrackNoRec(table, ref, can)
}

// lcsTable builds the dynamic programming table for LCS reconstruction.
func lcsTable(ref, can []string) [][]int {
	rows := len(ref)
	cols := len(can)
	table := make([][]int, rows+1)
	for i := range table {
		table[i] = make([]int, cols+1)
	}
	for i := 1; i <= rows; i++ {
		for j := 1; j <= cols; j++ {
			if ref[i-1] == can[j-1] {
				table[i][j] = table[i-1][j-1] + 1
				continue
			}
			if table[i-1][j] >= table[i][j-1] {
				table[i][j] = table[i-1][j]
			} else {
				table[i][j] = table[i][j-1]
			}
		}
	}
	return table
}

// backtrackNoRec reconstructs a single LCS index sequence without recursion.
func backtrackNoRec(table [][]int, ref, can []string) []int {
	i := len(ref)
	j := len(can)
	indices := make([]int, 0, table[i][j])
	for i > 0 && j > 0 {
		if ref[i-1] == can[j-1] {
			indices = append(indices, i-1)
			i--
			j--
		} else if table[i][j-1] > table[i-1][j] {
			j--
		} else {
			i--
		}
	}
	for left, right := 0, len(indices)-1; left < right; left, right = left+1, right-1 {
		indices[left], indices[right] = indices[right], indices[left]
	}
	return indices
}
