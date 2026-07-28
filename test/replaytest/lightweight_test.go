//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStandardCasesAreConsistentAcrossLightweightBackends(t *testing.T) {
	report, err := Run(context.Background(), newLightweightBackends(t), StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences(), "report: %+v", report.Differences)
}

func TestLightweightSuiteCompletesWithinThirtySeconds(t *testing.T) {
	started := time.Now()
	report, err := Run(context.Background(), newLightweightBackends(t), StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences())
	require.Less(t, time.Since(started), 30*time.Second)
}
