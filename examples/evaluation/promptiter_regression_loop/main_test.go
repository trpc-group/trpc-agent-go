//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

func TestDeterministicScenarios(t *testing.T) {
	tests := []struct {
		name     string
		accepted bool
	}{
		{name: "success", accepted: true},
		{name: "ineffective", accepted: false},
		{name: "overfit", accepted: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			engine, err := newDeterministicEngine(ctx, test.name)
			require.NoError(t, err)
			report, err := regression.Run(ctx, engine, deterministicRequest(), regression.GateConfig{
				MinScoreGain: 0.05, MaxNewFailures: 0, MaxScoreRegressions: 0,
				CriticalCaseIDs: []string{"validation_account_security"},
			})
			require.NoError(t, err)
			assert.Equal(t, test.accepted, report.Accepted)
		})
	}
}

func TestDeterministicRunRejectsUnknownScenario(t *testing.T) {
	_, err := newDeterministicEngine(context.Background(), "unknown")
	require.ErrorContains(t, err, "unknown scenario")
}
