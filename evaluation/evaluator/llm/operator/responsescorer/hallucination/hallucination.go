//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package hallucination scores sentence-level hallucination judgments.
package hallucination

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	positiveVerdict    = "yes"
	labelSupported     = "supported"
	labelUnsupported   = "unsupported"
	labelContradictory = "contradictory"
	labelDisputed      = "disputed"
	labelNotApplicable = "not_applicable"
)

var sentenceBlockRegex = regexp.MustCompile(
	`(?ms)ID:\s*(.*?)\s*` +
		`Reason:\s*(.*?)\s*` +
		`Label:\s*(.*?)\s*` +
		`Verdict:\s*(.*?)(?:\n\s*\n|\z)`,
)

type hallucinationResponseScorer struct {
}

// New returns a response scorer for hallucination judgments.
func New() responsescorer.ResponseScorer {
	return &hallucinationResponseScorer{}
}

// ScoreBasedOnResponse scores hallucination judgments by averaging sentence verdicts.
func (e *hallucinationResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	if response == nil {
		return nil, fmt.Errorf("response is nil")
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	content := response.Choices[0].Message.Content
	matches := sentenceBlockRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no sentence blocks found in response")
	}
	result := &evaluator.ScoreResult{}
	reasons := make([]string, 0, len(matches))
	total := 0.0
	for i, match := range matches {
		id := strings.TrimSpace(match[1])
		if id == "" {
			id = fmt.Sprintf("%d", i+1)
		}
		reason := strings.TrimSpace(match[2])
		label := normalizeLabel(match[3])
		verdict := strings.ToLower(strings.TrimSpace(match[4]))
		score, err := scoreForLabel(label, verdict)
		if err != nil {
			return nil, fmt.Errorf("score sentence %s: %w", id, err)
		}
		annotatedReason := reason
		if label != "" {
			annotatedReason = fmt.Sprintf("[%s] %s", label, reason)
		}
		result.RubricScores = append(result.RubricScores, &evalresult.RubricScore{
			ID:     id,
			Reason: strings.TrimSpace(annotatedReason),
			Score:  score,
		})
		total += score
		reasons = append(reasons, strings.TrimSpace(annotatedReason))
	}
	result.Score = total / float64(len(matches))
	result.Reason = strings.Join(reasons, "\n")
	return result, nil
}

func normalizeLabel(label string) string {
	normalized := strings.ToLower(strings.TrimSpace(label))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}

func scoreForLabel(label, verdict string) (float64, error) {
	switch label {
	case labelSupported, labelNotApplicable:
		return 1.0, nil
	case labelUnsupported, labelContradictory, labelDisputed:
		return 0.0, nil
	case "":
		if verdict == positiveVerdict {
			return 1.0, nil
		}
		return 0.0, nil
	default:
		return 0, fmt.Errorf("unexpected label %q", label)
	}
}
