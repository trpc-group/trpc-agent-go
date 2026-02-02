//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package retention provides Information Retention evaluator.
// Based on τ²-bench CommunicateEvaluator methodology.
// Checks if key information from baseline is preserved in summary mode.
package retention

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// RetentionEvaluator evaluates information retention between baseline and
// summary modes. Inspired by τ²-bench CommunicateEvaluator.
type RetentionEvaluator struct {
	name      string
	llmJudge  model.Model
	threshold float64
}

// Option configures RetentionEvaluator.
type Option func(*RetentionEvaluator)

// WithName sets the evaluator name.
func WithName(name string) Option {
	return func(e *RetentionEvaluator) {
		e.name = name
	}
}

// WithThreshold sets the retention threshold.
func WithThreshold(threshold float64) Option {
	return func(e *RetentionEvaluator) {
		e.threshold = threshold
	}
}

// New creates a new RetentionEvaluator.
// llmJudge can be nil for rule-based evaluation.
func New(llmJudge model.Model, opts ...Option) *RetentionEvaluator {
	e := &RetentionEvaluator{
		name:      "information_retention",
		llmJudge:  llmJudge,
		threshold: 0.7,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name returns the evaluator name.
func (e *RetentionEvaluator) Name() string {
	return e.name
}

// Description returns the evaluator description.
func (e *RetentionEvaluator) Description() string {
	return "Evaluates information retention between baseline and summary modes"
}

// RetentionResult contains detailed retention analysis.
type RetentionResult struct {
	// KeyInfoCount is the number of key information items extracted.
	KeyInfoCount int `json:"keyInfoCount"`
	// RetainedCount is the number of retained information items.
	RetainedCount int `json:"retainedCount"`
	// RetentionRate is the ratio of retained to total key information.
	RetentionRate float64 `json:"retentionRate"`
	// MissingInfo lists information that was lost.
	MissingInfo []string `json:"missingInfo,omitempty"`
	// PerTurnRetention shows retention rate per conversation turn.
	PerTurnRetention []float64 `json:"perTurnRetention,omitempty"`
}

// Evaluate implements Evaluator interface.
func (e *RetentionEvaluator) Evaluate(
	ctx context.Context,
	actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	result, err := e.evaluateRetention(ctx, expecteds, actuals)
	if err != nil {
		return nil, err
	}
	evalStatus := status.EvalStatusFailed
	if result.RetentionRate >= e.threshold {
		evalStatus = status.EvalStatusPassed
	}
	return &evaluator.EvaluateResult{
		OverallScore:  result.RetentionRate,
		OverallStatus: evalStatus,
		Details: map[string]any{
			"key_info_count":     result.KeyInfoCount,
			"retained_count":     result.RetainedCount,
			"retention_rate":     result.RetentionRate,
			"missing_info":       result.MissingInfo,
			"per_turn_retention": result.PerTurnRetention,
		},
	}, nil
}

// EvaluateMultiRun evaluates retention across multiple runs.
func (e *RetentionEvaluator) EvaluateMultiRun(
	ctx context.Context,
	baselineRuns, testRuns [][]*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	if len(baselineRuns) == 0 || len(testRuns) == 0 {
		return &evaluator.EvaluateResult{
			OverallScore:  0,
			OverallStatus: status.EvalStatusNotEvaluated,
		}, nil
	}
	baseline := baselineRuns[0]
	var totalRetention float64
	var totalKeyInfo, totalRetained int
	allMissing := make([]string, 0)
	perRunRetention := make([]float64, 0, len(testRuns))
	for _, testRun := range testRuns {
		result, err := e.evaluateRetention(ctx, baseline, testRun)
		if err != nil {
			return nil, err
		}
		totalRetention += result.RetentionRate
		totalKeyInfo += result.KeyInfoCount
		totalRetained += result.RetainedCount
		allMissing = append(allMissing, result.MissingInfo...)
		perRunRetention = append(perRunRetention, result.RetentionRate)
	}
	avgRetention := totalRetention / float64(len(testRuns))
	evalStatus := status.EvalStatusFailed
	if avgRetention >= e.threshold {
		evalStatus = status.EvalStatusPassed
	}
	// Deduplicate missing info.
	uniqueMissing := deduplicateStrings(allMissing)
	return &evaluator.EvaluateResult{
		OverallScore:  avgRetention,
		OverallStatus: evalStatus,
		Details: map[string]any{
			"avg_retention_rate": avgRetention,
			"total_key_info":     totalKeyInfo,
			"total_retained":     totalRetained,
			"per_run_retention":  perRunRetention,
			"unique_missing":     uniqueMissing,
			"num_runs":           len(testRuns),
		},
	}, nil
}

func (e *RetentionEvaluator) evaluateRetention(
	ctx context.Context,
	baseline, test []*evalset.Invocation,
) (*RetentionResult, error) {
	if len(baseline) == 0 {
		return &RetentionResult{RetentionRate: 1.0}, nil
	}
	// Use LLM if available for semantic extraction.
	if e.llmJudge != nil {
		return e.llmEvaluateRetention(ctx, baseline, test)
	}
	// Rule-based evaluation.
	return e.ruleBasedEvaluateRetention(baseline, test), nil
}

// ruleBasedEvaluateRetention extracts key information using rules.
func (e *RetentionEvaluator) ruleBasedEvaluateRetention(
	baseline, test []*evalset.Invocation,
) *RetentionResult {
	result := &RetentionResult{
		PerTurnRetention: make([]float64, 0, len(baseline)),
		MissingInfo:      make([]string, 0),
	}
	minLen := len(baseline)
	if len(test) < minLen {
		minLen = len(test)
	}
	for i := 0; i < minLen; i++ {
		baselineResp := getResponseContent(baseline[i])
		testResp := getResponseContent(test[i])
		// Extract key information from baseline response.
		keyInfo := extractKeyInformation(baselineResp)
		result.KeyInfoCount += len(keyInfo)
		// Check retention in test response.
		retained := 0
		for _, info := range keyInfo {
			if isInfoRetained(info, testResp) {
				retained++
			} else {
				result.MissingInfo = append(result.MissingInfo,
					fmt.Sprintf("Turn %d: %s", i+1, truncate(info, 50)))
			}
		}
		result.RetainedCount += retained
		// Per-turn retention.
		turnRetention := 1.0
		if len(keyInfo) > 0 {
			turnRetention = float64(retained) / float64(len(keyInfo))
		}
		result.PerTurnRetention = append(result.PerTurnRetention, turnRetention)
	}
	// Penalize for missing turns.
	if len(test) < len(baseline) {
		for i := len(test); i < len(baseline); i++ {
			result.PerTurnRetention = append(result.PerTurnRetention, 0.0)
			baselineResp := getResponseContent(baseline[i])
			keyInfo := extractKeyInformation(baselineResp)
			result.KeyInfoCount += len(keyInfo)
			for _, info := range keyInfo {
				result.MissingInfo = append(result.MissingInfo,
					fmt.Sprintf("Turn %d (missing): %s", i+1, truncate(info, 50)))
			}
		}
	}
	// Calculate overall retention rate.
	if result.KeyInfoCount > 0 {
		result.RetentionRate = float64(result.RetainedCount) /
			float64(result.KeyInfoCount)
	} else {
		result.RetentionRate = 1.0
	}
	return result
}

// llmEvaluateRetention uses LLM for semantic retention evaluation.
func (e *RetentionEvaluator) llmEvaluateRetention(
	ctx context.Context,
	baseline, test []*evalset.Invocation,
) (*RetentionResult, error) {
	result := &RetentionResult{
		PerTurnRetention: make([]float64, 0, len(baseline)),
		MissingInfo:      make([]string, 0),
	}
	minLen := min(len(test), len(baseline))
	for i := range minLen {
		baselineResp := getResponseContent(baseline[i])
		testResp := getResponseContent(test[i])
		turnRetention, missing, err := e.llmCompareTurn(
			ctx, baselineResp, testResp, i+1)
		if err != nil {
			// Fallback to rule-based on error.
			keyInfo := extractKeyInformation(baselineResp)
			retained := 0
			for _, info := range keyInfo {
				if isInfoRetained(info, testResp) {
					retained++
				}
			}
			if len(keyInfo) > 0 {
				turnRetention = float64(retained) / float64(len(keyInfo))
			} else {
				turnRetention = 1.0
			}
		}
		result.PerTurnRetention = append(result.PerTurnRetention, turnRetention)
		result.MissingInfo = append(result.MissingInfo, missing...)
		// Estimate key info count (LLM doesn't give exact count).
		const estimatedKeyInfoPerTurn = 5
		result.KeyInfoCount += estimatedKeyInfoPerTurn
		result.RetainedCount += int(turnRetention * estimatedKeyInfoPerTurn)
	}
	// Handle missing turns.
	if len(test) < len(baseline) {
		for i := len(test); i < len(baseline); i++ {
			result.PerTurnRetention = append(result.PerTurnRetention, 0.0)
			result.MissingInfo = append(result.MissingInfo,
				fmt.Sprintf("Turn %d: entire response missing", i+1))
			const estimatedKeyInfoPerTurn = 5
			result.KeyInfoCount += estimatedKeyInfoPerTurn
		}
	}
	// Calculate overall retention rate.
	if len(result.PerTurnRetention) > 0 {
		var sum float64
		for _, r := range result.PerTurnRetention {
			sum += r
		}
		result.RetentionRate = sum / float64(len(result.PerTurnRetention))
	} else {
		result.RetentionRate = 1.0
	}
	return result, nil
}

// llmCompareTurn uses LLM to compare a single turn.
func (e *RetentionEvaluator) llmCompareTurn(
	ctx context.Context,
	baseline, test string,
	turnNum int,
) (float64, []string, error) {
	if baseline == "" && test == "" {
		return 1.0, nil, nil
	}
	if baseline == "" {
		return 1.0, nil, nil
	}
	if test == "" {
		return 0.0, []string{fmt.Sprintf("Turn %d: response missing", turnNum)}, nil
	}
	prompt := `You are evaluating information retention between two AI responses.

## Baseline Response (Full History)
` + baseline + `

## Test Response (Summary Mode)
` + test + `

## Task
Evaluate what percentage of KEY INFORMATION from the baseline is preserved in 
the test response. Key information includes:
- Specific facts, numbers, dates, names
- Main conclusions or recommendations  
- Important context or constraints
- Action items or next steps

## Response Format
RETENTION_RATE: [0.0 to 1.0]
MISSING: [comma-separated list of missing information, or "none"]
`
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: prompt},
		},
	}
	respCh, err := e.llmJudge.GenerateContent(ctx, request)
	if err != nil {
		return 0, nil, err
	}
	var content string
	for resp := range respCh {
		if resp.Error != nil {
			return 0, nil, fmt.Errorf("LLM error: %s", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			content += resp.Choices[0].Message.Content
		}
	}
	return parseRetentionResponse(content, turnNum)
}

