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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestSummarizeUsageUsesExistingPromptIterEvidence(t *testing.T) {
	trace := &atrace.Trace{Usage: &model.Usage{
		PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5,
	}}
	result := &engine.RunResult{
		BaselineValidation: &engine.EvaluationResult{
			EvalSets: []engine.EvalSetResult{{Cases: []engine.CaseResult{{
				RunDetails: []*evaluation.EvaluationCaseRunDetails{{
					Inference: &evaluation.EvaluationInferenceDetails{
						ExecutionTraces: []*atrace.Trace{trace},
					},
				}},
			}}}},
		},
	}
	usage, err := SummarizeUsage(result, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Calls != 1 || usage.TotalTokens != 5 || usage.CostKnown {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}
