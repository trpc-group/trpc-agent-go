//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

type fakeEvaluator struct{}

type fakeEvalSet struct {
	EvalSetID string     `json:"evalSetId"`
	Name      string     `json:"name"`
	EvalCases []fakeCase `json:"evalCases"`
}

type fakeCase struct {
	EvalID                  string                     `json:"evalId"`
	Critical                bool                       `json:"critical,omitempty"`
	ExpectedFailureCategory string                     `json:"expectedFailureCategory,omitempty"`
	UserInput               string                     `json:"userInput"`
	ExpectedResponse        string                     `json:"expectedResponse"`
	ExpectedTools           []regressionloop.ToolCall  `json:"expectedTools,omitempty"`
	FakeOutcomes            map[string]fakeCaseOutcome `json:"fakeOutcomes"`
}

type fakeCaseOutcome struct {
	Score                  float64                   `json:"score"`
	Passed                 bool                      `json:"passed"`
	HardFail               bool                      `json:"hardFail,omitempty"`
	FinalResponse          string                    `json:"finalResponse"`
	ToolTrajectory         []regressionloop.ToolCall `json:"toolTrajectory,omitempty"`
	TraceSummary           string                    `json:"traceSummary,omitempty"`
	RubricReason           string                    `json:"rubricReason,omitempty"`
	StructuredOutputStatus string                    `json:"structuredOutputStatus,omitempty"`
	FailureReasons         []string                  `json:"failureReasons,omitempty"`
}

func newFakeEvaluator() fakeEvaluator {
	return fakeEvaluator{}
}

func (fakeEvaluator) Evaluate(ctx context.Context, req regressionloop.EvaluationRequest) (regressionloop.EvaluationSummary, error) {
	data, err := os.ReadFile(req.EvalSet.Path)
	if err != nil {
		return regressionloop.EvaluationSummary{}, fmt.Errorf("read evalset %s: %w", req.EvalSet.Path, err)
	}
	var set fakeEvalSet
	if err := json.Unmarshal(data, &set); err != nil {
		return regressionloop.EvaluationSummary{}, fmt.Errorf("decode evalset %s: %w", req.EvalSet.Path, err)
	}
	key := promptKey(req.Prompt)
	cases := make([]regressionloop.CaseResult, 0, len(set.EvalCases))
	total := 0.0
	for _, evalCase := range set.EvalCases {
		outcome, ok := evalCase.FakeOutcomes[key]
		if !ok {
			return regressionloop.EvaluationSummary{}, fmt.Errorf("missing fake outcome for case %s prompt %s", evalCase.EvalID, key)
		}
		result := regressionloop.CaseResult{
			EvalSetID:              set.EvalSetID,
			EvalID:                 evalCase.EvalID,
			Critical:               evalCase.Critical,
			Score:                  outcome.Score,
			Passed:                 outcome.Passed,
			HardFail:               outcome.HardFail,
			FinalResponse:          outcome.FinalResponse,
			ExpectedResponse:       evalCase.ExpectedResponse,
			ToolTrajectory:         outcome.ToolTrajectory,
			ExpectedToolTrajectory: evalCase.ExpectedTools,
			TraceSummary:           outcome.TraceSummary,
			RubricReason:           outcome.RubricReason,
			StructuredOutputStatus: outcome.StructuredOutputStatus,
			FailureReasons:         outcome.FailureReasons,
			MetricResults: []regressionloop.MetricResult{{
				Name:     "deterministic_quality",
				Score:    outcome.Score,
				Passed:   outcome.Passed,
				HardFail: outcome.HardFail,
				Reason:   strings.Join(outcome.FailureReasons, "; "),
			}},
		}
		cases = append(cases, result)
		total += outcome.Score
	}
	status := "passed"
	for _, c := range cases {
		if !c.Passed {
			status = "failed"
			break
		}
	}
	return regressionloop.EvaluationSummary{
		EvalSetID: set.EvalSetID,
		Score:     total / float64(len(cases)),
		Status:    status,
		Cases:     cases,
		Cost:      regressionloop.CostSummary{Calls: len(cases), EstimatedCost: float64(len(cases)) * 0.001},
		Latency:   regressionloop.LatencySummary{TotalMS: int64(len(cases) * 5)},
	}, nil
}

func promptKey(prompt string) string {
	switch {
	case strings.Contains(prompt, "SUCCESS_PROMPT"):
		return "candidate_success"
	case strings.Contains(prompt, "INEFFECTIVE_PROMPT"):
		return "candidate_ineffective"
	case strings.Contains(prompt, "OVERFIT_PROMPT"):
		return "candidate_overfit"
	default:
		return "baseline"
	}
}
