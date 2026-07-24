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
	metric := &EvalMetric{
		MetricName: "accuracy",
		Threshold:  0.8,
		Extension: map[string]any{
			"caseThreshold": 0.7,
			"weight":        0.3,
		},
	}

	data, err := json.Marshal(metric)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"metricName":"accuracy","threshold":0.8,"extension":{"caseThreshold":0.7,"weight":0.3}}`, string(data))

	var decoded EvalMetric
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, metric.MetricName, decoded.MetricName)
	assert.Equal(t, metric.Threshold, decoded.Threshold)
	assert.Equal(t, map[string]any{"caseThreshold": 0.7, "weight": 0.3}, decoded.Extension)
}

func TestEvalMetricJSONOmitEmpty(t *testing.T) {
	metric := &EvalMetric{}

	data, err := json.Marshal(metric)
	assert.NoError(t, err)
	assert.Equal(t, `{}`, string(data))
}
