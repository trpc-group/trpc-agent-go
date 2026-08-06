//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package report

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestBuildReport_BasicStructure(t *testing.T) {
	findings := []types.Finding{
		{ID: "f1", Severity: "critical", Category: "security", File: "a.go", Line: 10,
			Title: "SQL injection", Confidence: 1.0, Source: "rule_engine"},
		{ID: "f2", Severity: "high", Category: "error_handling", File: "b.go", Line: 20,
			Title: "Ignored error", Confidence: 0.9, Source: "rule_engine"},
	}
	warnings := []types.Finding{
		{ID: "w1", Severity: "warning", Category: "style", File: "c.go", Line: 5,
			Title: "Naming", Confidence: 0.3, Source: "llm"},
	}
	decisions := []types.PermissionDecision{
		{Command: "go vet ./...", RiskLevel: "low", Decision: "allow"},
		{Command: "rm -rf /tmp", RiskLevel: "high", Decision: "deny"},
	}
	sandboxResults := []types.SandboxResult{
		{Command: "go_vet", ExitCode: 0, DurationMs: 500},
	}
	gs := graph.State{
		state.StateKeyNodeDiffParserMs: int64(10),
		state.StateKeyNodeRuleEngineMs: int64(50),
	}
	report := buildReport("task-1", findings, warnings, decisions, sandboxResults, gs)
	if report.TaskID != "task-1" {
		t.Errorf("expected task_id=task-1, got %q", report.TaskID)
	}
	if report.FindingsCount != 2 || report.WarningsCount != 1 {
		t.Errorf("counts wrong: f=%d w=%d", report.FindingsCount, report.WarningsCount)
	}
	if report.SeverityDistribution["critical"] != 1 || report.SeverityDistribution["warning"] != 1 {
		t.Errorf("severity dist wrong: %v", report.SeverityDistribution)
	}
	if report.PermissionSummary.Allowed != 1 || report.PermissionSummary.Denied != 1 {
		t.Errorf("perm summary wrong: %+v", report.PermissionSummary)
	}
	if len(report.SandboxSummary) != 1 {
		t.Errorf("expected 1 sandbox summary, got %d", len(report.SandboxSummary))
	}
}

func TestBuildReport_EmptyInputs(t *testing.T) {
	report := buildReport("task-empty", nil, nil, nil, nil, graph.State{})
	if report.FindingsCount != 0 || report.WarningsCount != 0 {
		t.Errorf("empty inputs should have 0 counts")
	}
}

func TestBuildReport_NeedsHumanReview(t *testing.T) {
	decisions := []types.PermissionDecision{
		{Command: "custom-tool", RiskLevel: "medium", Decision: "needs_human_review"},
	}
	report := buildReport("task-hr", nil, nil, decisions, nil, graph.State{})
	if report.PermissionSummary.NeedsHR != 1 {
		t.Errorf("expected 1 needs_human_review, got %d", report.PermissionSummary.NeedsHR)
	}
}

func TestFormatMarkdown_BasicStructure(t *testing.T) {
	report := types.ReviewReport{
		TaskID:        "task-md-1",
		FindingsCount: 2,
		WarningsCount: 1,
		SeverityDistribution: map[string]int{
			"critical": 1, "high": 1, "medium": 0, "low": 0, "warning": 1,
		},
		CategoryDistribution: map[string]int{"security": 1, "error_handling": 1, "style": 1},
		Summary:              "Reviewed 2 findings.",
		NodeTimings:          map[string]int64{state.StateKeyNodeDiffParserMs: 15},
		PermissionSummary:    types.PermissionSummary{Total: 2, Allowed: 1, Denied: 1},
		SandboxSummary: []types.SandboxSummary{
			{Command: "go_vet", ExitCode: 0, DurationMs: 450},
		},
	}
	findings := []types.Finding{
		{ID: "f1", Severity: "critical", Category: "security", File: "a.go", Line: 10,
			Title: "SQL injection risk", Confidence: 1.0, Source: "rule_engine",
			Evidence: "+query := fmt.Sprintf(...)", Recommendation: "Use parameterized queries"},
	}
	warnings := []types.Finding{
		{ID: "w1", Severity: "warning", Category: "style", File: "c.go", Line: 5,
			Title: "Naming convention", Confidence: 0.3, Source: "llm"},
	}
	md := formatMarkdown(report, findings, warnings)
	if !strings.Contains(md, "# Code Review Report") {
		t.Error("missing heading")
	}
	if !strings.Contains(md, "**Task ID**: `task-md-1`") {
		t.Error("missing task ID")
	}
	if !strings.Contains(md, "## Summary") {
		t.Error("missing Summary")
	}
	if !strings.Contains(md, "## Findings") {
		t.Error("missing Findings")
	}
	if !strings.Contains(md, "[CRITICAL] SQL injection risk") {
		t.Error("missing finding title")
	}
	if !strings.Contains(md, "**Fix**: Use parameterized queries") {
		t.Error("missing recommendation")
	}
	if !strings.Contains(md, "## Needs Human Review") {
		t.Error("missing Human Review")
	}
	if !strings.Contains(md, "## Sandbox Execution Summary") {
		t.Error("missing Sandbox section")
	}
	if !strings.Contains(md, "## Permission Decisions") {
		t.Error("missing Permission section")
	}
	if !strings.Contains(md, "## Performance") {
		t.Error("missing Performance section")
	}
}

