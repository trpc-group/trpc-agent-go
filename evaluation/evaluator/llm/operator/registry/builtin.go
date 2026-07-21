//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package registry

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/internal/category"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator/average"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/boolean"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/categorical"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/rubricscores"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/singlescore"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator/majorityvote"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func registerBuiltins(r Registry) {
	_ = r.RegisterResponseScorer(ResponseScorerSingleScoreName, singlescore.New())
	_ = r.RegisterResponseScorer(ResponseScorerRubricScoresName, rubricscores.New())
	_ = r.RegisterResponseScorer(ResponseScorerBooleanName, boolean.New())
	_ = r.RegisterResponseScorer(ResponseScorerCategoricalName, categorical.New())
	_ = r.RegisterStructuredOutput(StructuredOutputSingleScoreName,
		singleScoreStructuredOutputProvider{})
	_ = r.RegisterStructuredOutput(StructuredOutputRubricScoresName,
		rubricScoresStructuredOutputProvider{})
	_ = r.RegisterStructuredOutput(StructuredOutputBooleanName,
		booleanStructuredOutputProvider{})
	_ = r.RegisterStructuredOutput(StructuredOutputCategoricalName,
		categoricalStructuredOutputProvider{})
	_ = r.RegisterSamplesAggregator(SampleAggregatorMajorityVoteName, majorityvote.New())
	_ = r.RegisterInvocationsAggregator(InvocationAggregatorAverageName, average.New())
}

type singleScoreStructuredOutputProvider struct{}

func (p singleScoreStructuredOutputProvider) StructuredOutput(_ context.Context, _, _ []*evalset.Invocation,
	_ *metric.EvalMetric) (*model.StructuredOutput, error) {
	return singleScoreStructuredOutput()
}

type rubricScoresStructuredOutputProvider struct{}

func (p rubricScoresStructuredOutputProvider) StructuredOutput(_ context.Context, _, _ []*evalset.Invocation,
	_ *metric.EvalMetric) (*model.StructuredOutput, error) {
	return rubricScoresStructuredOutput()
}

type booleanStructuredOutputProvider struct{}

func (p booleanStructuredOutputProvider) StructuredOutput(_ context.Context, _, _ []*evalset.Invocation,
	_ *metric.EvalMetric) (*model.StructuredOutput, error) {
	return booleanStructuredOutput()
}

type categoricalStructuredOutputProvider struct{}

func (p categoricalStructuredOutputProvider) StructuredOutput(_ context.Context, _, _ []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*model.StructuredOutput, error) {
	return category.StructuredOutput(evalMetric)
}

func singleScoreStructuredOutput() (*model.StructuredOutput, error) {
	return &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:        "single_score_result",
			Strict:      true,
			Description: "A score and a concise reason for the evaluation result.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"score": map[string]any{
						"type":    "number",
						"minimum": 0,
						"maximum": 1,
					},
					"reason": map[string]any{
						"type": "string",
					},
				},
				"required":             []string{"score", "reason"},
				"additionalProperties": false,
			},
		},
	}, nil
}

func rubricScoresStructuredOutput() (*model.StructuredOutput, error) {
	return &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:        "rubric_scores_result",
			Strict:      true,
			Description: "Per-rubric scores and concise reasons for the evaluation result.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"rubricScores": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id": map[string]any{
									"type": "string",
								},
								"score": map[string]any{
									"type":    "number",
									"minimum": 0,
									"maximum": 1,
								},
								"reason": map[string]any{
									"type": "string",
								},
							},
							"required":             []string{"id", "score", "reason"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"rubricScores"},
				"additionalProperties": false,
			},
		},
	}, nil
}

func booleanStructuredOutput() (*model.StructuredOutput, error) {
	return &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:        "boolean_result",
			Strict:      true,
			Description: "A pass/fail result and a concise reason for the evaluation result.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"passed": map[string]any{
						"type": "boolean",
					},
					"reason": map[string]any{
						"type": "string",
					},
				},
				"required":             []string{"passed", "reason"},
				"additionalProperties": false,
			},
		},
	}, nil
}
