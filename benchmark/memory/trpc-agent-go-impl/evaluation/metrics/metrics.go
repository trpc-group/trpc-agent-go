//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package metrics provides evaluation metrics for memory benchmark.
// Implements F1, BLEU, ROUGE, and LLM-as-Judge metrics aligned with
// LoCoMo paper standards.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// QAMetrics holds QA evaluation metrics.
type QAMetrics struct {
	F1       float64 `json:"f1"`
	BLEU     float64 `json:"bleu"`
	LLMScore float64 `json:"llm_score,omitempty"`
}

// CategoryMetrics holds metrics for a specific QA category.
type CategoryMetrics struct {
	Count    int     `json:"count"`
	F1       float64 `json:"f1"`
	BLEU     float64 `json:"bleu"`
	LLMScore float64 `json:"llm_score,omitempty"`
}

// SummaryMetrics holds event summarization metrics.
type SummaryMetrics struct {
	ROUGE1 float64 `json:"rouge_1"`
	ROUGE2 float64 `json:"rouge_2"`
	ROUGEL float64 `json:"rouge_l"`
}

// CalculateF1 computes token-level F1 score (LoCoMo standard).
func CalculateF1(prediction, groundTruth string) float64 {
	predTokens := normalizeAndTokenize(prediction)
	gtTokens := normalizeAndTokenize(groundTruth)
	if len(predTokens) == 0 || len(gtTokens) == 0 {
		if len(predTokens) == 0 && len(gtTokens) == 0 {
			return 1.0
		}
		return 0.0
	}
	common := countCommonTokens(predTokens, gtTokens)
	precision := float64(common) / float64(len(predTokens))
	recall := float64(common) / float64(len(gtTokens))
	if precision+recall == 0 {
		return 0.0
	}
	return 2 * precision * recall / (precision + recall)
}

// CalculateBLEU computes BLEU-1 score.
func CalculateBLEU(prediction, groundTruth string) float64 {
	predTokens := normalizeAndTokenize(prediction)
	gtTokens := normalizeAndTokenize(groundTruth)
	if len(predTokens) == 0 {
		if len(gtTokens) == 0 {
			return 1.0
		}
		return 0.0
	}
	// Count ground truth token frequencies.
	gtCounts := make(map[string]int)
	for _, t := range gtTokens {
		gtCounts[t]++
	}
	// Count matching tokens.
	matches := 0
	for _, t := range predTokens {
		if gtCounts[t] > 0 {
			matches++
			gtCounts[t]--
		}
	}
	// BLEU-1 precision with brevity penalty.
	precision := float64(matches) / float64(len(predTokens))
	bp := brevityPenalty(len(predTokens), len(gtTokens))
	return bp * precision
}

// CalculateROUGE1 computes ROUGE-1 F1 score.
func CalculateROUGE1(prediction, groundTruth string) float64 {
	predTokens := normalizeAndTokenize(prediction)
	gtTokens := normalizeAndTokenize(groundTruth)
	return calculateRougeN(predTokens, gtTokens, 1)
}

// CalculateROUGE2 computes ROUGE-2 F1 score.
func CalculateROUGE2(prediction, groundTruth string) float64 {
	predTokens := normalizeAndTokenize(prediction)
	gtTokens := normalizeAndTokenize(groundTruth)
	return calculateRougeN(predTokens, gtTokens, 2)
}

// CalculateROUGEL computes ROUGE-L F1 score using LCS.
func CalculateROUGEL(prediction, groundTruth string) float64 {
	predTokens := normalizeAndTokenize(prediction)
	gtTokens := normalizeAndTokenize(groundTruth)
	if len(predTokens) == 0 || len(gtTokens) == 0 {
		if len(predTokens) == 0 && len(gtTokens) == 0 {
			return 1.0
		}
		return 0.0
	}
	lcsLen := lcsLength(predTokens, gtTokens)
	precision := float64(lcsLen) / float64(len(predTokens))
	recall := float64(lcsLen) / float64(len(gtTokens))
	if precision+recall == 0 {
		return 0.0
	}
	return 2 * precision * recall / (precision + recall)
}