func TestFormatMarkdown_NoWarningsSkipsHumanReview(t *testing.T) {
	report := types.ReviewReport{
		TaskID:               "task-no-warn",
		SeverityDistribution: map[string]int{},
		CategoryDistribution: map[string]int{},
		NodeTimings:          map[string]int64{},
		PermissionSummary:    types.PermissionSummary{},
	}
	md := formatMarkdown(report, []types.Finding{}, nil)
	if strings.Contains(md, "## Needs Human Review") {
		t.Error("should NOT have Human Review section when no warnings")
	}
}

func TestFormatMarkdown_ConfidencePercentage(t *testing.T) {
	report := types.ReviewReport{
		TaskID:               "task-conf",
		SeverityDistribution: map[string]int{"low": 1},
		CategoryDistribution: map[string]int{"other": 1},
		NodeTimings:          map[string]int64{},
		PermissionSummary:    types.PermissionSummary{},
	}
	findings := []types.Finding{
		{ID: "f1", Severity: "low", Category: "other", File: "a.go", Line: 1,
			Title: "test", Confidence: 0.75, Source: "llm"},
	}
	md := formatMarkdown(report, findings, nil)
	if !strings.Contains(md, "75%") {
		t.Errorf("confidence 0.75 should format as 75%%, got:\n%s", md)
	}
}

func TestRun_GeneratesJSONAndMarkdown(t *testing.T) {
	tmpDir := t.TempDir()
	findings := []types.Finding{
		{ID: "f1", Severity: "high", Category: "security", File: "a.go", Line: 10,
			Title: "Test finding", Confidence: 0.9, Source: "rule_engine",
			Evidence: "+test", Recommendation: "Fix it"},
	}
	gs := graph.State{
		state.StateKeyTaskID:              "task-run-1",
		state.StateKeyFindings:            findings,
		state.StateKeyWarnings:            []types.Finding{},
		state.StateKeyPermissionDecisions: []types.PermissionDecision{},
		state.StateKeySandboxResults:      []types.SandboxResult{},
		state.StateKeyOutputDir:           tmpDir,
	}
	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	finalState := result.(graph.State)
	jsonPath, _ := finalState[state.StateKeyJSONReportPath].(string)
	mdPath, _ := finalState[state.StateKeyMDReportPath].(string)
	if jsonPath == "" || mdPath == "" {
		t.Fatalf("reports not generated: json=%s md=%s", jsonPath, mdPath)
	}
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("cannot read JSON: %v", err)
	}
	var report types.ReviewReport
	if err := json.Unmarshal(jsonData, &report); err != nil {
		t.Fatalf("JSON invalid: %v", err)
	}
	if report.TaskID != "task-run-1" {
		t.Errorf("wrong task_id: %q", report.TaskID)
	}
	mdData, err := os.ReadFile(mdPath)
	if err != nil || len(mdData) == 0 {
		t.Error("MD report is missing or empty")
	}
}

func TestRun_WrongOutputDirCreatesDir(t *testing.T) {
	tmpBase := t.TempDir()
	outputDir := filepath.Join(tmpBase, "new-subdir", "reports")
	gs := graph.State{
		state.StateKeyTaskID:              "task-dir",
		state.StateKeyFindings:            []types.Finding{},
		state.StateKeyWarnings:            []types.Finding{},
		state.StateKeyPermissionDecisions: []types.PermissionDecision{},
		state.StateKeySandboxResults:      []types.SandboxResult{},
		state.StateKeyOutputDir:           outputDir,
	}
	_, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		t.Errorf("output directory %s was not created", outputDir)
	}
}

