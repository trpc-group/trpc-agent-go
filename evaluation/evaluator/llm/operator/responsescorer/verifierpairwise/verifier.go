//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package verifierpairwise scores pairwise LLM verifier judge outputs.
package verifierpairwise

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	defaultGranularity  = 20
	defaultTieScore     = 0.5
	scoreTokenFirstRune = 'A'
	scoreATag           = "<score_A>"
	scoreBTag           = "<score_B>"
)

type verifierResponseScorer struct {
}

// New returns a response scorer for pairwise verifier outputs.
func New() responsescorer.ResponseScorer {
	return &verifierResponseScorer{}
}

// ScoreBasedOnResponse scores pairwise verifier responses.
func (s *verifierResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	_ *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	score, reason, err := scoreFromLogprobs(response, defaultGranularity)
	if err != nil {
		return nil, err
	}
	return &evaluator.ScoreResult{Score: score, Reason: reason}, nil
}

func scoreFromLogprobs(response *model.Response, granularity int) (float64, string, error) {
	if response == nil || len(response.Choices) == 0 || response.Choices[0].Logprobs == nil {
		return 0, "", errors.New("logprobs are missing")
	}
	scoreA, err := scoreForTagFromLogprobs(response.Choices[0].Logprobs.Content, scoreATag, granularity)
	if err != nil {
		return 0, "", fmt.Errorf("score_A: %w", err)
	}
	scoreB, err := scoreForTagFromLogprobs(response.Choices[0].Logprobs.Content, scoreBTag, granularity)
	if err != nil {
		return 0, "", fmt.Errorf("score_B: %w", err)
	}
	return pairwisePreferenceScore(scoreA, scoreB),
		fmt.Sprintf("Computed from score_A %.4f and score_B %.4f log probabilities.", scoreA, scoreB),
		nil
}

func scoreForTagFromLogprobs(tokens []model.TokenLogprob, tag string, granularity int) (float64, error) {
	if len(tokens) == 0 {
		return 0, errors.New("token logprobs are empty")
	}
	var textSoFar strings.Builder
	for i, token := range tokens {
		textBeforeLen := textSoFar.Len()
		textSoFar.WriteString(token.Token)
		currentText := textSoFar.String()
		tagStart := scoreTagStart(currentText, textBeforeLen, tag)
		if tagStart < 0 {
			continue
		}
		tagEnd := tagStart + len(tag)
		if tagEnd < len(currentText) {
			prefixLen := tagEnd - textBeforeLen
			dist := scoreTokenDistributionFromTokenPrefix(token, prefixLen, granularity)
			if dist == nil {
				return 0, errors.New("score token logprobs are missing after tag")
			}
			return dist.expectedScore(granularity)
		}
		if i+1 >= len(tokens) {
			return 0, errors.New("score token is missing after tag")
		}
		dist := scoreTokenDistributionFromToken(tokens[i+1], granularity)
		if dist == nil {
			return 0, errors.New("score token logprobs are missing after tag")
		}
		return dist.expectedScore(granularity)
	}
	return 0, errors.New("score tag is missing")
}

func scoreTagStart(text string, textBeforeLen int, tag string) int {
	searchStart := textBeforeLen - len(tag) + 1
	if searchStart < 0 {
		searchStart = 0
	}
	idx := strings.Index(text[searchStart:], tag)
	if idx < 0 {
		return -1
	}
	return searchStart + idx
}

type scoreTokenDistribution struct {
	logprobs map[int]float64
}

func scoreTokenDistributionFromToken(token model.TokenLogprob, granularity int) *scoreTokenDistribution {
	return scoreTokenDistributionFromTokenPrefix(token, 0, granularity)
}

func scoreTokenDistributionFromTokenPrefix(token model.TokenLogprob, prefixLen int, granularity int) *scoreTokenDistribution {
	if prefixLen < 0 || prefixLen > len(token.Token) {
		return nil
	}
	prefix := token.Token[:prefixLen]
	dist := &scoreTokenDistribution{logprobs: make(map[int]float64)}
	if index, ok := scoreTokenIndexAfterPrefix(token.Token, prefix, granularity); ok {
		dist.logprobs[index] = token.Logprob
	}
	for _, top := range token.TopLogprobs {
		index, ok := scoreTokenIndexAfterPrefix(top.Token, prefix, granularity)
		if !ok {
			continue
		}
		if existing, exists := dist.logprobs[index]; !exists || top.Logprob > existing {
			dist.logprobs[index] = top.Logprob
		}
	}
	if len(dist.logprobs) == 0 {
		return nil
	}
	return dist
}

func scoreTokenIndexAfterPrefix(token string, prefix string, granularity int) (int, bool) {
	if !strings.HasPrefix(token, prefix) {
		return 0, false
	}
	return scoreTokenIndex(token[len(prefix):], granularity)
}

func (d *scoreTokenDistribution) expectedScore(granularity int) (float64, error) {
	maxLogprob := math.Inf(-1)
	for _, logprob := range d.logprobs {
		if math.IsNaN(logprob) {
			return 0, errors.New("score token logprob is NaN")
		}
		if logprob > maxLogprob {
			maxLogprob = logprob
		}
	}
	if math.IsInf(maxLogprob, -1) {
		return 0, errors.New("score token logprobs are all negative infinity")
	}
	totalWeight := 0.0
	totalScore := 0.0
	for index, logprob := range d.logprobs {
		weight := math.Exp(logprob - maxLogprob)
		score, _ := scoreForIndex(index, granularity)
		totalWeight += weight
		totalScore += weight * score
	}
	return totalScore / totalWeight, nil
}

func pairwisePreferenceScore(scoreA float64, scoreB float64) float64 {
	score := defaultTieScore + (scoreA-scoreB)/2
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func scoreForIndex(index int, granularity int) (float64, bool) {
	if granularity <= 1 || index < 0 || index >= granularity {
		return 0, false
	}
	return 1 - float64(index)/float64(granularity-1), true
}

func scoreTokenIndex(token string, granularity int) (int, bool) {
	normalized := normalizeScoreToken(token)
	if len(normalized) != 1 {
		return 0, false
	}
	r := rune(normalized[0])
	index := int(r - scoreTokenFirstRune)
	if index < 0 || index >= granularity {
		return 0, false
	}
	return index, true
}

func normalizeScoreToken(token string) string {
	return strings.ToUpper(strings.TrimSpace(token))
}
