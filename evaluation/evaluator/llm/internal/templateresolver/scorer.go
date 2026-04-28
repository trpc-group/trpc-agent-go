//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package templateresolver resolves template evaluator runtime components.
package templateresolver

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/rubricscores"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/singlescore"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// ResponseScorerSingleScoreName identifies the scalar score response scorer.
	ResponseScorerSingleScoreName = "single_score"
	// ResponseScorerRubricScoresName identifies the rubric scores response scorer.
	ResponseScorerRubricScoresName = "rubric_scores"
)

// ResolveResponseScorer returns the response scorer identified by name.
func ResolveResponseScorer(name string) (responsescorer.ResponseScorer, error) {
	switch name {
	case ResponseScorerSingleScoreName:
		return singlescore.New(), nil
	case ResponseScorerRubricScoresName:
		return rubricscores.New(), nil
	default:
		return nil, fmt.Errorf("unsupported response scorer %q", name)
	}
}

// StructuredOutput returns the schema associated with the named response scorer.
func StructuredOutput(name string) (*model.StructuredOutput, error) {
	switch name {
	case ResponseScorerSingleScoreName:
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
	case ResponseScorerRubricScoresName:
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
	default:
		return nil, fmt.Errorf("unsupported response scorer %q", name)
	}
}
