//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestDurableStorePersistsReviewRecords(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review_agent.db")
	s, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer s.Close()

	task := review.ReviewTask{
		ID:        "task-1",
		Status:    review.TaskStatusRunning,
		InputType: review.InputTypeFixture,
		DiffHash:  "hash",
		StartedAt: time.Unix(1, 0),
	}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if err := s.RecordInput(ctx, InputRecord{
		TaskID:           task.ID,
		DiffSummary:      "password=supersecretvalue",
		ChangedFilesJSON: "[]",
		RedactedDiff:     "token=supersecretvalue",
	}); err != nil {
		t.Fatalf("RecordInput() error = %v", err)
	}
	if err := s.RecordSandboxRun(ctx, review.SandboxRun{
		ID:      "run-1",
		TaskID:  task.ID,
		Runtime: "fake",
		Command: "go test ./...",
		Status:  "passed",
	}); err != nil {
		t.Fatalf("RecordSandboxRun() error = %v", err)
	}
	if err := s.RecordPermissionDecision(ctx, review.PermissionDecisionRecord{
		ID:              "perm-1",
		TaskID:          task.ID,
		ToolName:        "workspace_exec",
		FrameworkAction: "allow",
		SafetyDecision:  "allow",
		CreatedAt:       time.Unix(2, 0),
	}); err != nil {
		t.Fatalf("RecordPermissionDecision() error = %v", err)
	}
	findings := []review.Finding{{
		Severity:       review.SeverityCritical,
		Category:       "security",
		File:           "pkg/config.go",
		Line:           4,
		Title:          "Secret",
		Evidence:       "password=supersecretvalue",
		Recommendation: "rotate",
		Confidence:     0.99,
		Source:         "test",
		RuleID:         "security.secret_leak",
		Status:         review.FindingStatusFinding,
		Fingerprint:    "fp-1",
	}}
	if err := s.SaveFindings(ctx, task.ID, findings); err != nil {
		t.Fatalf("SaveFindings() error = %v", err)
	}
	if err := s.SaveFindings(ctx, task.ID, findings); err != nil {
		t.Fatalf("SaveFindings() duplicate error = %v", err)
	}
	if err := s.SaveArtifacts(ctx, []review.ArtifactRecord{{
		ID:        "art-1",
		TaskID:    task.ID,
		Kind:      "json_report",
		Path:      "review_report.json",
		MimeType:  "application/json",
		SHA256:    "abc",
		CreatedAt: time.Unix(3, 0),
	}}); err != nil {
		t.Fatalf("SaveArtifacts() error = %v", err)
	}
	if err := s.SaveReport(ctx, ReportRecord{
		TaskID:       task.ID,
		JSONPath:     "review_report.json",
		MarkdownPath: "review_report.md",
		Conclusion:   "passed",
		MetricsJSON:  "{}",
	}); err != nil {
		t.Fatalf("SaveReport() error = %v", err)
	}
	finishedAt := time.Unix(4, 0).UTC()
	if err := s.FinishTask(ctx, task.ID, review.TaskStatusPassed, "", finishedAt); err != nil {
		t.Fatalf("FinishTask() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("reopen NewSQLite() error = %v", err)
	}
	defer reopened.Close()
	loaded, err := reopened.LoadTaskReport(ctx, task.ID)
	if err != nil {
		t.Fatalf("LoadTaskReport() error = %v", err)
	}
	if loaded.Task.Status != review.TaskStatusPassed {
		t.Fatalf("loaded status = %q, want passed", loaded.Task.Status)
	}
	if loaded.Task.FinishedAt == nil || !loaded.Task.FinishedAt.Equal(finishedAt) {
		t.Fatalf("loaded finished_at = %v, want %v", loaded.Task.FinishedAt, finishedAt)
	}
	if len(loaded.Findings) != 1 || len(loaded.SandboxRuns) != 1 ||
		len(loaded.PermissionDecisions) != 1 || len(loaded.Artifacts) != 1 {
		t.Fatalf("loaded report missing records: %#v", loaded)
	}
	serialized := loaded.Input.DiffSummary + loaded.Input.RedactedDiff + loaded.Findings[0].Evidence
	if strings.Contains(serialized, "supersecretvalue") {
		t.Fatalf("loaded records leaked secret: %s", serialized)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(store) error = %v", err)
	}
	if strings.Contains(string(raw), "supersecretvalue") {
		t.Fatalf("store file leaked secret: %s", raw)
	}
}

