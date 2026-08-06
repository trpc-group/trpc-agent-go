//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestDryRunCompletesReportsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reviews.sqlite")
	report, paths, err := Run(context.Background(), Config{
		Fixture: "secret", OutputDir: dir, DatabasePath: dbPath,
		DryRun: true, FakeModel: true, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Task.Status != "completed" || len(report.Findings) == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Mode != "deterministic-rule-only+fake-model" {
		t.Fatalf("unexpected mode: %s", report.Mode)
	}
	if !hasArtifactProvenance(report.Artifacts, "diff_stats.json", "synthetic_dry_run") {
		t.Fatalf("dry-run statistics provenance is missing: %+v", report.Artifacts)
	}
	for _, path := range []string{paths.JSON, paths.Markdown, dbPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stored, err := store.Load(context.Background(), report.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Task.ID != report.Task.ID || len(stored.PermissionDecisions) == 0 || len(stored.SandboxRuns) == 0 {
		t.Fatalf("stored report incomplete: %+v", stored)
	}
	task, err := store.LoadTask(context.Background(), report.Task.ID)
	if err != nil || task.Status != "completed" {
		t.Fatalf("task state is not queryable: %+v, %v", task, err)
	}
	runs, err := store.LoadRuns(context.Background(), report.Task.ID)
	if err != nil || len(runs) != len(report.SandboxRuns) {
		t.Fatalf("sandbox runs are not queryable: %+v, %v", runs, err)
	}
	decisions, err := store.LoadDecisions(context.Background(), report.Task.ID)
	if err != nil || len(decisions) != len(report.PermissionDecisions) {
		t.Fatalf("permission decisions are not queryable: %+v, %v", decisions, err)
	}
	filterDecisions, err := store.LoadFilterDecisions(context.Background(), report.Task.ID)
	if err != nil || len(filterDecisions) != len(report.FilterDecisions) {
		t.Fatalf("filter decisions are not queryable: %+v, %v", filterDecisions, err)
	}
	metrics, err := store.LoadMetrics(context.Background(), report.Task.ID)
	if err != nil || metrics.ToolCallCount != report.Metrics.ToolCallCount {
		t.Fatalf("metrics are not queryable: %+v, %v", metrics, err)
	}
	findings, err := store.LoadFindings(context.Background(), report.Task.ID, "finding")
	if err != nil || len(findings) != len(report.Findings) {
		t.Fatalf("findings are not queryable: %+v, %v", findings, err)
	}
	artifacts, err := store.LoadArtifacts(context.Background(), report.Task.ID)
	if err != nil || len(artifacts) != len(report.Artifacts) {
		t.Fatalf("artifacts are not queryable: %+v, %v", artifacts, err)
	}
	if !hasArtifactProvenance(artifacts, "diff_stats.json", "synthetic_dry_run") {
		t.Fatalf("normalized artifacts lost provenance: %+v", artifacts)
	}
	for _, path := range []string{paths.JSON, paths.Markdown, dbPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "sk-abcdefghijklmnopqrstuvwxyz123456") {
			t.Fatalf("plaintext secret leaked into %s", path)
		}
	}
	if err := store.Delete(context.Background(), report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), report.Task.ID); err == nil {
		t.Fatal("deleted report remained queryable")
	}
}

func TestInputFailureIsPersistedForAudit(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reviews.sqlite")
	_, _, err := Run(context.Background(), Config{
		TaskID: "input-failure", DiffFile: filepath.Join(dir, "missing.diff"), DryRun: true,
		OutputDir: dir, DatabasePath: dbPath,
	})
	if err == nil || !strings.Contains(err.Error(), "input-failure") {
		t.Fatalf("input failure did not return its task identity: %v", err)
	}
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Load(context.Background(), "input-failure")
	if err != nil || report.Task.Status != TaskFailed || report.Task.InputMode != "diff_file" || len(report.SandboxRuns) != 1 || report.Metrics.TotalDurationMS < report.Metrics.PreparationDurationMS || report.Metrics.TotalDurationScope != "failure_audit" {
		t.Fatalf("input failure was not persisted: report=%+v err=%v", report, err)
	}
}

func TestSandboxConfigurationFailureIsPersistedForAudit(t *testing.T) {
	for _, test := range []struct {
		name     string
		executor Executor
	}{
		{name: "unknown executor", executor: "unknown"},
		{name: "local without opt in", executor: ExecutorLocal},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "reviews.sqlite")
			taskID := strings.ReplaceAll(test.name, " ", "-")
			_, _, err := Run(context.Background(), Config{TaskID: taskID, Fixture: "clean", Executor: test.executor, OutputDir: dir, DatabasePath: dbPath})
			if err == nil || !strings.Contains(err.Error(), taskID) {
				t.Fatalf("configuration failure did not return task identity: %v", err)
			}
			store, err := openStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			report, err := store.Load(context.Background(), taskID)
			if err != nil || report.Task.Status != TaskFailed || len(report.SandboxRuns) != 1 || report.SandboxRuns[0].Command != "initialize_sandbox" || report.Metrics.TotalDurationMS < report.Metrics.PreparationDurationMS || report.Metrics.TotalDurationScope != "failure_audit" {
				t.Fatalf("sandbox configuration failure was not persisted: report=%+v err=%v", report, err)
			}
		})
	}
}

func TestReportRedactionCoversFileNamesBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reviews.sqlite")
	const secret = "sk-abcdefghijklmnopqrstuvwxyz"
	rawReport := Report{
		Task: Task{ID: "redacted-file", Status: TaskCompleted},
		Findings: []Finding{{
			File: "internal/api_key=" + secret + ".go", Evidence: "reviewed " + secret,
		}},
		Artifacts: []Artifact{{
			Name: "api_key=" + secret + ".json", Path: "out/api_key=" + secret + ".json", Provenance: "source=" + secret,
		}},
		Metrics: Metrics{SeverityDistribution: map[string]int{}},
	}
	report, paths, err := publish(rawReport, dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.Findings[0].File, secret) {
		t.Fatalf("plaintext secret remained in published file name: %q", report.Findings[0].File)
	}
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), rawReport); err != nil {
		t.Fatal(err)
	}
	jsonData, err := os.ReadFile(paths.JSON)
	if err != nil {
		t.Fatal(err)
	}
	dbData, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{string(jsonData), string(dbData)} {
		if strings.Contains(value, secret) {
			t.Fatal("plaintext filename secret crossed a persistence boundary")
		}
	}
}

func TestSandboxFailureDoesNotAbortReview(t *testing.T) {
	dir := t.TempDir()
	report, _, err := Run(context.Background(), Config{Fixture: "sandbox_failure", OutputDir: dir, DatabasePath: filepath.Join(dir, "reviews.sqlite"), Executor: "fake-fail", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if report.Task.Status != "completed" || !hasCategory(report.NeedsHumanReview, "sandbox") {
		t.Fatalf("failure was not retained: %+v", report)
	}
	for _, artifact := range report.Artifacts {
		if artifact.Name == "diff_stats.json" {
			t.Fatal("failed fake sandbox published an unaudited statistics artifact")
		}
	}
}

func TestSnapshotIncludesGoAndExcludesSecrets(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("TOKEN=plaintext\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, cleanup, err := safeSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(snapshot, "main.go")); err != nil {
		t.Fatal("go source missing from snapshot")
	}
	if _, err := os.Stat(filepath.Join(snapshot, ".env")); !os.IsNotExist(err) {
		t.Fatal("environment file entered snapshot")
	}
}

func TestMarkdownContainsRequiredAuditSections(t *testing.T) {
	report := Report{Task: Task{ID: "review-test", Status: "completed"}, Metrics: Metrics{SeverityDistribution: map[string]int{}}, Conclusion: "done"}
	text := string(renderMarkdown(report))
	for _, section := range []string{"## Summary", "## Findings", "## Human review", "## Governance decisions", "## Filter decisions", "## Sandbox summary", "## Monitoring", "## Conclusion"} {
		if !strings.Contains(text, section) {
			t.Fatalf("missing %s", section)
		}
	}
}

func TestMarkdownEscapesUntrustedFields(t *testing.T) {
	report := Report{
		Task:       Task{ID: "review-test", Status: TaskCompleted},
		Findings:   []Finding{{Severity: SeverityHigh, Category: "x|y", File: "<script>|a.go", Title: "`title`", Evidence: "line1\nline2", Recommendation: "<b>fix</b>"}},
		Metrics:    Metrics{SeverityDistribution: map[string]int{}},
		Conclusion: "<script>alert(1)</script>",
	}
	text := string(renderMarkdown(report))
	for _, unsafe := range []string{"<script>", "<b>", "x|y", "`title`"} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("unsafe Markdown remained in report: %q", unsafe)
		}
	}
	if !strings.Contains(text, `x\|y`) || !strings.Contains(text, "&lt;script&gt;") {
		t.Fatalf("escaped fields missing: %s", text)
	}
}

func TestPublishCommitsReportPairOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	report := Report{Task: Task{ID: "pair", Status: TaskCompleted}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	_, paths, err := publish(report, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.JSON, paths.Markdown} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("published pair is incomplete: %v", err)
		}
	}
	if _, _, err := publish(report, dir); err == nil {
		t.Fatal("existing report pair was overwritten")
	}
}

func TestRunPersistsFailureWhenReportStagingFails(t *testing.T) {
	dir := t.TempDir()
	taskID := "commit-collision"
	if err := os.MkdirAll(filepath.Join(dir, taskID, "report"), 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "reviews.sqlite")
	if _, _, err := Run(context.Background(), Config{TaskID: taskID, Fixture: "clean", DryRun: true, OutputDir: dir, DatabasePath: dbPath}); err == nil {
		t.Fatal("report staging collision was ignored")
	}
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Load(context.Background(), taskID)
	if err != nil || report.Task.Status != TaskFailed || len(report.SandboxRuns) == 0 || report.SandboxRuns[len(report.SandboxRuns)-1].Command != "stage_report" || report.Metrics.TotalDurationMS < report.Metrics.PreparationDurationMS || report.Metrics.TotalDurationScope != "failure_audit" {
		t.Fatalf("failed publication was not retained for audit: report=%+v err=%v", report, err)
	}
}

func TestRunPersistsFailureWhenFinalizationFails(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reviews.sqlite")
	factory := func(_ context.Context, cfg Config) (Store, error) {
		store, err := openStore(cfg.DatabasePath)
		if err != nil {
			return nil, err
		}
		return &failOnceFinalizeStore{Store: store, err: errors.New("injected finalization failure")}, nil
	}
	_, _, err := Run(context.Background(), Config{
		TaskID: "finalization-failure", Fixture: "clean", DryRun: true,
		OutputDir: dir, DatabasePath: dbPath, StoreFactory: factory,
	})
	if err == nil || !strings.Contains(err.Error(), "finalize_report") {
		t.Fatalf("finalization failure was not returned: %v", err)
	}
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Load(context.Background(), "finalization-failure")
	if err != nil || report.Task.Status != TaskFailed || len(report.SandboxRuns) == 0 || report.SandboxRuns[len(report.SandboxRuns)-1].Command != "finalize_report" || report.Metrics.TotalDurationMS < report.Metrics.PreparationDurationMS || report.Metrics.TotalDurationScope != "failure_audit" {
		t.Fatalf("finalization failure was not retained for audit: report=%+v err=%v", report, err)
	}
	runs, err := store.LoadRuns(context.Background(), "finalization-failure")
	if err != nil || len(runs) != len(report.SandboxRuns) || runs[len(runs)-1].Command != "finalize_report" {
		t.Fatalf("normalized failure runs diverged from report: runs=%+v report=%+v err=%v", runs, report.SandboxRuns, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "finalization-failure")); !os.IsNotExist(err) {
		t.Fatalf("published report remained after finalization failed: %v", err)
	}
}

