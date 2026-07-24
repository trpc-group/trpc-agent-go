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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewerSandboxFailureDoesNotAbort(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(tempDir, "review.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	reviewer, err := NewReviewer(
		store, &FakeSandbox{FailCommand: "bash"}, "skills",
	)
	require.NoError(t, err)

	const secret = "sk-live-1234567890abcdef"
	const patch = `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,4 @@
 package main
+const apiKey = "` + secret + `"
+func changed() {}
`
	report, err := reviewer.Review(
		context.Background(),
		ReviewRequest{
			Diff: []byte(patch), InputKind: "test",
			RepoPath: tempDir, Runtime: "fake",
			OutputDir: tempDir,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "completed_with_errors", report.Status)
	require.Equal(t, "changes_requested", report.Conclusion)
	require.NotEmpty(t, report.Findings)
	require.Equal(t, 1, report.Metrics.Errors["command_failed"])

	jsonReport, err := os.ReadFile(filepath.Join(tempDir, "review_report.json"))
	require.NoError(t, err)
	markdownReport, err := os.ReadFile(filepath.Join(tempDir, "review_report.md"))
	require.NoError(t, err)
	require.NotContains(t, string(jsonReport), secret)
	require.NotContains(t, string(markdownReport), secret)
	rawDB, err := os.ReadFile(filepath.Join(tempDir, "review.db"))
	require.NoError(t, err)
	require.NotContains(t, string(rawDB), secret)

	stored, err := store.GetReview(context.Background(), report.TaskID)
	require.NoError(t, err)
	require.Equal(t, report.Status, stored.Status)
	require.Len(t, stored.Artifacts, 2)
}

func TestFindingsFromSandbox(t *testing.T) {
	findings := findingsFromSandbox([]SandboxRun{{
		Command: `"go" "vet" "./..."`, Status: "failed",
		Output: "internal/server.go:42:9: unreachable code",
	}})
	require.Len(t, findings, 1)
	require.Equal(t, "internal/server.go", findings[0].File)
	require.Equal(t, 42, findings[0].Line)
	require.Equal(t, "go_vet", findings[0].Source)
}
