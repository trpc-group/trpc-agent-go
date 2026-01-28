//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summary provides Summary-specific evaluators.
package summary

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator/comparator"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// SummaryComparator compares conversations for summary evaluation.
// It combines tool trajectory comparison with semantic similarity.
type SummaryComparator struct {
	toolComparator *comparator.ToolTrajectoryComparator
	llmJudge       model.Model
	threshold      float64
}

// NewSummaryComparator creates a new SummaryComparator.
func NewSummaryComparator(
	llmJudge model.Model,
	threshold float64,
) *SummaryComparator {
	return &SummaryComparator{
		toolComparator: comparator.NewToolTrajectoryComparator(true, false),
		llmJudge:       llmJudge,
		threshold:      threshold,
	}
}

// IsConversationConsistent checks if two conversations are consistent.
func (c *SummaryComparator) IsConversationConsistent(
	ctx context.Context,
	expected, actual []*evalset.Invocation,
) (bool, float64, error) {
	if len(expected) == 0 && len(actual) == 0 {
		return true, 1.0, nil
	}
	if len(expected) != len(actual) {
		// Length mismatch, calculate partial score.
		minLen := len(expected)
		if len(actual) < minLen {
			minLen = len(actual)
		}
		if minLen == 0 {
			return false, 0.0, nil
		}
		score, err := c.compareInvocations(ctx, expected[:minLen], actual[:minLen])
		if err != nil {
			return false, 0, err
		}
		// Penalize for length mismatch.
		maxLen := len(expected)
		if len(actual) > maxLen {
			maxLen = len(actual)
		}
		lengthPenalty := float64(minLen) / float64(maxLen)
		adjustedScore := score * lengthPenalty
		return adjustedScore >= c.threshold, adjustedScore, nil
	}
	score, err := c.compareInvocations(ctx, expected, actual)
	if err != nil {
		return false, 0, err
	}
	return score >= c.threshold, score, nil
}

func (c *SummaryComparator) compareInvocations(
	ctx context.Context,
	expected, actual []*evalset.Invocation,
) (float64, error) {
	if len(expected) == 0 {
		return 1.0, nil
	}
	var totalScore float64
	for i := range expected {
		score, err := c.compareInvocation(ctx, expected[i], actual[i])
		if err != nil {
			return 0, err
		}
		totalScore += score
	}
	return totalScore / float64(len(expected)), nil
}

func (c *SummaryComparator) compareInvocation(
	ctx context.Context,
	expected, actual *evalset.Invocation,
) (float64, error) {
	// Weight: tool trajectory 40%, response similarity 60%.
	const toolWeight = 0.4
	const responseWeight = 0.6
	// 1. Compare tool trajectories.
	toolMatch, err := c.toolComparator.IsConsistent(ctx, expected, actual)
	if err != nil {
		return 0, err
	}
	toolScore := 0.0
	if toolMatch {
		toolScore = 1.0
	}
	// 2. Compare responses using LLM judge.
	responseScore, err := c.compareResponses(ctx, expected, actual)
	if err != nil {
		return 0, err
	}
	return toolWeight*toolScore + responseWeight*responseScore, nil
}

func (c *SummaryComparator) compareResponses(
	ctx context.Context,
	expected, actual *evalset.Invocation,
) (float64, error) {
	if expected.FinalResponse == nil && actual.FinalResponse == nil {
		return 1.0, nil
	}
	if expected.FinalResponse == nil || actual.FinalResponse == nil {
		return 0.0, nil
	}
	expectedContent := expected.FinalResponse.Content
	actualContent := actual.FinalResponse.Content
	// If LLM judge is available, use it for semantic comparison.
	if c.llmJudge != nil {
		score, err := c.llmSemanticSimilarity(ctx, expectedContent, actualContent)
		if err == nil {
			return score, nil
		}
		// Fall back to simple comparison on error.
	}
	// Simple similarity based on common words.
	return simpleTextSimilarity(expectedContent, actualContent), nil
}

// llmSemanticSimilarity uses LLM to judge semantic equivalence.
// This follows the rubric-based evaluation pattern from trpc-agent-go.
func (c *SummaryComparator) llmSemanticSimilarity(
	ctx context.Context,
	expected, actual string,
) (float64, error) {
	// Use a structured rubric-based prompt for more reliable scoring.
	prompt := `You are an expert evaluator comparing two AI assistant responses.

## Task
Evaluate whether Response B conveys the same key information and meaning as 
Response A. Focus on:
1. **Factual Accuracy**: Are the same facts, numbers, names preserved?
2. **Semantic Equivalence**: Do both responses answer the question similarly?
3. **Completeness**: Does Response B cover the same key points?

## Rubric
- 1.0: Semantically identical - same key information, may differ in wording
- 0.8: Mostly equivalent - minor differences that don't affect meaning
- 0.6: Partially equivalent - some key information differs or is missing
- 0.4: Weakly related - addresses same topic but different conclusions
- 0.2: Barely related - only superficial similarity
- 0.0: Completely different - contradictory or unrelated

## Response A (Baseline)
` + expected + `

## Response B (Summary Mode)
` + actual + `

## Evaluation
Provide your score as a single decimal number (0.0 to 1.0):
Score: `
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: prompt},
		},
	}
	respCh, err := c.llmJudge.GenerateContent(ctx, request)
	if err != nil {
		return 0, err
	}
	var content string
	for resp := range respCh {
		if resp.Error != nil {
			return 0, fmt.Errorf("LLM error: %s", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			content += resp.Choices[0].Message.Content
		}
	}
	// Parse score from response.
	score, err := parseScoreFromResponse(strings.TrimSpace(content))
	if err != nil {
		return 0.5, nil // Default to 0.5 on parse error.
	}
	return score, nil
}

// parseScoreFromResponse extracts a float score from LLM response.
func parseScoreFromResponse(s string) (float64, error) {
	// Try to find a number in the response.
	s = strings.TrimSpace(s)
	// Common patterns: "0.8", "Score: 0.8", "0.8/1.0".
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		// Remove common prefixes.
		for _, prefix := range []string{"Score:", "score:", "Rating:", "rating:"} {
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimPrefix(line, prefix)
				line = strings.TrimSpace(line)
			}
		}
		// Try to parse as float.
		if score, err := strconv.ParseFloat(line, 64); err == nil {
			return clampScore(score), nil
		}
		// Try to find first number-like pattern.
		for i := 0; i < len(line); i++ {
			if (line[i] >= '0' && line[i] <= '9') || line[i] == '.' {
				end := i
				for end < len(line) && ((line[end] >= '0' && line[end] <= '9') ||
					line[end] == '.') {
					end++
				}
				if score, err := strconv.ParseFloat(line[i:end], 64); err == nil {
					return clampScore(score), nil
				}
			}
		}
	}
	return 0, fmt.Errorf("no score found in response")
}

func clampScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func simpleTextSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}
	// Tokenize.
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}
	// Count common words.
	wordSetA := make(map[string]bool)
	for _, w := range wordsA {
		wordSetA[w] = true
	}
	common := 0
	for _, w := range wordsB {
		if wordSetA[w] {
			common++
		}
	}
	// Jaccard similarity.
	union := len(wordsA) + len(wordsB) - common
	if union == 0 {
		return 0.0
	}
	return float64(common) / float64(union)
}
