//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/telemetry"
)

// sampleMetrics returns a telemetry.Summary populated with non-zero values
// so the Metrics section of the rendered report has meaningful content.
func sampleMetrics() telemetry.Summary {
	return telemetry.Summary{
		TotalDuration:     1500 * time.Millisecond,
		SandboxDuration:   800 * time.Millisecond,
		ToolCalls:         4,
		PermissionBlocked: 1,
		FindingCount:      2,
		SeverityCounts: map[string]int64{
			"critical": 1,
			"high":     1,
		},
		ExceptionTypes: map[string]int64{
			"sandbox_timeout": 1,
		},
	}
}

// TestBuildConclusionFail covers the case where a critical finding is
// present: the conclusion must be "fail" regardless of sandbox failures or
// permission decisions.
func TestBuildConclusionFail(t *testing.T) {
	rev := &review.Report{
		TaskID: "task-fail",
		Findings: []review.Finding{
			{
				TaskID: "task-fail", Severity: "critical", Category: "secret",
				File: "a.go", Line: 10, Title: "Hardcoded secret",
				Evidence: "x", Recommendation: "remove it",
				Confidence: 0.9, Source: "rule:SI-001", RuleID: "SI-001",
			},
			{
				TaskID: "task-fail", Severity: "high", Category: "error",
				File: "b.go", Line: 20, Title: "Unchecked error",
				Evidence: "y", Recommendation: "check the error",
				Confidence: 0.8, Source: "rule:EH-001", RuleID: "EH-001",
			},
		},
		Warnings: []review.Warning{
			{
				Finding: review.Finding{
					Severity: "low", File: "c.go", Line: 1, Title: "low conf item",
				},
				Reason: "low confidence: 0.40",
			},
		},
		NeedsHumanReview: []review.Finding{
			{
				Severity: "critical", File: "d.go", Line: 5, Title: "needs review",
			},
		},
	}
	runs := []sandbox.RunResult{
		{Status: sandbox.StatusFailed, ExitCode: 1, Duration: time.Second},
	}
	perms := []store.PermissionDecision{
		{TaskID: "task-fail", Command: "rm -rf /", Action: "deny", Reason: "dangerous"},
	}
	arts := []store.Artifact{
		{Name: "report.md", Path: "/tmp/report.md", SizeBytes: 100},
	}

	rd := Build("task-fail", rev, runs, perms, arts, sampleMetrics(), PRMetadata{})

	if rd.Conclusion != ConclusionFail {
		t.Fatalf("conclusion = %q, want %q", rd.Conclusion, ConclusionFail)
	}
	if rd.TotalFindings != 2 {
		t.Errorf("TotalFindings = %d, want 2", rd.TotalFindings)
	}
	if rd.TotalWarnings != 1 {
		t.Errorf("TotalWarnings = %d, want 1", rd.TotalWarnings)
	}
	if rd.NeedsHumanReview != 1 {
		t.Errorf("NeedsHumanReview = %d, want 1", rd.NeedsHumanReview)
	}
	if rd.PermissionBlocked != 1 {
		t.Errorf("PermissionBlocked = %d, want 1", rd.PermissionBlocked)
	}
	if got := rd.SeverityStats["critical"]; got != 1 {
		t.Errorf("SeverityStats[critical] = %d, want 1", got)
	}
	if got := rd.SeverityStats["high"]; got != 1 {
		t.Errorf("SeverityStats[high] = %d, want 1", got)
	}
}

// TestBuildConclusionPass covers the clean case: a single medium finding and
// no sandbox failures yields a "pass" conclusion.
func TestBuildConclusionPass(t *testing.T) {
	rev := &review.Report{
		TaskID: "task-pass",
		Findings: []review.Finding{
			{
				TaskID: "task-pass", Severity: "medium", File: "x.go", Line: 1,
				Title: "minor issue", Evidence: "e", Recommendation: "r",
				Confidence: 0.7, RuleID: "RL-001",
			},
		},
	}
	rd := Build("task-pass", rev, nil, nil, nil, telemetry.Summary{}, PRMetadata{})
	if rd.Conclusion != ConclusionPass {
		t.Fatalf("conclusion = %q, want %q", rd.Conclusion, ConclusionPass)
	}
}

// TestBuildConclusionNeedsReview covers a sandbox timeout with no findings:
// the conclusion must be "needs_human_review".
func TestBuildConclusionNeedsReview(t *testing.T) {
	rev := &review.Report{TaskID: "task-needs"}
	runs := []sandbox.RunResult{
		{Status: sandbox.StatusTimeout, TimedOut: true, Duration: 2 * time.Second},
	}
	rd := Build("task-needs", rev, runs, nil, nil, telemetry.Summary{}, PRMetadata{})
	if rd.Conclusion != ConclusionNeedsReview {
		t.Fatalf("conclusion = %q, want %q", rd.Conclusion, ConclusionNeedsReview)
	}
}

