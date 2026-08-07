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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/app"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
)

func runReview(t *testing.T, diffFile string) (*reviewmodel.ReviewReport, string) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_review.db")
	jsonPath := filepath.Join(tmpDir, "report.json")
	mdPath := filepath.Join(tmpDir, "report.md")

	cfg := app.Config{
		DiffFile:    diffFile,
		DryRun:      true,
		SandboxType: sandbox.RuntimeFake,
		DBPath:      dbPath,
		OutputJSON:  jsonPath,
		OutputMD:    mdPath,
	}

	ctx := context.Background()
	err := app.Run(ctx, cfg)
	require.NoError(t, err)

	data, err := os.ReadFile(jsonPath)
	require.NoError(t, err)

	var report reviewmodel.ReviewReport
	err = json.Unmarshal(data, &report)
	require.NoError(t, err)

	return &report, dbPath
}

func TestReviewCleanDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/clean.diff")
	t.Logf("Clean diff findings: %d", len(report.Findings))
	assert.True(t, report.Task.Status == reviewmodel.StatusCompleted ||
		report.Task.Status == reviewmodel.StatusCompletedWithWarnings,
		"clean diff should complete successfully")
}

func TestReviewSecurityDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/security.diff")
	assert.True(t, len(report.Findings) >= 2, "security.diff should have at least 2 findings (security + secret)")

	hasSecurity := false
	hasSecret := false
	for _, f := range report.Findings {
		if f.Category == reviewmodel.CategorySecurity {
			hasSecurity = true
		}
		if f.Category == reviewmodel.CategorySensitive {
			hasSecret = true
		}
	}
	assert.True(t, hasSecurity, "should have security finding")
	assert.True(t, hasSecret, "should have secret finding")

	t.Logf("Security diff findings: %d", len(report.Findings))
}

func TestReviewGoroutineDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/goroutine_context.diff")
	hasGoroutine := false
	for _, f := range report.Findings {
		if f.Category == reviewmodel.CategoryGoroutine {
			hasGoroutine = true
		}
	}
	assert.True(t, hasGoroutine, "should have goroutine finding")
	t.Logf("Goroutine diff findings: %d", len(report.Findings))
}

func TestReviewResourceDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/resource_lifecycle.diff")
	hasResource := false
	for _, f := range report.Findings {
		if f.Category == reviewmodel.CategoryResource {
			hasResource = true
		}
	}
	assert.True(t, hasResource, "should have resource finding")
	t.Logf("Resource diff findings: %d", len(report.Findings))
}

func TestReviewDatabaseDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/database_lifecycle.diff")
	hasDB := false
	for _, f := range report.Findings {
		if f.Category == reviewmodel.CategoryDB {
			hasDB = true
		}
	}
	assert.True(t, hasDB, "should have database lifecycle finding")
	t.Logf("Database diff findings: %d", len(report.Findings))
}

func TestReviewMissingTestsDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/missing_tests.diff")
	assert.True(t, len(report.Findings) >= 1, "missing_tests.diff should have at least 1 finding")
	t.Logf("Missing tests diff findings: %d", len(report.Findings))
}

func TestReviewDuplicateFindingDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/duplicate_finding.diff")
	// Deduplication should prevent duplicate findings for the same file+line+category.
	t.Logf("Duplicate diff findings: %d", len(report.Findings))
	assert.True(t, len(report.Findings) <= 3, "duplicate diff should have <= 3 unique findings after dedup")
}

func TestReviewSecretRedactionDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/secret_redaction.diff")
	assert.True(t, len(report.Findings) >= 2, "secret_redaction.diff should have at least 2 findings")

	// Verify no plaintext secrets in evidence fields.
	for _, f := range report.Findings {
		assert.NotContains(t, f.Evidence, "sk-proj-abcdefgh", "evidence should not contain plaintext API key")
		assert.NotContains(t, f.Evidence, "super_secret_password", "evidence should not contain plaintext password")
		assert.NotContains(t, f.Evidence, "root_password", "evidence should not contain plaintext password")
	}
	t.Logf("Secret redaction diff findings: %d", len(report.Findings))
}

