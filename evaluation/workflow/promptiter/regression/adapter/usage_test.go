//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package adapter

import (
	"testing"
	"time"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestSummarizeUsageFallsBackToEvaluationTraces(t *testing.T) {
	cost := .25
	result := &engine.RunResult{
		BaselineValidation: usageEvaluation(3, 2),
		Rounds: []engine.RoundResult{{
			Train:          usageEvaluation(5, 4),
			Validation:     usageEvaluation(7, 6),
			CandidateTrain: usageEvaluation(11, 8),
		}},
	}
	usage, err := SummarizeUsage(result, 2*time.Second, &cost)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Calls != 4 || usage.InputTokens != 26 || usage.OutputTokens != 20 ||
		usage.TotalTokens != 46 || usage.EstimatedCost != cost || !usage.CostKnown ||
		usage.Latency != 2*time.Second || usage.Complete || usage.Source != "evaluation_traces" {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestSummarizeUsagePrefersEngineWideTelemetry(t *testing.T) {
	result := &engine.RunResult{Usage: promptiter.Usage{
		Calls: 9, PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16,
		Complete: true,
	}}
	usage, err := SummarizeUsage(result, 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Calls != 9 || usage.TotalTokens != 16 || !usage.Complete ||
		usage.Source != "promptiter_engine" || usage.Latency != 3*time.Second {
		t.Fatalf("unexpected engine usage: %+v", usage)
	}
}

func usageEvaluation(inputTokens, outputTokens int) *engine.EvaluationResult {
	usage := &model.Usage{
		PromptTokens: inputTokens, CompletionTokens: outputTokens,
		TotalTokens: inputTokens + outputTokens,
	}
	return &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{{Cases: []engine.CaseResult{{
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				Inference: &evaluation.EvaluationInferenceDetails{
					ExecutionTraces: []*atrace.Trace{{Usage: usage}},
				},
			}},
		}}}},
	}
}