func parseRetentionResponse(
	content string,
	turnNum int,
) (float64, []string, error) {
	const defaultRate = 0.8
	lines := strings.Split(content, "\n")
	rate := defaultRate
	var missing []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "RETENTION_RATE:"); ok {
			val := strings.TrimSpace(after)
			if parsed, err := parseFloat(val); err == nil {
				rate = clamp(parsed, 0, 1)
			}
		}
		if after, ok := strings.CutPrefix(line, "MISSING:"); ok {
			val := strings.TrimSpace(after)
			if val != "" && strings.ToLower(val) != "none" {
				parts := strings.Split(val, ",")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						missing = append(missing,
							fmt.Sprintf("Turn %d: %s", turnNum, p))
					}
				}
			}
		}
	}
	return rate, missing, nil
}

// Pre-compiled regex patterns for extractKeyInformation.
var (
	numberPattern     = regexp.MustCompile(`\b\d+[\d,\.]*\b`)
	quotePattern      = regexp.MustCompile(`["']([^"']+)["']`)
	properNounPattern = regexp.MustCompile(`\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+)*\b`)
)

// extractKeyInformation extracts key information from text.
func extractKeyInformation(text string) []string {
	if text == "" {
		return nil
	}
	info := make([]string, 0)
	// Extract numbers (dates, amounts, etc.).
	numbers := numberPattern.FindAllString(text, -1)
	const minNumberLength = 2
	for _, n := range numbers {
		if len(n) >= minNumberLength {
			info = append(info, n)
		}
	}
	// Extract quoted content.
	quotes := quotePattern.FindAllStringSubmatch(text, -1)
	const minQuoteLength = 3
	for _, q := range quotes {
		if len(q) > 1 && len(q[1]) >= minQuoteLength {
			info = append(info, q[1])
		}
	}
	// Extract capitalized proper nouns (simple heuristic).
	nouns := properNounPattern.FindAllString(text, -1)
	const minNounLength = 3
	for _, n := range nouns {
		if len(n) >= minNounLength && !isCommonWord(n) {
			info = append(info, n)
		}
	}
	// Deduplicate and limit.
	info = deduplicateStrings(info)
	const maxKeyInfo = 10
	if len(info) > maxKeyInfo {
		info = info[:maxKeyInfo]
	}
	return info
}

