//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rubricscores scores JSON judge outputs shaped as {rubricScores: [...]}.
package rubricscores

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/internal/rubrics"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/internal/responsejson"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type rubricScoreItem struct {
	ID     *string  `json:"id"`
	Score  *float64 `json:"score"`
	Reason *string  `json:"reason"`
}

type rubricScoresResponse struct {
	RubricScores []rubricScoreItem `json:"rubricScores"`
}

type rubricScoresResponseScorer struct {
}

// New returns a response scorer for rubric scores JSON outputs.
func New() responsescorer.ResponseScorer {
	return &rubricScoresResponseScorer{}
}

// ScoreBasedOnResponse parses the structured judge response.
func (s *rubricScoresResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	var payload rubricScoresResponse
	if err := responsejson.UnmarshalContent(response, &payload); err != nil {
		return nil, err
	}
	if len(payload.RubricScores) == 0 {
		return nil, fmt.Errorf("rubricScores is empty")
	}
	expectedIDs, err := expectedRubricIDs(evalMetric)
	if err != nil {
		return nil, err
	}
	result := &evaluator.ScoreResult{
		RubricScores: make([]*evalresult.RubricScore, 0, len(payload.RubricScores)),
	}
	reasons := make([]string, 0, len(payload.RubricScores))
	seenIDs := make(map[string]struct{}, len(payload.RubricScores))
	total := 0.0
	for _, item := range payload.RubricScores {
		if item.ID == nil || strings.TrimSpace(*item.ID) == "" {
			return nil, fmt.Errorf("rubric score id is empty")
		}
		id := strings.TrimSpace(*item.ID)
		if err := validateRubricScoreID(id, seenIDs, expectedIDs); err != nil {
			return nil, err
		}
		if item.Score == nil {
			return nil, fmt.Errorf("rubric score is required")
		}
		if item.Reason == nil {
			return nil, fmt.Errorf("rubric score reason is required")
		}
		if *item.Score < 0 || *item.Score > 1 {
			return nil, fmt.Errorf("rubric score must be between 0 and 1")
		}
		result.RubricScores = append(result.RubricScores, &evalresult.RubricScore{
			ID:     id,
			Score:  *item.Score,
			Reason: *item.Reason,
		})
		total += *item.Score
		reasons = append(reasons, *item.Reason)
	}
	if expectedIDs != nil {
		if missing := missingRubricScoreID(seenIDs, expectedIDs); missing != "" {
			return nil, fmt.Errorf("missing rubric score id %q", missing)
		}
	}
	result.Score = total / float64(len(payload.RubricScores))
	result.Reason = strings.Join(reasons, "\n")
	return result, nil
}

func expectedRubricIDs(evalMetric *metric.EvalMetric) (map[string]struct{}, error) {
	if rubrics.Count(evalMetric) == 0 {
		return nil, nil
	}
	visibleRubrics, err := rubrics.ValidateStructured(evalMetric)
	if err != nil {
		return nil, err
	}
	expected := make(map[string]struct{}, len(visibleRubrics))
	for _, rubric := range visibleRubrics {
		expected[rubric.ID] = struct{}{}
	}
	return expected, nil
}

func validateRubricScoreID(id string, seen, expected map[string]struct{}) error {
	if _, ok := seen[id]; ok {
		return fmt.Errorf("duplicate rubric score id %q", id)
	}
	if expected != nil {
		if _, ok := expected[id]; !ok {
			return fmt.Errorf("unexpected rubric score id %q", id)
		}
	}
	seen[id] = struct{}{}
	return nil
}

func missingRubricScoreID(seen, expected map[string]struct{}) string {
	for id := range expected {
		if _, ok := seen[id]; !ok {
			return id
		}
	}
	return ""
}