func TestDurableStoreSaveFindingsComputesMissingFingerprints(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review_agent.db")
	s, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer s.Close()

	task := review.ReviewTask{
		ID:        "task-1",
		Status:    review.TaskStatusRunning,
		InputType: review.InputTypeFixture,
		DiffHash:  "hash",
		StartedAt: time.Unix(1, 0),
	}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	findings := []review.Finding{
		{
			Severity:   review.SeverityHigh,
			Category:   "security",
			File:       "pkg/a.go",
			Line:       10,
			Title:      "first",
			Confidence: 0.9,
			Source:     "test",
			RuleID:     "rule.one",
			Status:     review.FindingStatusFinding,
		},
		{
			Severity:   review.SeverityHigh,
			Category:   "security",
			File:       "pkg/b.go",
			Line:       20,
			Title:      "second",
			Confidence: 0.9,
			Source:     "test",
			RuleID:     "rule.two",
			Status:     review.FindingStatusFinding,
		},
	}
	if err := s.SaveFindings(ctx, task.ID, findings); err != nil {
		t.Fatalf("SaveFindings() error = %v", err)
	}
	loaded, err := s.LoadTaskReport(ctx, task.ID)
	if err != nil {
		t.Fatalf("LoadTaskReport() error = %v", err)
	}
	if len(loaded.Findings) != 2 {
		t.Fatalf("loaded findings = %d, want 2: %#v", len(loaded.Findings), loaded.Findings)
	}
	for _, finding := range loaded.Findings {
		if finding.Fingerprint == "" {
			t.Fatalf("finding has empty fingerprint: %#v", finding)
		}
	}
}

func TestDurableStoreRedactsQuotedChangedFilesBeforeMarshal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review_agent.db")
	s, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer s.Close()
	if err := s.CreateTask(ctx, review.ReviewTask{ID: "task-quoted", Status: review.TaskStatusRunning}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	changedFiles, err := json.Marshal([]review.DiffFile{{
		OldPath: "pkg/config.go",
		NewPath: "pkg/config.go",
		Hunks: []review.DiffHunk{{Lines: []review.DiffLine{
			{Kind: "+", Content: `password="quoted-password-value"`},
			{Kind: "+", Content: `token="quoted-token-value"`},
			{Kind: "+", Content: `api_key="quoted-api-key-value"`},
		}}},
	}})
	if err != nil {
		t.Fatalf("Marshal(changed files) error = %v", err)
	}
	if err := s.RecordInput(ctx, InputRecord{TaskID: "task-quoted", ChangedFilesJSON: string(changedFiles)}); err != nil {
		t.Fatalf("RecordInput() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(store) error = %v", err)
	}
	for _, secret := range []string{"quoted-password-value", "quoted-token-value", "quoted-api-key-value"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("store leaked quoted secret %q: %s", secret, raw)
		}
	}
}

func TestDurableStoreIndependentHandlesPreserveUpdates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review_agent.db")
	first, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLite(first) error = %v", err)
	}
	defer first.Close()
	second, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLite(second) error = %v", err)
	}
	defer second.Close()
	for _, item := range []struct {
		store *DurableStore
		id    string
	}{
		{store: first, id: "task-first"},
		{store: second, id: "task-second"},
	} {
		if err := item.store.CreateTask(ctx, review.ReviewTask{ID: item.id, Status: review.TaskStatusRunning}); err != nil {
			t.Fatalf("CreateTask(%s) error = %v", item.id, err)
		}
	}
	reopened, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLite(reopened) error = %v", err)
	}
	defer reopened.Close()
	for _, id := range []string{"task-first", "task-second"} {
		if _, err := reopened.LoadTaskReport(ctx, id); err != nil {
			t.Fatalf("LoadTaskReport(%s) error = %v", id, err)
		}
	}
}

func TestReviewTaskOmitsZeroFinishedAt(t *testing.T) {
	task := review.ReviewTask{
		ID:        "task-1",
		Status:    review.TaskStatusRunning,
		InputType: review.InputTypeFixture,
		DiffHash:  "hash",
		StartedAt: time.Unix(1, 0),
	}
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(raw), "finished_at") {
		t.Fatalf("unfinished task serialized finished_at: %s", raw)
	}
}

func TestDurableStoreFinishTaskUsesProvidedTimestamp(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review_agent.db")
	s, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer s.Close()

	task := review.ReviewTask{
		ID:        "task-1",
		Status:    review.TaskStatusRunning,
		InputType: review.InputTypeFixture,
		DiffHash:  "hash",
		StartedAt: time.Unix(1, 0).UTC(),
	}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	finishedAt := time.Date(2026, 7, 6, 1, 2, 3, 0, time.UTC)
	if err := s.FinishTask(ctx, task.ID, review.TaskStatusFailed, "boom", finishedAt); err != nil {
		t.Fatalf("FinishTask() error = %v", err)
	}

	loaded, err := s.LoadTaskReport(ctx, task.ID)
	if err != nil {
		t.Fatalf("LoadTaskReport() error = %v", err)
	}
	if loaded.Task.FinishedAt == nil || !loaded.Task.FinishedAt.Equal(finishedAt) {
		t.Fatalf("loaded finished_at = %v, want %v", loaded.Task.FinishedAt, finishedAt)
	}
	if loaded.Task.Error != "boom" {
		t.Fatalf("loaded error = %q, want boom", loaded.Task.Error)
	}
}
