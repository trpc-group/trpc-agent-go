//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
)

func llmVerifierMetric() *metric.EvalMetric {
	return &metric.EvalMetric{
		EvaluatorName: "llm_verifier_pairwise",
		Threshold:     0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Rubrics: []*criterionllm.Rubric{
					{
						ID: "accuracy",
						Content: &criterionllm.RubricContent{
							Text: "The final answer directly satisfies the user's request and does not introduce unsupported claims.",
						},
					},
					{
						ID: "conciseness",
						Content: &criterionllm.RubricContent{
							Text: "The final answer is concise and stays within the requested length constraint.",
						},
					},
					{
						ID: "required_terms",
						Content: &criterionllm.RubricContent{
							Text: "The final answer includes every term or concept explicitly required by the user.",
						},
					},
					{
						ID: "clarity",
						Content: &criterionllm.RubricContent{
							Text: "The final answer is easy for the target audience in the user prompt to understand.",
						},
					},
				},
			},
		},
	}
}