func TestRunRecoversPublishedRunningTask(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reviews.sqlite")
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Round(0)
	stored := Report{
		Task:    Task{ID: "recover-publication", Status: TaskRunning, InputMode: "fixture", StartedAt: now, EndedAt: now},
		Input:   DiffSummary{Digest: "recover-digest"},
		Metrics: Metrics{SeverityDistribution: map[string]int{}},
	}
	if err := store.Save(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	published := stored
	published.Task.Status = TaskCompleted
	published.Task.EndedAt = now.Add(time.Second)
	published.Metrics.TotalDurationMS = 1
	published.Metrics.TotalDurationScope = "pre_publication_snapshot"
	_, _, staged, err := stageReport(published, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, paths, err := Run(context.Background(), Config{
		TaskID: "recover-publication", Fixture: "clean", DryRun: true,
		OutputDir: dir, DatabasePath: dbPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Task.Status != TaskCompleted || recovered.Metrics.TotalDurationScope != "recovered_publication" {
		t.Fatalf("published running task was not recovered: %+v", recovered)
	}
	if _, err := os.Stat(paths.JSON); err != nil {
		t.Fatalf("recovered report was removed: %v", err)
	}
}

func TestRecoverPublishedReviewRollsBackIncompleteOrMalformedPublication(t *testing.T) {
	for _, test := range []struct {
		name    string
		publish func(t *testing.T, dir, taskID string)
	}{
		{name: "no published directory"},
		{
			name: "missing markdown report",
			publish: func(t *testing.T, dir, taskID string) {
				path := filepath.Join(dir, taskID, "report")
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(path, "review_report.json"), "{}")
			},
		},
		{
			name: "mismatched published task",
			publish: func(t *testing.T, dir, taskID string) {
				path := filepath.Join(dir, taskID, "report")
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(path, "review_report.json"), `{"task":{"id":"other","status":"completed"}}`)
				writeFile(t, filepath.Join(path, "review_report.md"), "# incomplete")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := openStore(filepath.Join(dir, "reviews.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			const taskID = "rollback-publication"
			now := time.Now().UTC().Round(0)
			report := Report{
				Task:  Task{ID: taskID, Status: TaskRunning, InputMode: "fixture", StartedAt: now, EndedAt: now},
				Input: DiffSummary{Digest: "rollback-digest"}, Metrics: Metrics{SeverityDistribution: map[string]int{}},
			}
			if err := store.Save(context.Background(), report); err != nil {
				t.Fatal(err)
			}
			if test.publish != nil {
				test.publish(t, dir, taskID)
			}
			_, _, recovered, err := recoverPublishedReview(context.Background(), store, Config{OutputDir: dir}, taskID)
			if err != nil {
				t.Fatal(err)
			}
			if recovered {
				t.Fatal("incomplete publication was recovered")
			}
			_, err = store.Load(context.Background(), taskID)
			if err == nil {
				t.Fatal("rolled-back task remained in store")
			}
			_, err = os.Stat(filepath.Join(dir, taskID))
			if !os.IsNotExist(err) {
				t.Fatalf("rolled-back report directory remained: %v", err)
			}
		})
	}
}

func TestRecoverPublishedReviewLeavesTerminalTasksToDuplicateProtection(t *testing.T) {
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Round(0)
	report := Report{Task: Task{ID: "completed", Status: TaskCompleted, InputMode: "fixture", StartedAt: now, EndedAt: now}, Input: DiffSummary{Digest: "digest"}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	if err := store.Save(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	_, _, recovered, err := recoverPublishedReview(context.Background(), store, Config{OutputDir: dir}, report.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered {
		t.Fatal("terminal task was unexpectedly recovered")
	}
	loaded, err := store.Load(context.Background(), report.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Task.Status != TaskCompleted {
		t.Fatalf("terminal status changed: %s", loaded.Task.Status)
	}
}

func TestRecoverPublishedReviewRetainsAuditOnReadOrFinalizeFailure(t *testing.T) {
	t.Run("unreadable published JSON", func(t *testing.T) {
		dir := t.TempDir()
		store, err := openStore(filepath.Join(dir, "reviews.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		report := saveRunningRecoveryReport(t, store, "read-failure")
		path := filepath.Join(dir, report.Task.ID, "report", "review_report.json")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, _, recovered, err := recoverPublishedReview(context.Background(), store, Config{OutputDir: dir}, report.Task.ID); err == nil || !recovered {
			t.Fatalf("unreadable publication was not retained for inspection: recovered=%t err=%v", recovered, err)
		}
		if _, err := store.Load(context.Background(), report.Task.ID); err != nil {
			t.Fatalf("running audit was deleted after read failure: %v", err)
		}
	})

	t.Run("finalize failure", func(t *testing.T) {
		dir := t.TempDir()
		store, err := openStore(filepath.Join(dir, "reviews.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		report := saveRunningRecoveryReport(t, store, "finalize-failure")
		published := report
		published.Task.Status = TaskCompleted
		published.Task.EndedAt = time.Now()
		published.Metrics.TotalDurationScope = "pre_publication_snapshot"
		_, _, staged, err := stageReport(published, dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := staged.commit(); err != nil {
			t.Fatal(err)
		}
		failing := &failOnceFinalizeStore{Store: store, err: errors.New("finalize unavailable")}
		if _, _, recovered, err := recoverPublishedReview(context.Background(), failing, Config{OutputDir: dir}, report.Task.ID); err == nil || !recovered {
			t.Fatalf("finalization failure was not retained for retry: recovered=%t err=%v", recovered, err)
		}
		loaded, err := store.Load(context.Background(), report.Task.ID)
		if err != nil || loaded.Task.Status != TaskRunning {
			t.Fatalf("running audit changed after failed recovery: report=%+v err=%v", loaded, err)
		}
	})
}

func saveRunningRecoveryReport(t *testing.T, store Store, taskID string) Report {
	t.Helper()
	now := time.Now().UTC().Round(0)
	report := Report{Task: Task{ID: taskID, Status: TaskRunning, InputMode: "fixture", StartedAt: now, EndedAt: now}, Input: DiffSummary{Digest: taskID}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	if err := store.Save(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestRunPublishesReportBeforeCompletingStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reviews.sqlite")
	factory := func(_ context.Context, cfg Config) (Store, error) {
		store, err := openStore(cfg.DatabasePath)
		if err != nil {
			return nil, err
		}
		return &requirePublishedStore{Store: store, outputDir: cfg.OutputDir}, nil
	}
	if _, _, err := Run(context.Background(), Config{TaskID: "ordered-publication", Fixture: "clean", DryRun: true, OutputDir: dir, DatabasePath: dbPath, StoreFactory: factory}); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactProvenanceMigrationFromLegacySchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE artifacts (task_id TEXT NOT NULL, name TEXT NOT NULL, path TEXT NOT NULL, mime_type TEXT NOT NULL, size_bytes INTEGER NOT NULL, PRIMARY KEY(task_id, name)); INSERT INTO artifacts(task_id,name,path,mime_type,size_bytes) VALUES('legacy','old.json','old.json','application/json',3);`)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	legacy, err := store.LoadArtifacts(context.Background(), "legacy")
	if err != nil || len(legacy) != 1 || legacy[0].Provenance != "" {
		t.Fatalf("legacy artifact migration failed: artifacts=%+v err=%v", legacy, err)
	}
	report := Report{Task: Task{ID: "new", Status: TaskCompleted}, Artifacts: []Artifact{{Name: "new.json", Path: "new.json", MIMEType: "application/json", SizeBytes: 3, Provenance: "validated_sandbox_script"}}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	if err := store.Save(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	artifacts, err := store.LoadArtifacts(context.Background(), "new")
	if err != nil || !hasArtifactProvenance(artifacts, "new.json", "validated_sandbox_script") {
		t.Fatalf("migrated schema did not retain new provenance: artifacts=%+v err=%v", artifacts, err)
	}
}

func TestDuplicateTaskDoesNotOverwriteArtifacts(t *testing.T) {
	dir := t.TempDir()
	taskID := "duplicate-no-clobber"
	dbPath := filepath.Join(dir, "reviews.sqlite")
	_, paths, err := Run(context.Background(), Config{
		TaskID: taskID, Fixture: "clean", DryRun: true,
		OutputDir: dir, DatabasePath: dbPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	statsPath := filepath.Join(dir, taskID, "diff_stats.json")
	statsBefore, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatal(err)
	}
	reportBefore, err := os.ReadFile(paths.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Run(context.Background(), Config{
		TaskID: taskID, Fixture: "secret", DryRun: true,
		OutputDir: dir, DatabasePath: dbPath,
	}); err == nil {
		t.Fatal("duplicate task unexpectedly succeeded")
	}
	statsAfter, _ := os.ReadFile(statsPath)
	reportAfter, _ := os.ReadFile(paths.JSON)
	if string(statsAfter) != string(statsBefore) || string(reportAfter) != string(reportBefore) {
		t.Fatal("duplicate task overwrote previously published artifacts")
	}
}

func TestArtifactPathsUseForwardSlashes(t *testing.T) {
	outputDir := t.TempDir()
	stats, err := diffStatsArtifact(deterministicDiffStats(DiffSummary{}), "synthetic_dry_run")
	if err != nil {
		t.Fatal(err)
	}
	report := Report{Task: Task{ID: "portable-report-paths"}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	report, _, staged, err := stageReport(report, outputDir)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.cleanup()
	for _, artifact := range append(report.Artifacts, stats) {
		if strings.Contains(artifact.Path, `\`) {
			t.Fatalf("artifact path is platform-dependent: %q", artifact.Path)
		}
	}
}

func TestExecutorInitializationFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	runner := &sandbox{executor: "container", initErr: context.DeadlineExceeded, outputDir: dir}
	runs, decisions, artifacts, err := runner.run(context.Background(), "task-init-failure", "", ParsedInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].ErrorType != "setup_error" {
		t.Fatalf("unexpected runs: %+v", runs)
	}
	if len(decisions) != 1 || len(artifacts) != 0 {
		t.Fatalf("failed setup must not publish a synthetic sandbox artifact: decisions=%+v artifacts=%+v", decisions, artifacts)
	}
}

func TestSandboxDiffStatsMustMatchParsedInput(t *testing.T) {
	fs := &stubFS{collectFiles: []codeexecutor.File{{Name: "out/diff_stats.json", Content: `{"files_changed":9,"added_lines":2,"deleted_lines":1}`}}}
	runner := &sandbox{engine: stubEngine{fs: fs}, outputLimit: 1024}
	err := runner.validateDiffStats(context.Background(), codeexecutor.Workspace{}, DiffSummary{FilesChanged: 1, AddedLines: 2, DeletedLines: 1})
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("mismatched Skill artifact was accepted: %v", err)
	}
}

func TestSandboxDiffStatsValidation(t *testing.T) {
	want := DiffSummary{FilesChanged: 1, AddedLines: 2, DeletedLines: 3}
	valid := `{"files_changed":1,"added_lines":2,"deleted_lines":3}`
	cases := []struct {
		name  string
		files []codeexecutor.File
		err   error
		ok    bool
	}{
		{name: "valid", files: []codeexecutor.File{{Name: "out/diff_stats.json", Content: valid}}, ok: true},
		{name: "collect error", err: errors.New("collect")},
		{name: "missing", files: nil},
		{name: "truncated", files: []codeexecutor.File{{Content: valid, Truncated: true}}},
		{name: "invalid json", files: []codeexecutor.File{{Content: `{`}}},
		{name: "unknown field", files: []codeexecutor.File{{Content: `{"files_changed":1,"added_lines":2,"deleted_lines":3,"extra":1}`}}},
		{name: "trailing json", files: []codeexecutor.File{{Content: valid + `{}`}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fs := &stubFS{collectFiles: test.files, collectErr: test.err}
			runner := &sandbox{engine: stubEngine{fs: fs}, outputLimit: 1024}
			err := runner.validateDiffStats(context.Background(), codeexecutor.Workspace{}, want)
			if (err == nil) != test.ok {
				t.Fatalf("validation error = %v, want success %t", err, test.ok)
			}
		})
	}
}

func TestRunUsesInjectedStoreFactory(t *testing.T) {
	dir := t.TempDir()
	called := false
	_, _, err := Run(context.Background(), Config{
		Fixture: "clean", DryRun: true, OutputDir: dir, DatabasePath: filepath.Join(dir, "reviews.sqlite"),
		StoreFactory: func(_ context.Context, cfg Config) (Store, error) {
			called = true
			return openStore(cfg.DatabasePath)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("configured Store adapter was not used")
	}
}

func TestTotalDurationIncludesTerminalFinalizationAndVerification(t *testing.T) {
	dir := t.TempDir()
	const delay = 25 * time.Millisecond
	report, paths, err := Run(context.Background(), Config{
		Fixture: "clean", DryRun: true, OutputDir: dir, DatabasePath: filepath.Join(dir, "reviews.sqlite"),
		StoreFactory: func(_ context.Context, cfg Config) (Store, error) {
			store, err := openStore(cfg.DatabasePath)
			if err != nil {
				return nil, err
			}
			return delayedStore{Store: store, delay: delay}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.PreparationDurationMS < delay.Milliseconds() {
		t.Fatalf("preparation duration %dms excluded persistence delay", report.Metrics.PreparationDurationMS)
	}
	if report.Metrics.TotalDurationMS < report.Metrics.PreparationDurationMS+2*delay.Milliseconds() {
		t.Fatalf("total duration %dms precedes preparation duration %dms", report.Metrics.TotalDurationMS, report.Metrics.PreparationDurationMS)
	}
	data, err := os.ReadFile(paths.JSON)
	if err != nil {
		t.Fatal(err)
	}
	var published Report
	if err := json.Unmarshal(data, &published); err != nil {
		t.Fatal(err)
	}
	if published.Metrics.TotalDurationMS <= 0 || published.Metrics.TotalDurationMS > report.Metrics.TotalDurationMS || published.Metrics.TotalDurationScope != "pre_publication_snapshot" {
		t.Fatalf("published total duration must be a positive pre-publication snapshot: published=%d final=%d", published.Metrics.TotalDurationMS, report.Metrics.TotalDurationMS)
	}
	if !published.Task.EndedAt.Before(report.Task.EndedAt) {
		t.Fatalf("canonical completion timestamp must follow the immutable report snapshot: published=%s canonical=%s", published.Task.EndedAt, report.Task.EndedAt)
	}
	store, err := openStore(filepath.Join(dir, "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	metrics, err := store.LoadMetrics(context.Background(), report.Task.ID)
	if err != nil || metrics.TotalDurationMS != report.Metrics.TotalDurationMS || metrics.TotalDurationScope != "verified_before_metric_persistence" || metrics.FinalizationDurationMS < delay.Milliseconds() || metrics.VerificationDurationMS < delay.Milliseconds() {
		t.Fatalf("persisted total duration mismatch: metrics=%+v err=%v", metrics, err)
	}
	task, err := store.LoadTask(context.Background(), report.Task.ID)
	if err != nil || !task.EndedAt.Equal(report.Task.EndedAt) {
		t.Fatalf("persisted completion timestamp mismatch: task=%+v err=%v", task, err)
	}
}

type delayedStore struct {
	Store
	delay time.Duration
}

func (s delayedStore) Save(ctx context.Context, report Report) error {
	time.Sleep(s.delay)
	return s.Store.Save(ctx, report)
}

func (s delayedStore) Finalize(ctx context.Context, report Report) error {
	time.Sleep(s.delay)
	return s.Store.Finalize(ctx, report)
}

func (s delayedStore) Load(ctx context.Context, taskID string) (Report, error) {
	time.Sleep(s.delay)
	return s.Store.Load(ctx, taskID)
}

type failOnceFinalizeStore struct {
	Store
	err    error
	failed bool
}

type requirePublishedStore struct {
	Store
	outputDir string
}

func (s requirePublishedStore) Finalize(ctx context.Context, report Report) error {
	if report.Task.Status == TaskCompleted {
		if _, err := os.Stat(filepath.Join(s.outputDir, report.Task.ID)); err != nil {
			return fmt.Errorf("report was not published before completion: %w", err)
		}
	}
	return s.Store.Finalize(ctx, report)
}

func (s *failOnceFinalizeStore) Finalize(ctx context.Context, report Report) error {
	if !s.failed {
		s.failed = true
		return s.err
	}
	return s.Store.Finalize(ctx, report)
}

func TestTaskIDValidation(t *testing.T) {
	for _, value := range []string{"../escape", "has space", strings.Repeat("a", 81), "sk-abcdefghijklmnopqrstuvwxyz"} {
		cfg := Config{TaskID: value}
		if err := normalizeConfig(&cfg); err == nil {
			t.Fatalf("task id %q was accepted", value)
		}
	}
}

func TestRunReportsConfigurationAndStoreInitializationFailures(t *testing.T) {
	if _, _, err := Run(context.Background(), Config{TaskID: "../invalid", Fixture: "clean", DryRun: true}); err == nil {
		t.Fatal("invalid task ID reached the review pipeline")
	}
	storeErr := errors.New("store unavailable")
	if _, _, err := Run(context.Background(), Config{
		TaskID: "store-failure", Fixture: "clean", DryRun: true,
		StoreFactory: func(context.Context, Config) (Store, error) { return nil, storeErr },
	}); !errors.Is(err, storeErr) {
		t.Fatalf("store initialization error was not returned: %v", err)
	}
}

func hasCategory(values []Finding, category string) bool {
	for _, value := range values {
		if value.Category == category {
			return true
		}
	}
	return false
}

func hasArtifactProvenance(artifacts []Artifact, name, provenance string) bool {
	for _, artifact := range artifacts {
		if artifact.Name == name && artifact.Provenance == provenance {
			return true
		}
	}
	return false
}
