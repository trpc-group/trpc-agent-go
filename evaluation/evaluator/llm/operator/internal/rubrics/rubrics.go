//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rubrics extracts and validates LLM judge rubrics shared by evaluator operators.
package rubrics

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// VisibleRubric is a rubric that is visible to the judge prompt.
type VisibleRubric struct {
	ID string
}

// Visible returns rubrics that would be rendered into judge prompts.
func Visible(evalMetric *metric.EvalMetric) []VisibleRubric {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil
	}
	rubrics := make([]VisibleRubric, 0, len(evalMetric.Criterion.LLMJudge.Rubrics))
	for _, rubric := range evalMetric.Criterion.LLMJudge.Rubrics {
		if rubric == nil || rubric.Content == nil {
			continue
		}
		rubrics = append(rubrics, VisibleRubric{ID: strings.TrimSpace(rubric.ID)})
	}
	return rubrics
}

// Count returns the number of rubrics that would be rendered into judge prompts.
func Count(evalMetric *metric.EvalMetric) int {
	return len(Visible(evalMetric))
}

// ValidateStructured returns visible rubrics that can safely drive structured output schemas.
func ValidateStructured(evalMetric *metric.EvalMetric) ([]VisibleRubric, error) {
	if evalMetric == nil {
		return nil, fmt.Errorf("eval metric is nil")
	}
	if evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("llm judge criterion is required")
	}
	rubrics := Visible(evalMetric)
	if len(rubrics) == 0 {
		return nil, fmt.Errorf("llm judge rubrics are required")
	}
	seen := make(map[string]struct{}, len(rubrics))
	for _, rubric := range rubrics {
		if rubric.ID == "" {
			return nil, fmt.Errorf("llm judge rubric id is required for structured output")
		}
		if _, ok := seen[rubric.ID]; ok {
			return nil, fmt.Errorf("duplicate llm judge rubric id %q", rubric.ID)
		}
		seen[rubric.ID] = struct{}{}
	}
	return rubrics, nil
}

// ScoresOutput returns a structured output schema for per-rubric scores.
func ScoresOutput(name, description string, visibleRubrics []VisibleRubric) *model.StructuredOutput {
	ids := make([]string, 0, len(visibleRubrics))
	for _, rubric := range visibleRubrics {
		ids = append(ids, rubric.ID)
	}
	return &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:        name,
			Strict:      true,
			Description: description,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"rubricScores": map[string]any{
						"type":     "array",
						"minItems": len(visibleRubrics),
						"maxItems": len(visibleRubrics),
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id": map[string]any{
									"type": "string",
									"enum": ids,
								},
								"score": map[string]any{
									"type": "number",
									"enum": []float64{0, 1},
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
	}
}