// TestWriteAllFiles verifies WriteAll creates both the JSON and Markdown
// files in the output directory. The filenames must include the task id so
// concurrent or repeated runs do not clobber each other's reports.
func TestWriteAllFiles(t *testing.T) {
	rev := &review.Report{
		TaskID: "task-write",
		Findings: []review.Finding{
			{
				TaskID: "task-write", Severity: "high", File: "a.go", Line: 3,
				Title: "issue", Evidence: "ev", Recommendation: "fix it",
				Confidence: 0.8, RuleID: "EH-001",
			},
		},
	}
	rd := Build("task-write", rev, nil, nil, nil, sampleMetrics(), PRMetadata{})

	dir := t.TempDir()
	jsonPath, mdPath, err := rd.WriteAll(dir)
	if err != nil {
		t.Fatalf("WriteAll returned error: %v", err)
	}
	for _, p := range []string{jsonPath, mdPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file %q to exist: %v", p, err)
		}
	}
	if got := filepath.Base(jsonPath); got != "review_report_task-write.json" {
		t.Errorf("json base name = %q, want review_report_task-write.json", got)
	}
	if got := filepath.Base(mdPath); got != "review_report_task-write.md" {
		t.Errorf("md base name = %q, want review_report_task-write.md", got)
	}
}

// TestReportFileNameDoesNotClobber verifies that writing reports for two
// different task ids in the same directory produces distinct files and that
// the second write does not overwrite the first. This is a regression test
// for the bug where all tasks wrote the same fixed filename.
func TestReportFileNameDoesNotClobber(t *testing.T) {
	dir := t.TempDir()

	mk := func(taskID string) *ReportData {
		rev := &review.Report{
			TaskID: taskID,
			Findings: []review.Finding{
				{
					TaskID: taskID, Severity: "medium", File: "x.go", Line: 1,
					Title: "t", Evidence: "e", Recommendation: "r",
					Confidence: 0.7, RuleID: "RL-001",
				},
			},
		}
		return Build(taskID, rev, nil, nil, nil, telemetry.Summary{}, PRMetadata{})
	}

	rd1 := mk("cr-20260101-120000-aaaaaaaa-1111")
	rd2 := mk("cr-20260101-120000-bbbbbbbb-2222")

	jp1, mp1, err := rd1.WriteAll(dir)
	if err != nil {
		t.Fatalf("first WriteAll: %v", err)
	}
	jp2, mp2, err := rd2.WriteAll(dir)
	if err != nil {
		t.Fatalf("second WriteAll: %v", err)
	}
	if jp1 == jp2 {
		t.Fatalf("two task ids produced the same json path %q", jp1)
	}
	if mp1 == mp2 {
		t.Fatalf("two task ids produced the same md path %q", mp1)
	}
	// Both JSON files must coexist on disk.
	for _, p := range []string{jp1, jp2, mp1, mp2} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file %q to still exist after second write: %v", p, err)
		}
	}
}

