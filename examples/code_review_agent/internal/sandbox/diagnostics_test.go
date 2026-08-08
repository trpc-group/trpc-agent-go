//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestParseDiagnosticsKeepsOnlyAddedLinesAndBoundsInput(t *testing.T) {
	diff := oneAddedLineDiff(t)
	candidates := ParseDiagnostics("task-1", CheckGoVet, diff,
		"x.go:1:1: old line\nx.go:2:3: new issue\n../escape.go:2: bad\n")
	require.Len(t, candidates, 1)
	require.Equal(t, "x.go", candidates[0].File)
	require.Equal(t, 2, candidates[0].Line)
	require.Equal(t, review.SourceTool, candidates[0].Source)
	require.Equal(t, "tool/go-vet/v1", candidates[0].RuleID)
}
