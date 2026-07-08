//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAttributeCaseCategories(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		category AttributionCategory
	}{
		{"final", "final response mismatch with reference", AttributionFinalResponseMismatch},
		{"tool selection", "wrong tool selected", AttributionToolSelectionError},
		{"tool argument", "tool argument query is wrong", AttributionToolArgumentError},
		{"routing", "route selected wrong agent", AttributionRoutingError},
		{"format", "structured output json schema violation", AttributionFormatError},
		{"knowledge", "knowledge recall missing context", AttributionKnowledgeRecallInsufficient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := caseResult("case", 0, false)
			c.FailureReasons = []string{tt.reason}
			got := AttributeCase(c)
			assert.Equal(t, tt.category, got[0].Category)
			assert.NotEmpty(t, got[0].Evidence)
		})
	}
}

func TestAttributeCaseFallsBackToMetricThreshold(t *testing.T) {
	c := CaseResult{
		EvalID: "case",
		Score:  0.4,
		Passed: false,
		MetricResults: []MetricResult{{
			Name:   "quality",
			Score:  0.4,
			Passed: false,
			Reason: "score below threshold",
		}},
	}
	got := AttributeCase(c)
	assert.Equal(t, AttributionMetricThresholdMiss, got[0].Category)
	assert.NotEmpty(t, got[0].Evidence)
}

func TestAttributeCaseFallsBackToUnknown(t *testing.T) {
	got := AttributeCase(CaseResult{EvalID: "case", Passed: false})
	assert.Equal(t, AttributionUnknown, got[0].Category)
	assert.NotEmpty(t, got[0].Evidence)
}
