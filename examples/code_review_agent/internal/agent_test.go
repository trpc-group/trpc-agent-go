//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgent_ReviewDryRun_CleanDiff(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "01_clean.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "completed", result.Task.Status)
	// Clean diff should have no critical/high findings.
	for _, f := range result.Findings {
		require.NotEqual(t, SeverityCritical, f.Severity,
			"clean diff should not have critical findings")
	}
}

func TestAgent_ReviewDryRun_SecurityDiff(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "02_security.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Findings)

	// Should detect security issues.
	hasSecurity := false
	for _, f := range result.Findings {
		if f.Category == "security" {
			hasSecurity = true
			break
		}
	}
	require.True(t, hasSecurity, "expected security findings")

	// Should have generated reports.
	require.NotEmpty(t, result.ReportJSON)
	require.NotEmpty(t, result.ReportMD)
}

func TestAgent_ReviewDryRun_AllFixtures(t *testing.T) {
	fixtures := []string{
		"01_clean.diff",
		"02_security.diff",
		"03_goroutine_leak.diff",
		"04_resource_unclosed.diff",
		"05_db_lifecycle.diff",
		"06_test_missing.diff",
		"07_duplicate.diff",
		"08_sensitive_info.diff",
	}

	for _, fx := range fixtures {
		t.Run(fx, func(t *testing.T) {
			s := newTestStorage(t)
			agent := NewReviewAgent(s)

			diffPath := filepath.Join("..", "fixtures", fx)
			result, err := agent.Review(context.Background(), ReviewInput{
				DiffFile: diffPath,
				DryRun:   true,
			})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "completed", result.Task.Status)
			require.NotEmpty(t, result.ReportJSON)
			require.NotEmpty(t, result.ReportMD)
		})
	}
}

func TestAgent_Review_PersistsToDB(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)
	ctx := context.Background()

	diffPath := filepath.Join("..", "fixtures", "02_security.diff")
	result, err := agent.Review(ctx, ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)

	// Verify task was persisted.
	task, err := s.GetTask(ctx, result.TaskID)
	require.NoError(t, err)
	require.Equal(t, "completed", task.Status)

	// Verify findings were persisted.
	findings, err := s.GetFindingsByTask(ctx, result.TaskID)
	require.NoError(t, err)
	require.NotEmpty(t, findings)
}

func TestAgent_Review_MonitoringMetrics(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "03_goroutine_leak.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, result.Monitoring)
	require.Greater(t, result.Monitoring.TotalDurationMs, int64(0))
	require.Greater(t, result.Monitoring.ToolCallCount, 0)
}

func TestAgent_Review_SensitiveInfoRedacted(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "08_sensitive_info.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)

	// Verify no raw secrets in findings evidence.
	for _, f := range result.Findings {
		require.NotContains(t, f.Evidence, "sk-proj-abcdef1234567890abcdef",
			"API key should be redacted in evidence")
		require.NotContains(t, f.Evidence, "MyP@ssw0rd!2024",
			"password should be redacted in evidence")
	}

	// Verify no raw secrets in report.
	require.NotContains(t, result.ReportJSON, "sk-proj-abcdef1234567890abcdef")
	require.NotContains(t, result.ReportMD, "sk-proj-abcdef1234567890abcdef")
}

func TestAgent_Review_DedupWorks(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "07_duplicate.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)

	// Verify no duplicate (file, line, category) in findings.
	seen := map[string]bool{}
	for _, f := range result.Findings {
		key := f.File + ":" + itoa(f.Line) + ":" + f.Category
		require.False(t, seen[key],
			"duplicate finding found: %s", key)
		seen[key] = true
	}
}
