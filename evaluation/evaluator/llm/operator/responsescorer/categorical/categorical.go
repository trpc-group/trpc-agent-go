//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package categorical scores JSON judge outputs shaped as {category, reason}.
package categorical

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/internal/category"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/internal/responsejson"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type categoricalResponse struct {
	Category *string `json:"category"`
	Reason   *string `json:"reason"`
}

type categoricalResponseScorer struct {
}

// New returns a response scorer for categorical JSON outputs.
func New() responsescorer.ResponseScorer {
	return &categoricalResponseScorer{}
}

// ScoreBasedOnResponse parses the structured judge response.
func (s *categoricalResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	categoryScores, err := category.Scores(evalMetric)
	if err != nil {
		return nil, err
	}
	var payload categoricalResponse
	if err := responsejson.UnmarshalContent(response, &payload); err != nil {
		return nil, err
	}
	if payload.Category == nil {
		return nil, fmt.Errorf("category is required")
	}
	if payload.Reason == nil {
		return nil, fmt.Errorf("reason is required")
	}
	value := &score.Value{Kind: score.KindCategorical, Categorical: *payload.Category}
	categoryScore, ok := categoryScores[*payload.Category]
	if !ok {
		failedStatus := status.EvalStatusFailed
		return &evaluator.ScoreResult{
			Score:  0,
			Status: &failedStatus,
			Value:  value,
			Reason: fmt.Sprintf("unknown categorical label %q: %s", *payload.Category, *payload.Reason),
		}, nil
	}
	return &evaluator.ScoreResult{
		Score:  categoryScore,
		Value:  value,
		Reason: *payload.Reason,
	}, nil
}
