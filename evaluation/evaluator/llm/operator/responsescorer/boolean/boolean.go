//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package boolean scores JSON judge outputs shaped as {passed, reason}.
package boolean

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/internal/responsejson"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type booleanResponse struct {
	Passed *bool   `json:"passed"`
	Reason *string `json:"reason"`
}

type booleanResponseScorer struct {
}

// New returns a response scorer for boolean JSON outputs.
func New() responsescorer.ResponseScorer {
	return &booleanResponseScorer{}
}

// ScoreBasedOnResponse parses the structured judge response.
func (s *booleanResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	_ *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	var payload booleanResponse
	if err := responsejson.UnmarshalContent(response, &payload); err != nil {
		return nil, err
	}
	if payload.Passed == nil {
		return nil, fmt.Errorf("passed is required")
	}
	if payload.Reason == nil {
		return nil, fmt.Errorf("reason is required")
	}
	scoreValue := 0.0
	if *payload.Passed {
		scoreValue = 1.0
	}
	return &evaluator.ScoreResult{
		Score:  scoreValue,
		Value:  &score.Value{Kind: score.KindBoolean, Boolean: payload.Passed},
		Reason: *payload.Reason,
	}, nil
}
