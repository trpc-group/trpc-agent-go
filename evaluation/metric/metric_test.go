//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package metric

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvalMetricJSONMarshalling(t *testing.T) {
	temperature := 0.7
	maxTokens := 128
	numSamples := 3
	metric := &EvalMetric{
		MetricName: "accuracy",
		Threshold:  0.8,
		JudgeModelOptions: &JudgeModelOptions{
			JudgeModel:   "judge",
			Temperature:  &temperature,
			MaxTokens:    &maxTokens,
			NumSamples:   &numSamples,
			CustomPrompt: "prompt",
		},
		Config: map[string]any{"mode": "strict"},
	}

	data, err := json.Marshal(metric)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"metricName":"accuracy"`)

	var decoded EvalMetric
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, metric.MetricName, decoded.MetricName)
	assert.NotNil(t, decoded.JudgeModelOptions)
	assert.Equal(t, "prompt", decoded.JudgeModelOptions.CustomPrompt)
	assert.NotNil(t, decoded.JudgeModelOptions.Temperature)
	assert.Equal(t, temperature, *decoded.JudgeModelOptions.Temperature)
	assert.NotNil(t, decoded.JudgeModelOptions.MaxTokens)
	assert.Equal(t, maxTokens, *decoded.JudgeModelOptions.MaxTokens)
	assert.NotNil(t, decoded.JudgeModelOptions.NumSamples)
	assert.Equal(t, numSamples, *decoded.JudgeModelOptions.NumSamples)
	assert.NotNil(t, decoded.Config)
	assert.Equal(t, "strict", decoded.Config["mode"])
}

func TestEvalMetricJSONOmitEmpty(t *testing.T) {
	metric := &EvalMetric{
		MetricName: "fluency",
		Threshold:  1.0,
	}

	data, err := json.Marshal(metric)
	assert.NoError(t, err)
	assert.NotContains(t, string(data), "judgeModelOptions")
	assert.NotContains(t, string(data), "config")
}