// TestReportFileNameSanitizesTraversal verifies that a hostile task id
// containing path separators or traversal sequences cannot escape outDir.
// The sanitized id must contain only [A-Za-z0-9._-] characters.
func TestReportFileNameSanitizesTraversal(t *testing.T) {
	dir := t.TempDir()
	rev := &review.Report{TaskID: "../../etc/passwd", Findings: []review.Finding{}}
	rd := Build("../../etc/passwd", rev, nil, nil, nil, telemetry.Summary{}, PRMetadata{})

	jp, _, err := rd.WriteAll(dir)
	if err != nil {
		t.Fatalf("WriteAll: %v", err)
	}
	// The written file must live inside dir, not somewhere above it.
	absDir, _ := filepath.Abs(dir)
	rel, err := filepath.Rel(absDir, jp)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Fatalf("json path %q escaped outDir %q (rel=%q)", jp, absDir, rel)
	}
	// Sanitized filename should not contain '/' or '\\'.
	base := filepath.Base(jp)
	if strings.ContainsAny(base, `/\`) {
		t.Errorf("sanitized base name %q contains a path separator", base)
	}
}

// TestPRMetadataInHeader verifies that when PRMetadata is populated, the
// PR title/author/branch are rendered in the Markdown report header so
// CI-generated reports carry reviewer context. Borrowed from competitor
// PR #2090.
func TestPRMetadataInHeader(t *testing.T) {
	rev := &review.Report{TaskID: "task-pr", Findings: []review.Finding{}}
	rd := Build("task-pr", rev, nil, nil, nil, telemetry.Summary{}, PRMetadata{
		Title:  "Fix auth flow",
		Author: "alice",
		Branch: "feature/auth",
	})

	dir := t.TempDir()
	mdPath, err := rd.ToMarkdown(dir)
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	body, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	for _, want := range []string{"Fix auth flow", "alice", "feature/auth"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("markdown header missing %q", want)
		}
	}
}

// TestJSONRoundTrip verifies the JSON output can be unmarshaled back into a
// ReportData with matching TaskID and TotalFindings.
func TestJSONRoundTrip(t *testing.T) {
	rev := &review.Report{
		TaskID: "task-json",
		Findings: []review.Finding{
			{
				TaskID: "task-json", Severity: "medium", File: "m.go", Line: 7,
				Title: "t", Evidence: "e", Recommendation: "r",
				Confidence: 0.7, RuleID: "RL-001",
			},
			{
				TaskID: "task-json", Severity: "low", File: "n.go", Line: 9,
				Title: "t2", Evidence: "e2", Recommendation: "r2",
				Confidence: 0.7, RuleID: "RL-002",
			},
		},
	}
	rd := Build("task-json", rev, nil, nil, nil, telemetry.Summary{}, PRMetadata{})

	dir := t.TempDir()
	jsonPath, err := rd.ToJSON(dir)
	if err != nil {
		t.Fatalf("ToJSON returned error: %v", err)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var back ReportData
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.TaskID != rd.TaskID {
		t.Errorf("TaskID = %q, want %q", back.TaskID, rd.TaskID)
	}
	if back.TotalFindings != rd.TotalFindings {
		t.Errorf("TotalFindings = %d, want %d", back.TotalFindings, rd.TotalFindings)
	}
}

// TestMarkdownContent verifies the Markdown report contains the expected
// section headers and severity stats.
func TestMarkdownContent(t *testing.T) {
	rev := &review.Report{
		TaskID: "task-md",
		Findings: []review.Finding{
			{
				TaskID: "task-md", Severity: "critical", File: "a.go", Line: 1,
				Title: "critical issue", Evidence: "e", Recommendation: "r",
				Confidence: 0.9, RuleID: "SI-001",
			},
		},
	}
	rd := Build("task-md", rev, nil, nil, nil, sampleMetrics(), PRMetadata{})

	dir := t.TempDir()
	mdPath, err := rd.ToMarkdown(dir)
	if err != nil {
		t.Fatalf("ToMarkdown returned error: %v", err)
	}
	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		"# Code Review Report",
		"## Findings",
		"## Metrics",
		"## Findings Summary",
		"## Sandbox Execution Summary",
		"## Governance (Permission Decisions)",
		"## Executable Recommendations",
		"critical",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	// Severity summary table should record the critical count of 1.
	if !strings.Contains(s, "| critical | 1 |") {
		t.Errorf("markdown missing critical severity stat row, got:\n%s", s)
	}
}

// TestNoPlaintextSecrets verifies that a secret placed in finding evidence is
// redacted by Build and does not appear in either rendered file.
func TestNoPlaintextSecrets(t *testing.T) {
	const secret = "sk-abc123"
	rev := &review.Report{
		TaskID: "task-secret",
		Findings: []review.Finding{
			{
				TaskID: "task-secret", Severity: "high", File: "s.go", Line: 1,
				Title:          "leaked key",
				Evidence:       "API_KEY = " + secret,
				Recommendation: "remove the api key from source",
				Confidence:     0.9, RuleID: "SI-001",
			},
		},
	}
	rd := Build("task-secret", rev, nil, nil, nil, telemetry.Summary{}, PRMetadata{})

	dir := t.TempDir()
	jsonPath, mdPath, err := rd.WriteAll(dir)
	if err != nil {
		t.Fatalf("WriteAll returned error: %v", err)
	}
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	mdData, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if strings.Contains(string(jsonData), secret) {
		t.Errorf("plaintext secret %q found in JSON output", secret)
	}
	if strings.Contains(string(mdData), secret) {
		t.Errorf("plaintext secret %q found in Markdown output", secret)
	}
	// The redaction marker should be present instead.
	if !strings.Contains(string(jsonData), "[REDACTED:") {
		t.Errorf("expected redaction marker in JSON output, got:\n%s", string(jsonData))
	}
}

// TestSeveritySortStability verifies that two findings with the same severity
// preserve their input order in the Markdown findings table.
func TestSeveritySortStability(t *testing.T) {
	rev := &review.Report{
		TaskID: "task-stable",
		Findings: []review.Finding{
			{
				TaskID: "task-stable", Severity: "high", File: "first.go", Line: 1,
				Title: "First", Evidence: "e1", Recommendation: "r1",
				Confidence: 0.9, RuleID: "EH-001",
			},
			{
				TaskID: "task-stable", Severity: "high", File: "second.go", Line: 2,
				Title: "Second", Evidence: "e2", Recommendation: "r2",
				Confidence: 0.9, RuleID: "EH-002",
			},
		},
	}
	rd := Build("task-stable", rev, nil, nil, nil, telemetry.Summary{}, PRMetadata{})

	dir := t.TempDir()
	mdPath, err := rd.ToMarkdown(dir)
	if err != nil {
		t.Fatalf("ToMarkdown returned error: %v", err)
	}
	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	s := string(data)
	idxFirst := strings.Index(s, "First")
	idxSecond := strings.Index(s, "Second")
	if idxFirst < 0 || idxSecond < 0 {
		t.Fatalf("expected both findings in markdown, got:\n%s", s)
	}
	if idxFirst >= idxSecond {
		t.Errorf("expected First before Second (stable sort); first=%d second=%d",
			idxFirst, idxSecond)
	}
}
