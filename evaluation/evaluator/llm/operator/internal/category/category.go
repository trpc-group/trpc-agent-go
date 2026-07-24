//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package category provides internal helpers for categorical response scoring.
package category

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Scores validates and returns configured category scores.
func Scores(evalMetric *metric.EvalMetric) (map[string]float64, error) {
	options, err := responseScorerOptions(evalMetric)
	if err != nil {
		return nil, err
	}
	if len(options.Categories) == 0 {
		return nil, fmt.Errorf("categorical response scorer categories are required")
	}
	scores := make(map[string]float64, len(options.Categories))
	for _, category := range options.Categories {
		if category == nil {
			return nil, fmt.Errorf("categorical category is nil")
		}
		if category.Label == "" {
			return nil, fmt.Errorf("categorical category label is empty")
		}
		if category.Score < 0 || category.Score > 1 {
			return nil, fmt.Errorf("categorical category %q score must be between 0 and 1", category.Label)
		}
		if _, ok := scores[category.Label]; ok {
			return nil, fmt.Errorf("duplicate categorical category label %q", category.Label)
		}
		scores[category.Label] = category.Score
	}
	return scores, nil
}

// Labels validates and returns configured category labels.
func Labels(evalMetric *metric.EvalMetric) ([]string, error) {
	options, err := responseScorerOptions(evalMetric)
	if err != nil {
		return nil, err
	}
	if _, err := Scores(evalMetric); err != nil {
		return nil, err
	}
	labels := make([]string, 0, len(options.Categories))
	for _, category := range options.Categories {
		labels = append(labels, category.Label)
	}
	return labels, nil
}

// StructuredOutput builds the categorical response schema.
func StructuredOutput(evalMetric *metric.EvalMetric) (*model.StructuredOutput, error) {
	labels, err := Labels(evalMetric)
	if err != nil {
		return nil, err
	}
	return &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:        "categorical_result",
			Strict:      true,
			Description: "A categorical label and a concise reason for the evaluation result.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{
						"type": "string",
						"enum": labels,
					},
					"reason": map[string]any{
						"type": "string",
					},
				},
				"required":             []string{"category", "reason"},
				"additionalProperties": false,
			},
		},
	}, nil
}

func responseScorerOptions(evalMetric *metric.EvalMetric) (*metricllm.ResponseScorerOptions, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("llm judge criterion is required")
	}
	template := evalMetric.Criterion.LLMJudge.Template
	if template == nil {
		return nil, fmt.Errorf("template is nil")
	}
	if template.ResponseScorerOptions == nil {
		return nil, fmt.Errorf("responseScorerOptions is required")
	}
	return template.ResponseScorerOptions, nil
}