// isInfoRetained checks if information is retained in the response.
func isInfoRetained(info, response string) bool {
	if info == "" || response == "" {
		return false
	}
	infoLower := strings.ToLower(info)
	responseLower := strings.ToLower(response)
	// Exact match.
	if strings.Contains(responseLower, infoLower) {
		return true
	}
	// Fuzzy match for numbers (allow different formats).
	if isNumeric(info) {
		normalized := normalizeNumber(info)
		return strings.Contains(responseLower, normalized)
	}
	return false
}

func getResponseContent(inv *evalset.Invocation) string {
	if inv == nil || inv.FinalResponse == nil {
		return ""
	}
	return inv.FinalResponse.Content
}

func isNumeric(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && c != '.' && c != ',' {
			return false
		}
	}
	return true
}

func normalizeNumber(s string) string {
	return strings.ReplaceAll(s, ",", "")
}

func isCommonWord(s string) bool {
	common := map[string]bool{
		"The": true, "This": true, "That": true, "These": true,
		"There": true, "Then": true, "When": true, "Where": true,
		"What": true, "Which": true, "How": true, "Why": true,
		"And": true, "But": true, "For": true, "With": true,
		"From": true, "Into": true, "About": true, "After": true,
		"Before": true, "During": true, "Without": true,
	}
	return common[s]
}

func deduplicateStrings(input []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(input))
	for _, s := range input {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

func clamp(v, minVal, maxVal float64) float64 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}
