//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunPipelineRejectsOverfitCandidate(t *testing.T) {
	input, err := loadInput(filepath.Join("data", "promptiter.json"))
	require.NoError(t, err)
	report, err := RunPipeline(context.Background(), input)
	require.NoError(t, err)
	require.False(t, report.Gate.Accepted)
	require.Greater(t, report.Candidate.TrainEvaluation.OverallScore, report.BaselineTrain.OverallScore)
	require.Equal(t, 1, report.Delta.NewlyFailed)
	require.Equal(t, 1, report.Delta.CriticalRegressed)
	require.NotEmpty(t, report.Rounds)
	require.NotEmpty(t, report.Rounds[0].Losses)
	require.NotNil(t, report.Rounds[0].Patches)
}
