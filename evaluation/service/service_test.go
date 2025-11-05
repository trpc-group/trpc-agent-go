//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestInferenceRequestJSONRoundTrip(t *testing.T) {
	req := &InferenceRequest{
		AppName:     "demo-app",
		EvalSetID:   "math-basic",
		EvalCaseIDs: []string{"case-1", "case-2"},
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded InferenceRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, req.AppName, decoded.AppName)
	assert.Equal(t, req.EvalSetID, decoded.EvalSetID)
	assert.ElementsMatch(t, req.EvalCaseIDs, decoded.EvalCaseIDs)
}

func TestInferenceResultJSONRoundTrip(t *testing.T) {
	result := &InferenceResult{
		AppName:      "demo-app",
		EvalSetID:    "math-basic",
		EvalCaseID:   "case-1",
		Inferences:   []*evalset.Invocation{{InvocationID: "inv-1"}},
		SessionID:    "session-123",
		Status:       status.EvalStatusPassed,
		ErrorMessage: "",
	}

	data, err := json.Marshal(result)
	assert.NoError(t, err)

	var decoded InferenceResult
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, result.AppName, decoded.AppName)
	assert.Equal(t, result.EvalSetID, decoded.EvalSetID)
	assert.Equal(t, result.EvalCaseID, decoded.EvalCaseID)
	assert.Equal(t, result.SessionID, decoded.SessionID)
	assert.Equal(t, result.Status, decoded.Status)
	assert.Len(t, decoded.Inferences, 1)
	if len(decoded.Inferences) == 1 {
		assert.Equal(t, "inv-1", decoded.Inferences[0].InvocationID)
	}
}

func TestEvaluateRequestJSONRoundTrip(t *testing.T) {
	evaluate := &EvaluateRequest{
		AppName:          "demo-app",
		EvalSetID:        "math-basic",
		InferenceResults: []*InferenceResult{{AppName: "demo-app", EvalSetID: "math-basic", EvalCaseID: "case-1"}},
		EvaluateConfig: &EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: "metric", Threshold: 0.8}},
		},
	}

	data, err := json.Marshal(evaluate)
	assert.NoError(t, err)

	var decoded EvaluateRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, evaluate.AppName, decoded.AppName)
	assert.Equal(t, evaluate.EvalSetID, decoded.EvalSetID)
	assert.Len(t, decoded.InferenceResults, 1)
	if len(decoded.InferenceResults) == 1 {
		assert.Equal(t, "case-1", decoded.InferenceResults[0].EvalCaseID)
	}
	assert.NotNil(t, decoded.EvaluateConfig)
	if decoded.EvaluateConfig != nil {
		assert.Len(t, decoded.EvaluateConfig.EvalMetrics, 1)
		if len(decoded.EvaluateConfig.EvalMetrics) == 1 {
			assert.Equal(t, "metric", decoded.EvaluateConfig.EvalMetrics[0].MetricName)
			assert.Equal(t, 0.8, decoded.EvaluateConfig.EvalMetrics[0].Threshold)
		}
	}
}
