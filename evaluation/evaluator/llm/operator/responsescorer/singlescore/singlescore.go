//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package singlescore scores JSON judge outputs shaped as {score, reason}.
package singlescore

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/internal/responsejson"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type singleScoreResponse struct {
	Score  *float64 `json:"score"`
	Reason *string  `json:"reason"`
}

type singleScoreResponseScorer struct {
}

// New returns a response scorer for single score JSON outputs.
func New() responsescorer.ResponseScorer {
	return &singleScoreResponseScorer{}
}

// ScoreBasedOnResponse parses the structured judge response.
func (s *singleScoreResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	_ *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	var payload singleScoreResponse
	if err := responsejson.UnmarshalContent(response, &payload); err != nil {
		return nil, err
	}
	if payload.Score == nil {
		return nil, fmt.Errorf("score is required")
	}
	if payload.Reason == nil {
		return nil, fmt.Errorf("reason is required")
	}
	if *payload.Score < 0 || *payload.Score > 1 {
		return nil, fmt.Errorf("score must be between 0 and 1")
	}
	return &evaluator.ScoreResult{
		Score:  *payload.Score,
		Reason: *payload.Reason,
	}, nil
}
