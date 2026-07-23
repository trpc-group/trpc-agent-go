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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTestCoverageRuleIsPackageScoped(t *testing.T) {
	summary := DiffSummary{
		Files: []ChangedFile{
			{OldPath: "pkg/a/service.go", NewPath: "pkg/a/service.go"},
			{OldPath: "pkg/b/helper_test.go", NewPath: "pkg/b/helper_test.go"},
		},
	}

	findings := testCoverageRule(summary)
	require.Len(t, findings, 1)
	require.Equal(t, "pkg/a/service.go", findings[0].File)
	require.Equal(t, "go.testing.missing", findings[0].RuleID)
}
