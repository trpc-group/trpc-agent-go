//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The executable quality gate over the shared corpus fixture lives in
// corpus_test.go (TestCorpusFixture_QualityGateFromFixture); this file
// keeps only the performance budget test.

// TestCorpusPerformance_500UnderOneSecond asserts the 500-sample
// performance budget from the plan.
func TestCorpusPerformance_500UnderOneSecond(t *testing.T) {
	p := testPolicy(t)
	s := NewScanner(p)
	inputs := make([]ScanInput, 0, 500)
	for i := 0; i < 500; i++ {
		inputs = append(inputs, ScanInput{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspaceExec,
			Command:  "echo " + itoa(i),
		})
	}
	start := time.Now()
	_, err := s.ScanBatch(context.Background(), inputs)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Less(t, elapsed, time.Second, "500-command batch took %v", elapsed)
}
