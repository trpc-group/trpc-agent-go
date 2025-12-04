//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rubicresponse

import (
	"context"
	"fmt"
	"regexp"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var rubricBlockRegex = regexp.MustCompile(
	`(?ms)ID:\s*(\d+)\s*` + // 1: ID 数字
		`Property:\s*(.*?)\s*` + // 2: Property 内容
		`Evidence:\s*(.*?)\s*` + // 3: Evidence 内容
		`Reason:\s*(.*?)\s*` + // 4: Reason 内容
		`Verdict:\s*(.*?)\s*$`, // 5: Verdict 内容（yes / no）
)

type rubicResponseScorer struct {
}

func New() responsescorer.ResponseScorer {
	return &rubicResponseScorer{}
}

func (e *rubicResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	_ *metric.EvalMetric) (*evalresult.ScoreResult, error) {
	content := response.Choices[0].Message.Content
	matches := rubricBlockRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no rubric blocks found in response")
	}
	result := &evalresult.ScoreResult{}
	averageScore := 0.0
	for _, match := range matches {
		rubricID := match[1]
		reason := match[4]
		verdict := match[5]
		var score float64
		if verdict == "yes" {
			score = 1.0
		} else {
			score = 0.0
		}
		result.RubricScores = append(result.RubricScores, &evalresult.RubricScore{
			ID:     rubricID,
			Reason: reason,
			Score:  &score,
		})
		averageScore += score
	}
	averageScore /= float64(len(matches))
	result.Score = averageScore
	return result, nil
}
