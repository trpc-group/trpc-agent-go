//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package adapter contains narrow helpers for auditing existing PromptIter results.
package adapter

import (
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

// SummarizeUsage returns Engine-wide telemetry when available and otherwise
// falls back to Evaluation traces. The fallback remains incomplete because
// backward, aggregation, optimization, judge, retry, or provider-side calls
// may not be represented by those traces.
func SummarizeUsage(
	result *engine.RunResult,
	latency time.Duration,
	estimatedCost *float64,
) (regression.UsageSummary, error) {
	if result == nil {
		return regression.UsageSummary{}, errors.New("PromptIter result is nil")
	}
	usage := regression.UsageSummary{
		Latency: latency,
		Source:  "evaluation_traces",
	}
	if result.Usage.Complete || result.Usage.HasEvidence() {
		usage.Calls = result.Usage.Calls
		usage.InputTokens = result.Usage.PromptTokens
		usage.OutputTokens = result.Usage.CompletionTokens
		usage.TotalTokens = result.Usage.TotalTokens
		usage.Complete = result.Usage.Complete
		usage.Source = "promptiter_engine"
		if estimatedCost != nil {
			if *estimatedCost < 0 {
				return regression.UsageSummary{}, errors.New("estimated cost must be non-negative")
			}
			usage.EstimatedCost = *estimatedCost
			usage.CostKnown = true
		}
		return usage, nil
	}
	for _, evaluation := range evaluations(result) {
		if evaluation == nil {
			continue
		}
		for _, evalSet := range evaluation.EvalSets {
			for _, evalCase := range evalSet.Cases {
				for _, run := range evalCase.RunDetails {
					if run == nil || run.Inference == nil {
						continue
					}
					for _, trace := range run.Inference.ExecutionTraces {
						if trace == nil {
							continue
						}
						calls := 0
						for _, step := range trace.Steps {
							if step.Usage != nil {
								calls++
							}
						}
						if calls == 0 && trace.Usage != nil {
							calls = 1
						}
						usage.Calls += calls
						if trace.Usage != nil {
							usage.InputTokens += int64(trace.Usage.PromptTokens)
							usage.OutputTokens += int64(trace.Usage.CompletionTokens)
							usage.TotalTokens += int64(trace.Usage.TotalTokens)
							continue
						}
						for _, step := range trace.Steps {
							if step.Usage == nil {
								continue
							}
							usage.InputTokens += int64(step.Usage.PromptTokens)
							usage.OutputTokens += int64(step.Usage.CompletionTokens)
							usage.TotalTokens += int64(step.Usage.TotalTokens)
						}
					}
				}
			}
		}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if estimatedCost != nil {
		if *estimatedCost < 0 {
			return regression.UsageSummary{}, errors.New("estimated cost must be non-negative")
		}
		usage.EstimatedCost = *estimatedCost
		usage.CostKnown = true
	}
	return usage, nil
}

func evaluations(result *engine.RunResult) []*engine.EvaluationResult {
	values := make([]*engine.EvaluationResult, 0, 1+3*len(result.Rounds))
	values = append(values, result.BaselineValidation)
	for index := range result.Rounds {
		values = append(
			values,
			result.Rounds[index].Train,
			result.Rounds[index].Validation,
			result.Rounds[index].CandidateTrain,
		)
	}
	return values
}
