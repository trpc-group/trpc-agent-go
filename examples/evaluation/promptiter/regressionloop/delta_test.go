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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestCompareSnapshotsClassifiesMetricChanges(t *testing.T) {
	baseline := testSnapshot(0.5,
		testCase("a", status.EvalStatusFailed, 0.2),
		testCase("b", status.EvalStatusPassed, 0.8),
	)
	candidate := testSnapshot(0.7,
		testCase("a", status.EvalStatusPassed, 0.7),
		testCase("b", status.EvalStatusFailed, 0.7),
	)

	got, err := compareSnapshots(baseline, candidate)
	require.NoError(t, err)
	require.Len(t, got.Metrics, 2)
	assert.Equal(t, deltaNewPass, got.Metrics[0].Kind)
	assert.Equal(t, deltaNewFailure, got.Metrics[1].Kind)
}

func TestCompareSnapshotsRejectsShapeDifference(t *testing.T) {
	baseline := testSnapshot(0.5, testCase("a", status.EvalStatusPassed, 0.5))
	candidate := testSnapshot(0.5, testCase("b", status.EvalStatusPassed, 0.5))

	_, err := compareSnapshots(baseline, candidate)
	require.ErrorContains(t, err, "evidence shape")
}
