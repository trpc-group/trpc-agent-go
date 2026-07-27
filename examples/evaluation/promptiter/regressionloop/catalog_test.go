//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCatalogRejectsNullAndDuplicateCases(t *testing.T) {
	_, err := buildCatalog(
		[]byte(`{"evalSetId":"v","evalCases":[null,{"evalId":"x"},{"evalId":"x"}]}`),
		[]byte(`[]`),
	)
	require.ErrorContains(t, err, "null evaluation case")

	_, err = buildCatalog(
		[]byte(`{"evalSetId":"v","evalCases":[{"evalId":"x"},{"evalId":"x"}]}`),
		[]byte(`[{"metricName":"quality"}]`),
	)
	require.ErrorContains(t, err, "duplicate evaluation case")
}

func TestBuildCatalogRejectsNullAndDuplicateMetrics(t *testing.T) {
	evalSet := []byte(`{"evalSetId":"v","evalCases":[{"evalId":"x"}]}`)

	_, err := buildCatalog(evalSet, []byte(`[null]`))
	require.ErrorContains(t, err, "null metric")

	_, err = buildCatalog(evalSet, []byte(`[
		{"metricName":"quality"},
		{"metricName":"quality"}
	]`))
	require.ErrorContains(t, err, "duplicate metric")
}

func TestBuildCatalogRejectsEmptyIdentities(t *testing.T) {
	tests := []struct {
		name    string
		evalSet string
		metrics string
		message string
	}{
		{
			name:    "evaluation set ID",
			evalSet: `{"evalCases":[{"evalId":"x"}]}`,
			metrics: `[{"metricName":"quality"}]`,
			message: "evaluation set ID",
		},
		{
			name:    "evaluation case ID",
			evalSet: `{"evalSetId":"v","evalCases":[{}]}`,
			metrics: `[{"metricName":"quality"}]`,
			message: "evaluation case ID",
		},
		{
			name:    "metric name",
			evalSet: `{"evalSetId":"v","evalCases":[{"evalId":"x"}]}`,
			metrics: `[{}]`,
			message: "metric name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildCatalog([]byte(tt.evalSet), []byte(tt.metrics))
			require.ErrorContains(t, err, tt.message)
		})
	}
}

func TestBuildCatalogPreservesOrderAndBuildsResultKeys(t *testing.T) {
	catalog, err := buildCatalog(
		[]byte(`{"evalSetId":"v","evalCases":[{"evalId":"b"},{"evalId":"a"}]}`),
		[]byte(`[{"metricName":"z"},{"metricName":"m"}]`),
	)
	require.NoError(t, err)
	assert.Equal(t, "v", catalog.EvalSetID)
	assert.Equal(t, []string{"b", "a"}, catalog.EvalCaseIDs)
	assert.Equal(t, []string{"z", "m"}, catalog.MetricNames)
	assert.Len(t, catalog.ResultKeys, 4)
	_, ok := catalog.ResultKeys[resultKey{
		EvalSetID:  "v",
		EvalCaseID: "a",
		MetricName: "m",
	}]
	assert.True(t, ok)
}

func TestFingerprintChangesWithExactPromptInput(t *testing.T) {
	a := fingerprintInputs(
		[]byte("prompt-a"),
		[]byte("train"),
		[]byte("validation"),
		[]byte("metrics"),
		[]byte("config"),
	)
	b := fingerprintInputs(
		[]byte("prompt-b"),
		[]byte("train"),
		[]byte("validation"),
		[]byte("metrics"),
		[]byte("config"),
	)

	assert.NotEqual(t, a, b)
}

func TestFingerprintDoesNotNormalizeInputBytes(t *testing.T) {
	a := fingerprintInputs([]byte("prompt\n"), nil, nil, nil, nil)
	b := fingerprintInputs([]byte("prompt\r\n"), nil, nil, nil, nil)

	assert.NotEqual(t, a, b)
	assert.Regexp(t, `^[0-9a-f]{64}$`, a)
}