func TestReviewSandboxFailureDiff(t *testing.T) {
	report, _ := runReview(t, "fixtures/diffs/sandbox_failure.diff")
	// Even with sandbox in fake mode, review should complete.
	assert.NotEqual(t, reviewmodel.StatusFailed, report.Task.Status)
	t.Logf("Sandbox failure diff findings: %d", len(report.Findings))
}

func TestReportGeneratesBothFormats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "review.db")
	jsonPath := filepath.Join(tmpDir, "report.json")
	mdPath := filepath.Join(tmpDir, "report.md")

	cfg := app.Config{
		DiffFile:    "fixtures/diffs/security.diff",
		DryRun:      true,
		SandboxType: sandbox.RuntimeFake,
		DBPath:      dbPath,
		OutputJSON:  jsonPath,
		OutputMD:    mdPath,
	}

	ctx := context.Background()
	err := app.Run(ctx, cfg)
	require.NoError(t, err)

	// Verify JSON output.
	jsonData, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), `"severity"`)

	// Verify Markdown output.
	mdData, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	assert.Contains(t, string(mdData), "# Code Review Report")

	// Verify SQLite output.
	_, err = os.Stat(dbPath)
	assert.NoError(t, err)

	// Verify task count.
	// Just check that the DB file exists and has some content.
	info, err := os.Stat(dbPath)
	require.NoError(t, err)
	assert.True(t, info.Size() > 0, "SQLite DB should not be empty")

	fmt.Printf("JSON: %d bytes, MD: %d bytes, DB: %d bytes\n",
		len(jsonData), len(mdData), info.Size())
}

func TestGovernanceDenyBlocksSandbox(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := app.Config{
		DiffFile:        "fixtures/diffs/security.diff",
		DryRun:          false,
		SandboxType:     sandbox.RuntimeFake,
		DBPath:          filepath.Join(tmpDir, "deny_review.db"),
		OutputJSON:      filepath.Join(tmpDir, "report.json"),
		OutputMD:        filepath.Join(tmpDir, "report.md"),
		AllowedCommands: nil,
		DeniedCommands:  []string{"go", "checkrunner", "rm", "curl", "ssh", "sudo", "bash", "sh"},
	}

	ctx := context.Background()
	err := app.Run(ctx, cfg)
	require.NoError(t, err)

	data, err := os.ReadFile(cfg.OutputJSON)
	require.NoError(t, err)

	var report reviewmodel.ReviewReport
	err = json.Unmarshal(data, &report)
	require.NoError(t, err)

	assert.True(t, len(report.PermissionDecisions) >= 2)
	denyCount := 0
	for _, d := range report.PermissionDecisions {
		if d.Action == "deny" {
			denyCount++
		}
	}
	assert.True(t, denyCount > 0, "should have denied permissions with empty allowlist")
	assert.Empty(t, report.SandboxRuns, "denied commands should not execute in sandbox")
	t.Logf("Deny decisions: %d, Sandbox runs: %d", denyCount, len(report.SandboxRuns))
}

func TestReportContainsAllRequiredSections(t *testing.T) {
	tmpDir := t.TempDir()
	mdPath := filepath.Join(tmpDir, "report.md")

	cfg := app.Config{
		DiffFile:    "fixtures/diffs/security.diff",
		DryRun:      true,
		SandboxType: sandbox.RuntimeFake,
		DBPath:      filepath.Join(tmpDir, "review.db"),
		OutputJSON:  filepath.Join(tmpDir, "report.json"),
		OutputMD:    mdPath,
	}

	ctx := context.Background()
	err := app.Run(ctx, cfg)
	require.NoError(t, err)

	data, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	content := string(data)

	required := []string{
		"Code Review Report",
		"Summary",
		"Total Files",
		"Total Findings",
		"Critical",
		"High",
		"Medium",
		"Low",
		"Warning",
		"Needs Human Review",
		"Severity Distribution",
		"Monitoring",
		"Total Duration",
		"Sandbox Duration",
		"Tool Calls",
		"Permission Denies",
		"Governance Summary",
		"Sandbox Execution Summary",
		"Human Review Items",
		"Findings",
		"Recommendation",
	}
	for _, section := range required {
		assert.Contains(t, content, section, "report must contain section: %s", section)
	}
}
