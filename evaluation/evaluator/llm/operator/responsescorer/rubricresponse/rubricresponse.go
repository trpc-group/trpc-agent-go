//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rubricresponse scores rubric-graded judge outputs.
package rubricresponse

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

const passedVerdict = "yes"

// rubricBlockRegex extracts rubric ID, property, evidence, reason, and verdict blocks.
var rubricBlockRegex = regexp.MustCompile(
	`(?ms)ID:\s*(.*?)\s*` + // 1: rubric id
		`Rubric:\s*(.*?)\s*` + // 2: rubric text
		`Evidence:\s*(.*?)\s*` + // 3: evidence text
		`Reason:\s*(.*?)\s*` + // 4: reason text
		`Verdict:\s*(.*?)\s*$`, // 5: verdict yes/no
)

type rubricResponseScorer struct {
}

// New returns a response scorer for rubric responses.
func New() responsescorer.ResponseScorer {
	return &rubricResponseScorer{}
}

// ScoreBasedOnResponse scores rubric responses.
func (e *rubricResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	_ *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	content := response.Choices[0].Message.Content
	matches := rubricBlockRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no rubric blocks found in response")
	}
	averageScore := 0.0
	reasons := make([]string, 0, len(matches))
	result := &evaluator.ScoreResult{}
	for _, match := range matches {
		rubricID := strings.TrimSpace(match[1])
		reason := strings.TrimSpace(match[4])
		verdict := strings.ToLower(strings.TrimSpace(match[5]))
		var score float64
		if verdict == passedVerdict {
			score = 1.0
		} else {
			score = 0.0
		}
		result.RubricScores = append(result.RubricScores, &evalresult.RubricScore{
			ID:     rubricID,
			Reason: reason,
			Score:  score,
		})
		averageScore += score
		reasons = append(reasons, reason)
	}
	averageScore /= float64(len(matches))
	result.Score = averageScore
	result.Reason = strings.Join(reasons, "\n")
	return result, nil
}