func TestRun_SensitiveDataRedaction(t *testing.T) {
	tmpDir := t.TempDir()
	findings := []types.Finding{
		{ID: "f1", Severity: "high", Category: "sensitive_info", File: "a.go", Line: 10,
			Title: "Hardcoded secret", Confidence: 1.0, Source: "rule_engine",
			Evidence:       "+APIKey = \"sk-proj-abc123xyz456\"",
			Recommendation: "Use os.Getenv(\"API_KEY\")"},
	}
	gs := graph.State{
		state.StateKeyTaskID:              "task-redact",
		state.StateKeyFindings:            findings,
		state.StateKeyWarnings:            []types.Finding{},
		state.StateKeyPermissionDecisions: []types.PermissionDecision{},
		state.StateKeySandboxResults:      []types.SandboxResult{},
		state.StateKeyOutputDir:           tmpDir,
	}
	_, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	mdPath := filepath.Join(tmpDir, "task-redact_report.md")
	mdData, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown report: %v", err)
	}
	if strings.Contains(string(mdData), "sk-proj-abc123xyz456") {
		t.Error("secret key should be redacted in markdown report")
	}
	if !strings.Contains(string(mdData), "***REDACTED***") {
		t.Error("redaction placeholder ***REDACTED*** not found in report")
	}
}

func TestRun_TimingRecorded(t *testing.T) {
	tmpDir := t.TempDir()
	gs := graph.State{
		state.StateKeyTaskID:              "task-timing",
		state.StateKeyFindings:            []types.Finding{},
		state.StateKeyWarnings:            []types.Finding{},
		state.StateKeyPermissionDecisions: []types.PermissionDecision{},
		state.StateKeySandboxResults:      []types.SandboxResult{},
		state.StateKeyOutputDir:           tmpDir,
	}
	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// Verify in-memory state
	finalState := result.(graph.State)
	ms, ok := finalState[state.StateKeyNodeReportGeneratorMs].(int64)
	if !ok || ms < 0 {
		t.Error("node timing not recorded in state")
	}
	// Verify serialized JSON report contains the timing
	jsonPath := filepath.Join(tmpDir, "task-timing_report.json")
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json report: %v", err)
	}
	var report types.ReviewReport
	if err := json.Unmarshal(jsonData, &report); err != nil {
		t.Fatalf("json report invalid: %v", err)
	}
	reportMs, ok := report.NodeTimings[state.StateKeyNodeReportGeneratorMs]
	if !ok {
		t.Error("serialized NodeTimings missing report_generator entry")
	} else if reportMs < 0 {
		t.Error("serialized NodeTimings for report_generator should be non-negative")
	}
	// Cross-layer contract: serialized value must match in-memory state.
	if reportMs != ms {
		t.Errorf("report_generator timing mismatch: state=%dms, report=%dms", ms, reportMs)
	}
}

func TestRun_MultipleFindingsMultipleCategories(t *testing.T) {
	tmpDir := t.TempDir()
	findings := []types.Finding{
		{ID: "f1", Severity: "critical", Category: "security", File: "a.go", Line: 1, Title: "A", Confidence: 1.0, Source: "rule_engine"},
		{ID: "f2", Severity: "high", Category: "security", File: "b.go", Line: 2, Title: "B", Confidence: 1.0, Source: "rule_engine"},
		{ID: "f3", Severity: "medium", Category: "error_handling", File: "c.go", Line: 3, Title: "C", Confidence: 0.8, Source: "llm"},
	}
	warnings := []types.Finding{
		{ID: "w1", Severity: "warning", Category: "goroutine_leak", File: "f.go", Line: 6, Title: "W", Confidence: 0.4, Source: "llm"},
	}
	decisions := []types.PermissionDecision{
		{Command: "go vet", RiskLevel: "low", Decision: "allow"},
		{Command: "staticcheck", RiskLevel: "low", Decision: "allow"},
	}
	sandboxResults := []types.SandboxResult{
		{Command: "go_vet", ExitCode: 0, DurationMs: 100},
	}
	gs := graph.State{
		state.StateKeyTaskID:              "task-full",
		state.StateKeyFindings:            findings,
		state.StateKeyWarnings:            warnings,
		state.StateKeyPermissionDecisions: decisions,
		state.StateKeySandboxResults:      sandboxResults,
		state.StateKeyOutputDir:           tmpDir,
	}
	_, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	jsonData, err := os.ReadFile(filepath.Join(tmpDir, "task-full_report.json"))
	if err != nil {
		t.Fatalf("read json report: %v", err)
	}
	var report types.ReviewReport
	if err := json.Unmarshal(jsonData, &report); err != nil {
		t.Fatalf("json report invalid: %v", err)
	}
	if report.FindingsCount != 3 || report.WarningsCount != 1 {
		t.Errorf("counts wrong: f=%d w=%d", report.FindingsCount, report.WarningsCount)
	}
	if report.PermissionSummary.Total != 2 {
		t.Errorf("expected 2 perm decisions, got %d", report.PermissionSummary.Total)
	}
	if len(report.SandboxSummary) != 1 {
		t.Errorf("expected 1 sandbox summary, got %d", len(report.SandboxSummary))
	}
}
