//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pipeline

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// makeCase builds a synthetic single-metric eval case result for tests. A passing case gets a
// passed status on both the case and its metric; a failing case gets failed on both. The criterion
// and reason drive attribution.
func makeCase(id string, passed bool, score float64, crit *criterion.Criterion, reason string) *evaluation.EvaluationCaseResult {
	st := status.EvalStatusFailed
	if passed {
		st = status.EvalStatusPassed
	}
	return &evaluation.EvaluationCaseResult{
		EvalCaseID:    id,
		OverallStatus: st,
		MetricResults: []*evalresult.EvalMetricResult{
			{
				MetricName: "final_response_avg_score",
				Score:      score,
				EvalStatus: st,
				Criterion:  crit,
				Details:    &evalresult.EvalMetricResultDetails{Reason: reason},
			},
		},
	}
}

// makeResult wraps cases into an EvaluationResult, deriving the overall status from the cases.
func makeResult(setID string, cases ...*evaluation.EvaluationCaseResult) *evaluation.EvaluationResult {
	overall := status.EvalStatusPassed
	for _, c := range cases {
		if c.OverallStatus != status.EvalStatusPassed {
			overall = status.EvalStatusFailed
			break
		}
	}
	return &evaluation.EvaluationResult{
		EvalSetID:     setID,
		OverallStatus: overall,
		EvalCases:     cases,
	}
}

func critFinalResponseText() *criterion.Criterion {
	return &criterion.Criterion{FinalResponse: &finalresponse.FinalResponseCriterion{}}
}

func critFinalResponseJSON() *criterion.Criterion {
	return &criterion.Criterion{FinalResponse: &finalresponse.FinalResponseCriterion{JSON: &cjson.JSONCriterion{}}}
}

func critToolTrajectory() *criterion.Criterion {
	return &criterion.Criterion{ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{}}
}

func critLLMJudge() *criterion.Criterion {
	return &criterion.Criterion{LLMJudge: &llm.LLMCriterion{}}
}