// CalculateRecallAtK computes retrieval recall at K.
// evidenceIDs: ground truth session IDs containing the answer.
// retrievedIDs: retrieved session IDs.
func CalculateRecallAtK(evidenceIDs, retrievedIDs []string, k int) float64 {
	if len(evidenceIDs) == 0 {
		return 1.0
	}
	topK := retrievedIDs
	if len(topK) > k {
		topK = topK[:k]
	}
	retrievedSet := make(map[string]bool)
	for _, id := range topK {
		retrievedSet[id] = true
	}
	hits := 0
	for _, id := range evidenceIDs {
		if retrievedSet[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(evidenceIDs))
}

// LLMJudge evaluates answers using an LLM as judge.
type LLMJudge struct {
	model model.Model
}

// NewLLMJudge creates a new LLM judge.
func NewLLMJudge(m model.Model) *LLMJudge {
	return &LLMJudge{model: m}
}

// LLMJudgeResult holds the result of LLM evaluation.
type LLMJudgeResult struct {
	Correct    bool    `json:"correct"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

const llmJudgePrompt = `You are an expert evaluator. Given a question, a ground
truth answer, and a predicted answer, determine if the predicted answer is
correct.

Question: %s
Ground Truth: %s
Predicted: %s

Is the predicted answer correct? Consider semantic equivalence, not just exact
match. Respond with a JSON object:
{"correct": true/false, "confidence": 0.0-1.0, "reason": "brief explanation"}`

// Evaluate uses the LLM to judge if the prediction is correct.
func (j *LLMJudge) Evaluate(
	ctx context.Context,
	question, groundTruth, prediction string,
) (*LLMJudgeResult, error) {
	if j.model == nil {
		return nil, fmt.Errorf("LLM model not configured")
	}
	prompt := fmt.Sprintf(llmJudgePrompt, question, groundTruth, prediction)
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: prompt},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}
	respCh, err := j.model.GenerateContent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("generate content: %w", err)
	}
	var content strings.Builder
	for resp := range respCh {
		if resp.Error != nil {
			return nil, fmt.Errorf("response error: %s", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			content.WriteString(resp.Choices[0].Message.Content)
		}
	}
	return parseJudgeResponse(content.String())
}

// EvaluateBatch evaluates multiple predictions and returns average score.
func (j *LLMJudge) EvaluateBatch(
	ctx context.Context,
	items []struct {
		Question    string
		GroundTruth string
		Prediction  string
	},
) (float64, error) {
	if len(items) == 0 {
		return 0, nil
	}
	var totalScore float64
	for _, item := range items {
		result, err := j.Evaluate(ctx, item.Question, item.GroundTruth, item.Prediction)
		if err != nil {
			return 0, err
		}
		if result.Correct {
			totalScore += result.Confidence
		}
	}
	return totalScore / float64(len(items)), nil
}

func parseJudgeResponse(content string) (*LLMJudgeResult, error) {
	// Extract JSON from response.
	content = strings.TrimSpace(content)
	// Try to find JSON block.
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		content = content[start : end+1]
	}
	var result LLMJudgeResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		// Fallback: try to parse manually.
		lower := strings.ToLower(content)
		result.Correct = strings.Contains(lower, "true") ||
			strings.Contains(lower, "correct")
		result.Confidence = 0.5
		result.Reason = "Failed to parse JSON response"
	}
	// Normalize confidence to [0, 1] range.
	if result.Confidence < 0 {
		result.Confidence = -result.Confidence
	}
	if result.Confidence > 1 {
		result.Confidence = 1
	}
	return &result, nil
}

// normalizeAndTokenize normalizes text and splits into tokens.
// Follows LoCoMo paper: lowercase, remove punctuation, split by whitespace.
func normalizeAndTokenize(text string) []string {
	if text == "" {
		return nil
	}
	// Replace <｜end▁of▁sentence｜> to space.
	text = strings.ReplaceAll(text, "<｜end▁of▁sentence｜>", " ")
	// Lowercase.
	text = strings.ToLower(text)
	// Remove punctuation.
	text = removePunctuation(text)
	// Split by whitespace.
	fields := strings.Fields(text)
	// Filter empty strings and stop words.
	result := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" && !isStopWord(f) {
			result = append(result, f)
		}
	}
	return result
}

var punctuationRegex = regexp.MustCompile(`[^\w\s]`)

func removePunctuation(text string) string {
	return punctuationRegex.ReplaceAllString(text, " ")
}

func isStopWord(word string) bool {
	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "must": true, "shall": true,
		"i": true, "you": true, "he": true, "she": true, "it": true,
		"we": true, "they": true, "me": true, "him": true, "her": true,
		"us": true, "them": true, "my": true, "your": true, "his": true,
		"its": true, "our": true, "their": true,
		"this": true, "that": true, "these": true, "those": true,
		"and": true, "or": true, "but": true, "if": true, "because": true,
		"as": true, "until": true, "while": true, "of": true, "at": true,
		"by": true, "for": true, "with": true, "about": true, "against": true,
		"between": true, "into": true, "through": true, "during": true,
		"before": true, "after": true, "above": true, "below": true,
		"to": true, "from": true, "up": true, "down": true, "in": true,
		"out": true, "on": true, "off": true, "over": true, "under": true,
		"again": true, "further": true, "then": true, "once": true,
	}
	return stopWords[word]
}

func countCommonTokens(pred, gt []string) int {
	gtCounts := make(map[string]int)
	for _, t := range gt {
		gtCounts[t]++
	}
	common := 0
	for _, t := range pred {
		if gtCounts[t] > 0 {
			common++
			gtCounts[t]--
		}
	}
	return common
}

func brevityPenalty(predLen, gtLen int) float64 {
	if predLen >= gtLen {
		return 1.0
	}
	return math.Exp(1 - float64(gtLen)/float64(predLen))
}

func calculateRougeN(pred, gt []string, n int) float64 {
	if len(pred) < n || len(gt) < n {
		if len(pred) == 0 && len(gt) == 0 {
			return 1.0
		}
		return 0.0
	}
	predNgrams := extractNgrams(pred, n)
	gtNgrams := extractNgrams(gt, n)
	// Count matches.
	matches := 0
	for ngram, count := range predNgrams {
		if gtCount, ok := gtNgrams[ngram]; ok {
			if count < gtCount {
				matches += count
			} else {
				matches += gtCount
			}
		}
	}
	totalPred := 0
	for _, c := range predNgrams {
		totalPred += c
	}
	totalGt := 0
	for _, c := range gtNgrams {
		totalGt += c
	}
	if totalPred == 0 || totalGt == 0 {
		return 0.0
	}
	precision := float64(matches) / float64(totalPred)
	recall := float64(matches) / float64(totalGt)
	if precision+recall == 0 {
		return 0.0
	}
	return 2 * precision * recall / (precision + recall)
}

func extractNgrams(tokens []string, n int) map[string]int {
	ngrams := make(map[string]int)
	for i := 0; i <= len(tokens)-n; i++ {
		ngram := strings.Join(tokens[i:i+n], " ")
		ngrams[ngram]++
	}
	return ngrams
}

func lcsLength(a, b []string) int {
	m, n := len(a), len(b)
	// Use space-optimized LCS.
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				if curr[j-1] > prev[j] {
					curr[j] = curr[j-1]
				} else {
					curr[j] = prev[j]
				}
			}
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

// AggregateMetrics aggregates metrics across multiple evaluations.
type AggregateMetrics struct {
	samples []QAMetrics
}

// NewAggregateMetrics creates a new aggregate metrics collector.
func NewAggregateMetrics() *AggregateMetrics {
	return &AggregateMetrics{}
}

// Add adds a new sample to the aggregate.
func (a *AggregateMetrics) Add(m QAMetrics) {
	a.samples = append(a.samples, m)
}

// Average returns the average of all samples.
func (a *AggregateMetrics) Average() QAMetrics {
	if len(a.samples) == 0 {
		return QAMetrics{}
	}
	var sumF1, sumBLEU, sumLLM float64
	for _, m := range a.samples {
		sumF1 += m.F1
		sumBLEU += m.BLEU
		sumLLM += m.LLMScore
	}
	n := float64(len(a.samples))
	return QAMetrics{
		F1:       sumF1 / n,
		BLEU:     sumBLEU / n,
		LLMScore: sumLLM / n,
	}
}

// Count returns the number of samples.
func (a *AggregateMetrics) Count() int {
	return len(a.samples)
}

// CategoryAggregator aggregates metrics by QA category.
type CategoryAggregator struct {
	categories map[string]*AggregateMetrics
}

// NewCategoryAggregator creates a new category aggregator.
func NewCategoryAggregator() *CategoryAggregator {
	return &CategoryAggregator{
		categories: make(map[string]*AggregateMetrics),
	}
}

// Add adds a metric to a specific category.
func (c *CategoryAggregator) Add(category string, m QAMetrics) {
	if c.categories[category] == nil {
		c.categories[category] = NewAggregateMetrics()
	}
	c.categories[category].Add(m)
}

// GetCategoryMetrics returns metrics for all categories.
func (c *CategoryAggregator) GetCategoryMetrics() map[string]CategoryMetrics {
	result := make(map[string]CategoryMetrics)
	for cat, agg := range c.categories {
		avg := agg.Average()
		result[cat] = CategoryMetrics{
			Count:    agg.Count(),
			F1:       avg.F1,
			BLEU:     avg.BLEU,
			LLMScore: avg.LLMScore,
		}
	}
	return result
}

// GetOverall returns overall metrics across all categories.
func (c *CategoryAggregator) GetOverall() CategoryMetrics {
	var total QAMetrics
	var count int
	for _, agg := range c.categories {
		avg := agg.Average()
		n := agg.Count()
		total.F1 += avg.F1 * float64(n)
		total.BLEU += avg.BLEU * float64(n)
		total.LLMScore += avg.LLMScore * float64(n)
		count += n
	}
	if count == 0 {
		return CategoryMetrics{}
	}
	return CategoryMetrics{
		Count:    count,
		F1:       total.F1 / float64(count),
		BLEU:     total.BLEU / float64(count),
		LLMScore: total.LLMScore / float64(count),
	}
}

// GetCategories returns sorted list of category names.
func (c *CategoryAggregator) GetCategories() []string {
	cats := make([]string, 0, len(c.categories))
	for cat := range c.categories {
		cats = append(cats, cat)
	}
	sort.Strings(cats)
	return cats
}

// TokenCounter counts tokens in text (simple word-based estimation).
type TokenCounter struct{}

// NewTokenCounter creates a new token counter.
func NewTokenCounter() *TokenCounter {
	return &TokenCounter{}
}

// Count estimates the number of tokens in the given text.
// This is a simple word-based estimation; actual tokenization depends on
// the specific model's tokenizer.
func (t *TokenCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	// Simple estimation: count words and punctuation.
	count := 0
	inWord := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if inWord {
				count++
				inWord = false
			}
		} else if unicode.IsPunct(r) {
			if inWord {
				count++
				inWord = false
			}
			count++
		} else {
			inWord = true
		}
	}
	if inWord {
		count++
	}
	return count
}
