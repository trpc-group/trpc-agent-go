//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestReviewInputModesAreMutuallyExclusive(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{
			name:       "missing review input",
			args:       []string{"--dry-run"},
			wantCode:   2,
			wantStderr: "exactly one",
		},
		{
			name:       "multiple review inputs",
			args:       []string{"--diff-file", "change.diff", "--fixture", "clean"},
			wantCode:   2,
			wantStderr: "exactly one",
		},
		{
			name:       "show task conflicts with fixture",
			args:       []string{"--show-task", "task-1", "--fixture", "clean"},
			wantCode:   2,
			wantStderr: "cannot be combined",
		},
		{
			name:       "show task conflicts with files",
			args:       []string{"--show-task", "task-1", "--files", "a.go"},
			wantCode:   2,
			wantStderr: "cannot be combined",
		},
		{
			name:       "files require repo path",
			args:       []string{"--files", "a.go"},
			wantCode:   2,
			wantStderr: "can only be used with --repo-path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runForTest(t, tt.args, nil, nil)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr: %s", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr %q does not contain %q", stderr, tt.wantStderr)
			}
		})
	}
}

func TestOutputDirectoryControlsDefaultDBPath(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantOutput string
		wantDB     string
	}{
		{
			name:       "defaults",
			args:       []string{"--fixture", "clean", "--dry-run"},
			wantOutput: defaultOutputDir,
			wantDB:     filepath.Join(defaultOutputDir, "reviews.db"),
		},
		{
			name:       "custom output moves default db",
			args:       []string{"--fixture", "clean", "--dry-run", "--output-dir", "custom"},
			wantOutput: "custom",
			wantDB:     filepath.Join("custom", "reviews.db"),
		},
		{
			name: "explicit db wins",
			args: []string{
				"--fixture", "clean", "--dry-run",
				"--output-dir", "custom", "--db-path", "state/reviews.db",
			},
			wantOutput: "custom",
			wantDB:     "state/reviews.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, code, err := parseConfig(tt.args, func(string) string { return "" })
			if err != nil || code != 0 {
				t.Fatalf("parseConfig() = code %d, error %v", code, err)
			}
			if cfg.outputDir != tt.wantOutput || cfg.dbPath != tt.wantDB {
				t.Fatalf("paths = output %q, db %q; want output %q, db %q",
					cfg.outputDir, cfg.dbPath, tt.wantOutput, tt.wantDB)
			}
		})
	}
}

func TestShowTaskQuery(t *testing.T) {
	t.Run("missing argument", func(t *testing.T) {
		code, _, stderr := runForTest(t, []string{"--show-task"}, nil, nil)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "flag needs an argument") {
			t.Fatalf("stderr %q does not report a missing argument", stderr)
		}
	})

	t.Run("empty task id", func(t *testing.T) {
		code, _, stderr := runForTest(t, []string{"--show-task="}, nil, nil)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "must not be empty") {
			t.Fatalf("stderr %q does not report an empty task id", stderr)
		}
	})

	t.Run("unknown task id returns structured error", func(t *testing.T) {
		code, stdout, stderr := runForTest(t, []string{"--show-task", "task-1"}, nil, nil)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr: %s", code, stderr)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
		var got taskQueryError
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("unmarshal stdout: %v\n%s", err, stdout)
		}
		if got.Error != errReviewTaskNotFound.Error() || got.TaskID != "task-1" {
			t.Fatalf("task query response = %+v", got)
		}
	})

	t.Run("empty sqlite store returns structured error", func(t *testing.T) {
		if !sqlDriverAvailable(sqliteDriverName) {
			t.Skip("sqlite3 driver is not registered")
		}
		dbPath := filepath.Join(t.TempDir(), "empty.db")
		code, stdout, stderr := runRawForTest(t, []string{
			"--show-task", "missing-task",
			"--db-path", dbPath,
		}, nil, nil, runtimeHooks{})
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr: %s", code, stderr)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
		var got taskQueryError
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("unmarshal stdout: %v\n%s", err, stdout)
		}
		if got.Error != errReviewTaskNotFound.Error() || got.TaskID != "missing-task" ||
			got.DBPath != dbPath {
			t.Fatalf("task query response = %+v", got)
		}
	})

	t.Run("valid task id returns stored report", func(t *testing.T) {
		store := newMemoryReviewStore()
		hooks := runtimeHooks{reviewStore: store, taskID: "task-1"}
		code, _, stderr := runForTestWithHooks(t, []string{"--fixture", "clean", "--dry-run"}, nil, nil, hooks)
		if code != 0 {
			t.Fatalf("review exit code = %d, want 0; stderr: %s", code, stderr)
		}

		code, stdout, stderr := runForTestWithHooks(t, []string{"--show-task", "task-1"}, nil, nil, runtimeHooks{
			reviewStore: store,
		})
		if code != 0 {
			t.Fatalf("show-task exit code = %d, want 0; stderr: %s", code, stderr)
		}
		var got reviewReport
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("unmarshal report: %v\n%s", err, stdout)
		}
		if got.TaskID != "task-1" || got.Status != reviewStatusCompleted ||
			got.Conclusion != reviewConclusionPass {
			t.Fatalf("report = %+v", got)
		}
	})
}

func TestReportFilesAndSummaryBoundary(t *testing.T) {
	code, stdout, stderr := runForTest(t, []string{"--fixture", "secret_leak", "--dry-run"}, nil, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	for _, unexpected := range []string{"filter_decisions", "permission_decisions", "sandbox_runs", "evidence"} {
		if strings.Contains(stdout, unexpected) {
			t.Fatalf("stdout summary contains detailed field %q: %s", unexpected, stdout)
		}
	}
	var summary reviewSummary
	mustUnmarshalSummary(t, stdout, &summary)
	if summary.TaskID == "" || summary.Status != reviewStatusCompleted ||
		summary.Conclusion != reviewConclusionFindings ||
		summary.ReportPaths.JSON == "" || summary.ReportPaths.Markdown == "" ||
		summary.DurationMS < 0 {
		t.Fatalf("summary = %+v", summary)
	}

	report := readReportFromSummary(t, summary)
	if len(report.Findings) != 1 || len(report.Governance.FilterDecisions) == 0 ||
		len(report.Governance.SandboxRuns) == 0 {
		t.Fatalf("full report missing detailed fields: %+v", report)
	}
	markdown, err := os.ReadFile(filepath.FromSlash(summary.ReportPaths.Markdown))
	if err != nil {
		t.Fatalf("read markdown report: %v", err)
	}
	for _, content := range []string{stdout, string(markdown)} {
		for _, leaked := range []string{"ghp_", "serviceToken", "diff --git"} {
			if strings.Contains(content, leaked) {
				t.Fatalf("report output leaked %q: %s", leaked, content)
			}
		}
	}
}

func TestSampleReviewReports(t *testing.T) {
	jsonBytes, err := os.ReadFile(filepath.Join("testdata", "review_report.json"))
	if err != nil {
		t.Fatalf("read sample json report: %v", err)
	}
	markdownBytes, err := os.ReadFile(filepath.Join("testdata", "review_report.md"))
	if err != nil {
		t.Fatalf("read sample markdown report: %v", err)
	}

	var report reviewReport
	if err := json.Unmarshal(jsonBytes, &report); err != nil {
		t.Fatalf("unmarshal sample report: %v\n%s", err, jsonBytes)
	}
	if report.TaskID != "review-sample-secret-leak" ||
		report.Input.Kind != inputKindFixture ||
		report.Input.Source != "secret_leak" ||
		report.Runtime.Runtime != runtimeFake ||
		len(report.Findings) != 1 ||
		len(report.Governance.SandboxRuns) != 3 ||
		report.Metrics.ToolCalls != 3 {
		t.Fatalf("sample report = %+v", report)
	}

	markdown := string(markdownBytes)
	for _, want := range []string{
		"review-sample-secret-leak",
		"## Findings",
		"## Governance",
		"## Sandbox",
		"## Metrics",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("sample markdown missing %q:\n%s", want, markdown)
		}
	}
	for _, leaked := range []string{"ghp_", "serviceToken", "diff --git"} {
		if strings.Contains(string(jsonBytes), leaked) {
			t.Fatalf("sample json leaked %q", leaked)
		}
		if strings.Contains(markdown, leaked) {
			t.Fatalf("sample markdown leaked %q", leaked)
		}
	}
}

func TestSQLiteReviewStoreSaveLoadAndSchema(t *testing.T) {
	requireSQLiteDriver(t)

	dbPath := filepath.Join(t.TempDir(), "reviews.db")
	ctx := context.Background()
	store, err := openSQLiteReviewStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	report := sampleReviewReport("task-1")
	if err := store.SaveReview(ctx, report); err != nil {
		t.Fatalf("save review: %v", err)
	}
	loaded, err := store.LoadReview(ctx, "task-1")
	if err != nil {
		t.Fatalf("load review: %v", err)
	}
	if loaded.TaskID != report.TaskID || loaded.Input.DiffSHA256 != report.Input.DiffSHA256 ||
		len(loaded.Governance.FilterDecisions) != 1 || len(loaded.Governance.SandboxRuns) != 1 ||
		len(loaded.Findings) != 1 || len(loaded.Artifacts) != 2 {
		t.Fatalf("loaded report = %+v", loaded)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openSQLiteReviewStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteReviewStoreRollsBackOnChildInsertFailure(t *testing.T) {
	requireSQLiteDriver(t)

	ctx := context.Background()
	store, err := openSQLiteReviewStore(ctx, filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	report := sampleReviewReport("task-rollback")
	report.Artifacts = append(report.Artifacts, reportArtifact{
		Kind:  "unsupported_artifact",
		Path:  "bad",
		Bytes: 1,
	})
	if err := store.SaveReview(ctx, report); err == nil {
		t.Fatalf("save review succeeded, want artifact constraint failure")
	}
	if _, err := store.LoadReview(ctx, "task-rollback"); !errors.Is(err, errReviewTaskNotFound) {
		t.Fatalf("load after rollback = %v, want not found", err)
	}
}

func TestSQLiteUnsupportedSchemaVersion(t *testing.T) {
	requireSQLiteDriver(t)

	dbPath := filepath.Join(t.TempDir(), "reviews.db")
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = openSQLiteReviewStore(context.Background(), dbPath)
	if err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("open sqlite store err = %v, want unsupported version", err)
	}
}

func TestShowTaskRebuildsReportFromSQLite(t *testing.T) {
	requireSQLiteDriver(t)

	dbPath := filepath.Join(t.TempDir(), "reviews.db")
	outputDir := filepath.Join(t.TempDir(), "output")
	code, stdout, stderr := runRawForTest(t, []string{
		"--fixture", "clean",
		"--dry-run",
		"--db-path", dbPath,
		"--output-dir", outputDir,
	}, nil, nil, runtimeHooks{taskID: "task-sqlite"})
	if code != 0 {
		t.Fatalf("review exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var summary reviewSummary
	mustUnmarshalSummary(t, stdout, &summary)

	if err := os.Remove(filepath.FromSlash(summary.ReportPaths.JSON)); err != nil {
		t.Fatalf("remove json report: %v", err)
	}
	code, stdout, stderr = runRawForTest(t, []string{
		"--show-task", "task-sqlite",
		"--db-path", dbPath,
	}, nil, nil, runtimeHooks{})
	if code != 0 {
		t.Fatalf("show-task exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var report reviewReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("unmarshal report: %v\n%s", err, stdout)
	}
	if report.TaskID != "task-sqlite" || report.Input.DiffSHA256 != summary.DiffSHA256 ||
		len(report.Artifacts) != 2 {
		t.Fatalf("rebuilt report = %+v", report)
	}
}

func TestSQLiteAndShowTaskDoNotPersistRawSecret(t *testing.T) {
	requireSQLiteDriver(t)

	dbPath := filepath.Join(t.TempDir(), "reviews.db")
	outputDir := filepath.Join(t.TempDir(), "output")
	code, stdout, stderr := runRawForTest(t, []string{
		"--fixture", "secret_leak",
		"--dry-run",
		"--db-path", dbPath,
		"--output-dir", outputDir,
	}, nil, nil, runtimeHooks{taskID: "task-secret"})
	if code != 0 {
		t.Fatalf("review exit code = %d, want 0; stderr: %s", code, stderr)
	}
	code, queryOut, stderr := runRawForTest(t, []string{
		"--show-task", "task-secret",
		"--db-path", dbPath,
	}, nil, nil, runtimeHooks{})
	if code != 0 {
		t.Fatalf("show-task exit code = %d, want 0; stderr: %s", code, stderr)
	}
	dbBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{stdout, queryOut, string(dbBytes)} {
		for _, leaked := range []string{"ghp_", "serviceToken", "diff --git"} {
			if strings.Contains(content, leaked) {
				t.Fatalf("stored output leaked %q", leaked)
			}
		}
	}
}

func TestFilesAreNormalizedAndPassedToGit(t *testing.T) {
	var gotRepo string
	var gotArgs []string
	runner := func(_ context.Context, repoPath string, args []string) ([]byte, []byte, error) {
		gotRepo = repoPath
		gotArgs = append([]string(nil), args...)
		return []byte(minimalDiff()), nil, nil
	}

	code, stdout, stderr := runForTest(t, []string{
		"--repo-path", "repo",
		"--files", "a.go,b.go",
		"--files", `pkg\c.go`,
		"--runtime", "fake",
	}, nil, runner)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if gotRepo != "repo" {
		t.Fatalf("repo path = %q, want repo", gotRepo)
	}
	wantArgs := []string{"diff", "HEAD", "--", "a.go", "b.go", "pkg/c.go"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("git args = %#v, want %#v", gotArgs, wantArgs)
	}
	var got reviewSummary
	mustUnmarshalSummary(t, stdout, &got)
	if got.InputKind != inputKindRepoPath || got.Runtime != runtimeFake {
		t.Fatalf("summary = %+v", got)
	}
}

func TestFileFilterValidation(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		wantStderr string
	}{
		{name: "parent escape", file: "../a.go", wantStderr: "escapes the repository"},
		{name: "drive path", file: "C:/repo/a.go", wantStderr: "must be relative"},
		{name: "absolute path", file: "/repo/a.go", wantStderr: "must be relative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runForTest(t, []string{
				"--repo-path", "repo",
				"--files", tt.file,
				"--runtime", "fake",
			}, nil, nil)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; stderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr %q does not contain %q", stderr, tt.wantStderr)
			}
		})
	}
}

func TestRuntimeValidation(t *testing.T) {
	t.Run("container is rejected", func(t *testing.T) {
		code, _, stderr := runForTest(t, []string{"--fixture", "clean", "--runtime", "container"}, nil, nil)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "runtime must be one of e2b, fake, local") {
			t.Fatalf("stderr %q does not list allowed runtimes", stderr)
		}
	})

	t.Run("local requires allow local", func(t *testing.T) {
		code, _, stderr := runForTest(t, []string{"--fixture", "clean", "--runtime", "local"}, nil, nil)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "requires --allow-local") {
			t.Fatalf("stderr %q does not explain local gating", stderr)
		}
	})

	t.Run("local is accepted with allow local", func(t *testing.T) {
		code, stdout, stderr := runForTest(t, []string{
			"--fixture", "clean",
			"--runtime", "local",
			"--allow-local",
		}, nil, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
		var got reviewSummary
		mustUnmarshalSummary(t, stdout, &got)
		if got.Runtime != runtimeLocal {
			t.Fatalf("runtime = %q, want %q", got.Runtime, runtimeLocal)
		}
	})

	t.Run("dry run forces fake runtime", func(t *testing.T) {
		code, stdout, stderr := runForTest(t, []string{
			"--fixture", "clean",
			"--runtime", "e2b",
			"--dry-run",
		}, nil, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
		var got reviewSummary
		mustUnmarshalSummary(t, stdout, &got)
		if got.Runtime != runtimeFake || !got.DryRun {
			t.Fatalf("summary = %+v", got)
		}
	})
}

func TestE2BTemplatePrecedence(t *testing.T) {
	t.Run("env is used when flag is empty", func(t *testing.T) {
		code, stdout, stderr := runForTest(t, []string{"--fixture", "clean", "--dry-run"}, map[string]string{
			envE2BTemplate: "env-template",
		}, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
		var got reviewSummary
		mustUnmarshalSummary(t, stdout, &got)
		if got.E2BTemplate != "env-template" {
			t.Fatalf("e2b template = %q, want env-template", got.E2BTemplate)
		}
	})

	t.Run("flag wins over env", func(t *testing.T) {
		code, stdout, stderr := runForTest(t, []string{
			"--fixture", "clean",
			"--dry-run",
			"--e2b-template", "flag-template",
		}, map[string]string{envE2BTemplate: "env-template"}, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
		var got reviewSummary
		mustUnmarshalSummary(t, stdout, &got)
		if got.E2BTemplate != "flag-template" {
			t.Fatalf("e2b template = %q, want flag-template", got.E2BTemplate)
		}
	})

	t.Run("empty when no source is configured", func(t *testing.T) {
		code, stdout, stderr := runForTest(t, []string{"--fixture", "clean", "--dry-run"}, nil, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
		var got reviewSummary
		mustUnmarshalSummary(t, stdout, &got)
		if got.E2BTemplate != "" {
			t.Fatalf("e2b template = %q, want empty", got.E2BTemplate)
		}
	})
}

func TestE2BMissingAPIKeyDoesNotAbortReview(t *testing.T) {
	t.Setenv("E2B_API_KEY", "")

	code, stdout, stderr := runForTest(t, []string{
		"--fixture", "clean",
		"--runtime", "e2b",
	}, nil, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var got reviewSummary
	mustUnmarshalSummary(t, stdout, &got)
	if got.Runtime != runtimeE2B || got.CommandsAllowed != got.CommandsPlanned {
		t.Fatalf("summary = %+v", got)
	}
	report := readReportFromSummary(t, got)
	if len(report.Governance.SandboxRuns) != got.CommandsAllowed {
		t.Fatalf("sandbox runs = %d, allowed = %d", len(report.Governance.SandboxRuns), got.CommandsAllowed)
	}
	for _, run := range report.Governance.SandboxRuns {
		if run.Runtime != runtimeE2B || !run.Skipped {
			t.Fatalf("sandbox run = %+v, want skipped e2b run", run)
		}
	}
	if !containsString(got.WarningRuleIDs, ruleSandboxPreflightFailed) ||
		!containsString(got.WarningRuleIDs, ruleSandboxRunSkipped) {
		t.Fatalf("warning rule ids = %#v", got.WarningRuleIDs)
	}
}

func TestLocalPreflightMissingToolsDoesNotAbortReview(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	code, stdout, stderr := runForTest(t, []string{
		"--fixture", "clean",
		"--runtime", "local",
		"--allow-local",
	}, nil, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var got reviewSummary
	mustUnmarshalSummary(t, stdout, &got)
	report := readReportFromSummary(t, got)
	if got.Runtime != runtimeLocal || len(report.Governance.SandboxRuns) == 0 {
		t.Fatalf("summary = %+v", got)
	}
	for _, run := range report.Governance.SandboxRuns {
		if run.Runtime != runtimeLocal || !run.Skipped ||
			!strings.Contains(run.Error, "local runtime missing required tools") {
			t.Fatalf("sandbox run = %+v, want skipped local preflight run", run)
		}
	}
}

func TestSkillRunBridgeArgumentsUseGovernedSpec(t *testing.T) {
	repoRoot := t.TempDir()
	input := reviewInput{
		kind:     inputKindRepoPath,
		repoRoot: repoRoot,
	}
	spec := newCommandSpec(
		commandCheckGoTest,
		commandInputs(input),
		commandEnv(input),
	)
	if decision := gateCommand(spec); decision.Decision != governanceDecisionAllow {
		t.Fatalf("gate decision = %+v, want allow", decision)
	}

	args, err := permissionArguments(codeReviewSkillName, spec)
	if err != nil {
		t.Fatal(err)
	}
	var got skillRunPermissionArguments
	if err := json.Unmarshal(args, &got); err != nil {
		t.Fatal(err)
	}
	if got.Skill != codeReviewSkillName ||
		got.Command != "bash scripts/run_checks.sh test" ||
		got.Timeout != defaultCommandTimeoutSeconds {
		t.Fatalf("skill_run args = %+v", got)
	}
	if got.Env["REVIEW_REPO_DIR"] != "../../work/repo" {
		t.Fatalf("REVIEW_REPO_DIR = %q", got.Env["REVIEW_REPO_DIR"])
	}
	if len(got.Inputs) != 1 || got.Inputs[0].To != "work/repo" ||
		got.Inputs[0].Mode != "copy" ||
		!strings.HasPrefix(got.Inputs[0].From, "host://") {
		t.Fatalf("inputs = %+v", got.Inputs)
	}
}

func TestModeFlagsAreIncludedInSummary(t *testing.T) {
	code, stdout, stderr := runForTest(t, []string{
		"--fixture", "clean",
		"--dry-run",
		"--rule-only",
		"--enable-staticcheck",
	}, nil, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var got reviewSummary
	mustUnmarshalSummary(t, stdout, &got)
	if !got.RuleOnly {
		t.Fatalf("rule_only = false, want true")
	}
	if !got.EnableStaticcheck {
		t.Fatalf("enable_staticcheck = false, want true")
	}
}

func TestGovernanceSummaryFromDryRun(t *testing.T) {
	code, stdout, stderr := runForTest(t, []string{"--fixture", "clean", "--dry-run"}, nil, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var got reviewSummary
	mustUnmarshalSummary(t, stdout, &got)
	if got.CommandsPlanned != 3 || got.CommandsAllowed != 3 ||
		got.CommandsBlocked != 0 || got.PermissionBlocks != 0 {
		t.Fatalf("governance counts = planned:%d allowed:%d blocked:%d permission:%d",
			got.CommandsPlanned, got.CommandsAllowed, got.CommandsBlocked, got.PermissionBlocks)
	}
	report := readReportFromSummary(t, got)
	if report.Governance.SkillName != codeReviewSkillName || report.Governance.SkillDigest == "" {
		t.Fatalf("skill metadata = %q/%q, want code-review digest",
			report.Governance.SkillName, report.Governance.SkillDigest)
	}
	if len(report.Governance.FilterDecisions) != 3 || len(report.Governance.PermissionDecisions) != 3 ||
		len(report.Governance.SandboxRuns) != 3 {
		t.Fatalf("governance slices = filter:%d permission:%d runs:%d",
			len(report.Governance.FilterDecisions), len(report.Governance.PermissionDecisions),
			len(report.Governance.SandboxRuns))
	}
	for _, decision := range append(report.Governance.FilterDecisions, report.Governance.PermissionDecisions...) {
		if decision.Decision != governanceDecisionAllow {
			t.Fatalf("decision = %+v, want allow", decision)
		}
	}
}

func TestGovernanceStaticcheckIsOptIn(t *testing.T) {
	code, stdout, stderr := runForTest(t, []string{
		"--fixture", "clean",
		"--dry-run",
		"--enable-staticcheck",
	}, nil, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var got reviewSummary
	mustUnmarshalSummary(t, stdout, &got)
	report := readReportFromSummary(t, got)
	if got.CommandsPlanned != 4 || got.CommandsAllowed != 4 || len(report.Governance.SandboxRuns) != 4 {
		t.Fatalf("staticcheck governance summary = %+v", got)
	}
	if report.Governance.SandboxRuns[3].Command != string(commandCheckStaticcheck) {
		t.Fatalf("last sandbox command = %+v, want staticcheck", report.Governance.SandboxRuns[3])
	}
}

func TestSkillLoadFailureIsFatal(t *testing.T) {
	code, stdout, stderr := runForTestWithHooks(t, []string{"--fixture", "clean", "--dry-run"}, nil, nil, runtimeHooks{
		skillRoot: t.TempDir(),
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "load code-review skill") {
		t.Fatalf("stderr %q does not report skill load failure", stderr)
	}
}

func TestLoadCodeReviewSkillDigestAndRequiredFiles(t *testing.T) {
	loaded, err := loadCodeReviewSkill("")
	if err != nil {
		t.Fatalf("load skill: %v", err)
	}
	if loaded.Name != codeReviewSkillName || loaded.Digest == "" {
		t.Fatalf("loaded skill = %+v", loaded)
	}

	root := t.TempDir()
	dir := filepath.Join(root, codeReviewSkillName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: code-review\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCodeReviewSkill(root); err == nil {
		t.Fatalf("loadCodeReviewSkill succeeded with missing required files")
	}
}

func TestCommandGateValidation(t *testing.T) {
	absRepo := filepath.ToSlash(t.TempDir())
	validInput := []inputMapping{{From: "host://" + absRepo, To: "work/repo", Mode: "copy"}}

	tests := []struct {
		name string
		spec commandSpec
		want string
	}{
		{
			name: "valid repo command",
			spec: newCommandSpec(commandCheckGoTest, validInput, map[string]string{
				"REVIEW_REPO_DIR": "../../work/repo",
			}),
			want: governanceDecisionAllow,
		},
		{
			name: "unknown command",
			spec: commandSpec{Kind: commandKind("unknown")},
			want: governanceDecisionDeny,
		},
		{
			name: "extra arg",
			spec: func() commandSpec {
				spec := newCommandSpec(commandCheckGoVet, nil, nil)
				spec.Args = append(spec.Args, "--all")
				return spec
			}(),
			want: governanceDecisionDeny,
		},
		{
			name: "changed executable",
			spec: func() commandSpec {
				spec := newCommandSpec(commandCheckGoVersion, nil, nil)
				spec.Executable = "sh"
				return spec
			}(),
			want: governanceDecisionDeny,
		},
		{
			name: "cwd escape",
			spec: func() commandSpec {
				spec := newCommandSpec(commandCheckGoTest, nil, nil)
				spec.Cwd = "../repo"
				return spec
			}(),
			want: governanceDecisionDeny,
		},
		{
			name: "env injection",
			spec: func() commandSpec {
				spec := newCommandSpec(commandCheckGoTest, nil, map[string]string{"LD_PRELOAD": "x"})
				return spec
			}(),
			want: governanceDecisionDeny,
		},
		{
			name: "invalid input mapping",
			spec: newCommandSpec(commandCheckGoTest, []inputMapping{{
				From: "workspace://repo",
				To:   "work/repo",
			}}, nil),
			want: governanceDecisionDeny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gateCommand(tt.spec)
			if got.Decision != tt.want {
				t.Fatalf("gate decision = %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestGovernancePermissionBlocksSkipRunner(t *testing.T) {
	tests := []struct {
		name   string
		policy tool.PermissionPolicy
		want   string
	}{
		{
			name: "deny",
			policy: tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
				return tool.DenyPermission(`password = "supersecret123"`), nil
			}),
			want: governanceDecisionDeny,
		},
		{
			name: "ask",
			policy: tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
				return tool.AskPermission("approval required"), nil
			}),
			want: governanceDecisionAsk,
		},
		{
			name: "error",
			policy: tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
				return tool.PermissionDecision{}, errors.New(`policy failed with token = "secretvalue123"`)
			}),
			want: governanceDecisionError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingSandboxRunner{}
			gov, err := runGovernance(context.Background(), config{}, reviewInput{
				kind: inputKindFixture,
			}, parseUnifiedDiff([]byte(minimalDiff())), runtimeHooks{
				permissionPolicy: tt.policy,
				sandboxRunner:    runner,
			})
			if err != nil {
				t.Fatalf("runGovernance: %v", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %d, want 0", len(runner.calls))
			}
			if gov.PermissionBlocks != gov.CommandsPlanned || len(gov.Warnings) != gov.CommandsPlanned {
				t.Fatalf("governance result = %+v", gov)
			}
			for _, decision := range gov.PermissionDecisions {
				if decision.Decision != tt.want {
					t.Fatalf("permission decision = %+v, want %s", decision, tt.want)
				}
			}
			encoded, err := json.Marshal(gov)
			if err != nil {
				t.Fatal(err)
			}
			for _, leaked := range []string{"supersecret123", "secretvalue123"} {
				if strings.Contains(string(encoded), leaked) {
					t.Fatalf("governance output leaked %q: %s", leaked, encoded)
				}
			}
		})
	}
}

func TestGovernanceAllowCallsRunner(t *testing.T) {
	runner := &recordingSandboxRunner{}
	gov, err := runGovernance(context.Background(), config{}, reviewInput{
		kind: inputKindFixture,
	}, parseUnifiedDiff([]byte(minimalDiff())), runtimeHooks{
		sandboxRunner: runner,
	})
	if err != nil {
		t.Fatalf("runGovernance: %v", err)
	}
	if len(runner.calls) != gov.CommandsPlanned || gov.CommandsAllowed != gov.CommandsPlanned {
		t.Fatalf("runner calls = %d, governance = %+v", len(runner.calls), gov)
	}
	if len(gov.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", gov.Warnings)
	}
}

func TestDiffFileAndFixtureInputSummaries(t *testing.T) {
	t.Run("diff file summary", func(t *testing.T) {
		diff := minimalDiff()
		diffFile := filepath.Join(t.TempDir(), "change.diff")
		if err := os.WriteFile(diffFile, []byte(diff), 0o600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runForTest(t, []string{
			"--diff-file", diffFile,
			"--runtime", "fake",
		}, nil, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}

		var got reviewSummary
		mustUnmarshalSummary(t, stdout, &got)
		wantHash := sha256.Sum256([]byte(diff))
		if got.InputKind != inputKindDiffFile || got.Source != diffFile ||
			got.DiffBytes != len(diff) || got.DiffSHA256 != hex.EncodeToString(wantHash[:]) {
			t.Fatalf("summary = %+v", got)
		}
		if strings.Contains(stdout, "fmt.Println") {
			t.Fatalf("stdout leaked raw diff: %s", stdout)
		}
	})

	t.Run("fixture summary", func(t *testing.T) {
		code, stdout, stderr := runForTest(t, []string{"--fixture", "clean", "--dry-run"}, nil, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
		var got reviewSummary
		mustUnmarshalSummary(t, stdout, &got)
		if got.InputKind != inputKindFixture || got.Source != "clean" ||
			got.DiffBytes == 0 || got.DiffSHA256 == "" || got.Runtime != runtimeFake {
			t.Fatalf("summary = %+v", got)
		}
		if got.ChangedFiles != 1 || got.Hunks != 1 || got.CandidateLines != 1 ||
			got.ParseWarnings != 0 || got.RuleMatches != 0 || got.RuleWarnings != 0 {
			t.Fatalf("phase 2 summary counts = %+v", got)
		}
	})

	t.Run("missing fixture", func(t *testing.T) {
		code, _, stderr := runForTest(t, []string{"--fixture", "missing"}, nil, nil)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr, `fixture "missing" not found`) {
			t.Fatalf("stderr %q does not report missing fixture", stderr)
		}
	})
}

func TestRepoPathUsesGitDiffArgumentArray(t *testing.T) {
	var gotArgs []string
	runner := func(_ context.Context, repoPath string, args []string) ([]byte, []byte, error) {
		if repoPath != "repo" {
			t.Fatalf("repo path = %q, want repo", repoPath)
		}
		gotArgs = append([]string(nil), args...)
		return []byte(minimalDiff()), nil, nil
	}

	code, _, stderr := runForTest(t, []string{
		"--repo-path", "repo",
		"--runtime", "fake",
	}, nil, runner)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	wantArgs := []string{"diff", "HEAD", "--"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("git args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestCLIProblemFixtureAddsCountsWithoutLeakingDiff(t *testing.T) {
	code, stdout, stderr := runForTest(t, []string{"--fixture", "secret_leak", "--dry-run"}, nil, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var got reviewSummary
	mustUnmarshalSummary(t, stdout, &got)
	if got.RuleMatches == 0 || got.RuleWarnings != 0 || got.ChangedFiles != 1 {
		t.Fatalf("summary counts = %+v", got)
	}
	for _, leaked := range []string{"ghp_", "serviceToken", "diff --git"} {
		if strings.Contains(stdout, leaked) {
			t.Fatalf("stdout leaked %q in summary: %s", leaked, stdout)
		}
	}
}

func TestParseUnifiedDiffLineNumbersAndNoNewlineMarker(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/pkg/foo.go b/pkg/foo.go",
		"index 1111111..2222222 100644",
		"--- a/pkg/foo.go",
		"+++ b/pkg/foo.go",
		"@@ -1,3 +1,4 @@",
		" package pkg",
		"-func oldName() {}",
		"+func newName() {}",
		"+// keep this comment as an added business line",
		`\ No newline at end of file`,
	}, "\n")

	parsed := parseUnifiedDiff([]byte(diff))
	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", parsed.Warnings)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].PackageName != "pkg" {
		t.Fatalf("parsed files = %+v", parsed.Files)
	}
	hunk := parsed.Files[0].Hunks[0]
	if hunk.Lines[0].Kind != diffLineContext || hunk.Lines[0].OldLine != 1 || hunk.Lines[0].NewLine != 1 {
		t.Fatalf("context line = %+v", hunk.Lines[0])
	}
	if hunk.Lines[1].Kind != diffLineDeleted || hunk.Lines[1].OldLine != 2 || hunk.Lines[1].NewLine != 0 {
		t.Fatalf("deleted line = %+v", hunk.Lines[1])
	}
	if hunk.Lines[2].Kind != diffLineAdded || hunk.Lines[2].OldLine != 0 || hunk.Lines[2].NewLine != 2 {
		t.Fatalf("added line = %+v", hunk.Lines[2])
	}
	if !hunk.Lines[3].NoNewlineAtEOF {
		t.Fatalf("last added line did not record no-newline marker: %+v", hunk.Lines[3])
	}

	candidates := parsed.candidateLines()
	if len(candidates) != 2 || candidates[0].Line != 2 || candidates[1].Line != 3 {
		t.Fatalf("candidate lines = %+v", candidates)
	}
}

func TestParseUnifiedDiffFileMetadata(t *testing.T) {
	diff, err := readFixture("rename_and_binary")
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseUnifiedDiff(diff)
	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", parsed.Warnings)
	}
	if len(parsed.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(parsed.Files))
	}
	if !parsed.Files[0].IsRename || parsed.Files[0].OldPath != "old_name.go" ||
		parsed.Files[0].NewPath != "new_name.go" || parsed.Files[0].PackageName != "newname" {
		t.Fatalf("rename file = %+v", parsed.Files[0])
	}
	if !parsed.Files[1].IsBinary {
		t.Fatalf("binary file = %+v", parsed.Files[1])
	}
	if !parsed.Files[2].IsDeleted {
		t.Fatalf("deleted file = %+v", parsed.Files[2])
	}
	if got := len(parsed.candidateLines()); got != 1 {
		t.Fatalf("candidate line count = %d, want 1", got)
	}
}

func TestParseUnifiedDiffQuotedPaths(t *testing.T) {
	diff := strings.Join([]string{
		`diff --git "a/pkg/foo bar.go" "b/pkg/foo bar.go"`,
		`--- "a/pkg/foo bar.go"`,
		`+++ "b/pkg/foo bar.go"`,
		"@@ -1 +1,2 @@",
		" package quoted",
		"+func Added() {}",
	}, "\n")
	parsed := parseUnifiedDiff([]byte(diff))
	if len(parsed.Warnings) != 0 || len(parsed.Files) != 1 {
		t.Fatalf("parsed diff = %+v", parsed)
	}
	file := parsed.Files[0]
	if file.OldPath != "pkg/foo bar.go" || file.NewPath != "pkg/foo bar.go" ||
		!file.isGoFile() || file.PackageName != "quoted" {
		t.Fatalf("quoted file metadata = %+v", file)
	}

	escaped := parseUnifiedDiff([]byte(strings.Join([]string{
		`diff --git "a/pkg/\344\270\255.go" "b/pkg/\344\270\255.go"`,
		`--- "a/pkg/\344\270\255.go"`,
		`+++ "b/pkg/\344\270\255.go"`,
		"@@ -1 +1,2 @@",
		" package unicodepath",
		"+func Added() {}",
	}, "\n")))
	if len(escaped.Files) != 1 || escaped.Files[0].NewPath != "pkg/中.go" {
		t.Fatalf("escaped path = %+v", escaped)
	}
}

func TestParseUnifiedDiffMalformedHunkWarning(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/bad.go b/bad.go",
		"--- a/bad.go",
		"+++ b/bad.go",
		"@@ broken",
		"+func bad() {}",
	}, "\n")
	parsed := parseUnifiedDiff([]byte(diff))
	if len(parsed.Warnings) == 0 {
		t.Fatalf("warnings = none, want malformed hunk warning")
	}
}

func TestRulesFromFixtures(t *testing.T) {
	tests := []struct {
		fixture      string
		wantFindings []string
		wantWarnings []string
	}{
		{fixture: "clean"},
		{fixture: "command_injection", wantFindings: []string{ruleShellCommandInjection}},
		{fixture: "secret_leak", wantFindings: []string{ruleSecretHardcoded}},
		{fixture: "insecure_tls", wantFindings: []string{ruleInsecureTLS}},
		{fixture: "goroutine_context_leak", wantFindings: []string{ruleGoroutineContextLeak}},
		{fixture: "resource_leak", wantFindings: []string{ruleUnclosedFile, ruleUnclosedHTTPBody, ruleUnclosedSQLRows}},
		{fixture: "ignored_error", wantFindings: []string{ruleIgnoredReturn}},
		{fixture: "database_lifecycle", wantFindings: []string{ruleDatabaseOpenLifecycle, ruleDatabaseTxLifecycle}},
		{fixture: "missing_tests", wantWarnings: []string{ruleMissingTests}},
		{fixture: "negative_controls"},
		{fixture: "sandbox_failure"},
	}

	covered := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			diff, err := readFixture(tt.fixture)
			if err != nil {
				t.Fatal(err)
			}
			matches := runRules(parseUnifiedDiff(diff), "")
			findings, warnings := splitRuleIDs(matches)
			assertRuleIDs(t, findings, tt.wantFindings)
			assertRuleIDs(t, warnings, tt.wantWarnings)
			for id := range findings {
				covered[id] = true
			}
			for id := range warnings {
				covered[id] = true
			}
			for _, match := range matches {
				if match.Source != "diff" || match.Severity == "" || match.Confidence <= 0 {
					t.Fatalf("invalid rule match metadata: %+v", match)
				}
			}
		})
	}

	for _, id := range []string{
		ruleSecretHardcoded,
		ruleShellCommandInjection,
		ruleInsecureTLS,
		ruleGoroutineContextLeak,
		ruleUnclosedFile,
		ruleUnclosedHTTPBody,
		ruleUnclosedSQLRows,
		ruleIgnoredReturn,
		ruleDatabaseTxLifecycle,
		ruleDatabaseOpenLifecycle,
		ruleMissingTests,
	} {
		if !covered[id] {
			t.Fatalf("rule %s was not covered by fixtures", id)
		}
	}
}

func TestSecurityRuleBoundaryCases(t *testing.T) {
	for _, safe := range []string{
		`cmd := exec.Command("sh", "-c", "echo ok")`,
		`cmd := exec.CommandContext(ctx, "bash", "-c", ` + "`printf ok`" + `)`,
		`func run() error { return exec.Command("sh", "-c", "echo a+b").Run() }`,
	} {
		if isShellCommandInjection(safe) {
			t.Fatalf("fixed shell command was reported as injection: %s", safe)
		}
	}
	for _, unsafe := range []string{
		`cmd := exec.Command("sh", "-c", command)`,
		`cmd := exec.Command("bash", "-c", "echo "+userInput)`,
		`cmd := exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("echo %s", userInput))`,
	} {
		if !isShellCommandInjection(unsafe) {
			t.Fatalf("interpolated shell command was not reported: %s", unsafe)
		}
	}

	for _, line := range []string{
		`TLSClientConfig: &tls.Config{InsecureSkipVerify:true}`,
		`cfg.InsecureSkipVerify = true`,
	} {
		if !insecureTLSPattern.MatchString(line) {
			t.Fatalf("insecure TLS form was not detected: %s", line)
		}
	}
	for _, line := range []string{
		`const modelCredential = "sk-test_only_not_a_real_token_123456"`,
		`header := "Bearer abcdefghijklmnopqrstuvwxyz"`,
	} {
		if !isHardcodedSecret(line) {
			t.Fatalf("provider credential was not detected: %s", line)
		}
	}
}

func TestMissingTestsAreMatchedByDirectory(t *testing.T) {
	unrelatedTest := strings.Join([]string{
		"diff --git a/pkg/api.go b/pkg/api.go",
		"--- a/pkg/api.go",
		"+++ b/pkg/api.go",
		"@@ -1 +1,2 @@",
		" package api",
		"+func Exported() {}",
		"diff --git a/other/helper_test.go b/other/helper_test.go",
		"--- a/other/helper_test.go",
		"+++ b/other/helper_test.go",
		"@@ -1 +1,2 @@",
		" package other",
		"+func TestHelper(t *testing.T) {}",
	}, "\n")
	finalized := finalizeRuleMatches(runRules(parseUnifiedDiff([]byte(unrelatedTest)), ""))
	if len(finalized.Warnings) != 1 || finalized.Warnings[0].RuleID != ruleMissingTests {
		t.Fatalf("unrelated test suppressed warning: %+v", finalized)
	}

	relatedTest := strings.ReplaceAll(unrelatedTest, "other/helper_test.go", "pkg/api_test.go")
	relatedTest = strings.ReplaceAll(relatedTest, "package other", "package api")
	finalized = finalizeRuleMatches(runRules(parseUnifiedDiff([]byte(relatedTest)), ""))
	if len(finalized.Warnings) != 0 {
		t.Fatalf("related test did not suppress warning: %+v", finalized.Warnings)
	}
}

func TestFinalizeRuleMatchesRoutesDedupesAndSummarizes(t *testing.T) {
	matches := []ruleMatch{
		{
			Severity:       "medium",
			Category:       "security",
			File:           "b.go",
			Line:           2,
			Title:          "lower severity duplicate",
			Evidence:       `token = "abcdef123456"`,
			Recommendation: "rotate the token",
			Confidence:     0.80,
			Source:         "diff",
			RuleID:         "security.z_rule",
		},
		{
			Severity:       "high",
			Category:       "security",
			File:           "b.go",
			Line:           2,
			Title:          "higher severity duplicate",
			Evidence:       `token = "abcdef123456"`,
			Recommendation: "rotate the token",
			Confidence:     0.80,
			Source:         "diff",
			RuleID:         "security.m_rule",
		},
		{
			Severity:       "high",
			Category:       "security",
			File:           "b.go",
			Line:           2,
			Title:          "lexical rule id wins",
			Evidence:       `token = "abcdef123456"`,
			Recommendation: "rotate the token",
			Confidence:     0.80,
			Source:         "diff",
			RuleID:         "security.a_rule",
		},
		{
			Severity:       "high",
			Category:       "testing",
			File:           "a.go",
			Line:           1,
			Title:          "high severity but low confidence",
			Evidence:       "inferred missing coverage",
			Recommendation: "add tests",
			Confidence:     0.799,
			Source:         "diff",
			RuleID:         "tests.high",
		},
		{
			Severity:       "low",
			Category:       "testing",
			File:           "a.go",
			Line:           1,
			Title:          "lower severity warning duplicate",
			Evidence:       "inferred missing coverage",
			Recommendation: "add tests",
			Confidence:     0.799,
			Source:         "diff",
			RuleID:         "tests.low",
		},
		{
			Severity:       "critical",
			Category:       "security",
			File:           "c.go",
			Line:           7,
			Title:          "authorization header leak",
			Evidence:       "Authorization: Bearer abcdefghijklmnopqrstuvwxyz",
			Recommendation: "remove the header value",
			Confidence:     0.95,
			Source:         "diff",
			RuleID:         "security.authorization",
		},
	}

	first := finalizeRuleMatches(matches)
	second := finalizeRuleMatches(matches)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("finalizeRuleMatches is not stable:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if len(first.Findings) != 2 || len(first.Warnings) != 1 {
		t.Fatalf("findings/warnings = %d/%d, want 2/1: %+v", len(first.Findings), len(first.Warnings), first)
	}
	if first.SuppressedMatches != 3 {
		t.Fatalf("suppressed matches = %d, want 3", first.SuppressedMatches)
	}
	if !first.NeedsHumanReview {
		t.Fatalf("needs human review = false, want true")
	}
	if first.Findings[0].RuleID != "security.a_rule" {
		t.Fatalf("first finding winner = %+v, want security.a_rule", first.Findings[0])
	}
	if first.Warnings[0].RuleID != "tests.high" {
		t.Fatalf("warning winner = %+v, want tests.high", first.Warnings[0])
	}
	if first.SeverityCounts["critical"] != 1 || first.SeverityCounts["high"] != 2 {
		t.Fatalf("severity counts = %+v, want critical=1 high=2", first.SeverityCounts)
	}
	assertStringSlice(t, first.FindingRuleIDs, []string{"security.a_rule", "security.authorization"})
	assertStringSlice(t, first.WarningRuleIDs, []string{"tests.high"})
	if first.Redactions == 0 || strings.Contains(first.Findings[0].Evidence, "abcdef123456") ||
		strings.Contains(first.Findings[1].Evidence, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("redaction failed: redactions=%d findings=%+v", first.Redactions, first.Findings)
	}
}

func TestFinalizeRuleMatchesKeepsWarningFromSuppressingFinding(t *testing.T) {
	matches := []ruleMatch{
		{
			Severity:       "low",
			Category:       "security",
			File:           "same.go",
			Line:           10,
			Title:          "low confidence",
			Evidence:       "maybe",
			Recommendation: "review",
			Confidence:     0.70,
			Source:         "diff",
			RuleID:         "security.warning",
		},
		{
			Severity:       "medium",
			Category:       "security",
			File:           "same.go",
			Line:           10,
			Title:          "high confidence",
			Evidence:       "clear",
			Recommendation: "fix",
			Confidence:     0.90,
			Source:         "diff",
			RuleID:         "security.finding",
		},
	}
	finalized := finalizeRuleMatches(matches)
	if len(finalized.Findings) != 1 || len(finalized.Warnings) != 1 {
		t.Fatalf("findings/warnings = %d/%d, want 1/1: %+v", len(finalized.Findings), len(finalized.Warnings), finalized)
	}
	if finalized.SuppressedMatches != 0 {
		t.Fatalf("suppressed matches = %d, want 0", finalized.SuppressedMatches)
	}
}

func TestGovernanceWarningsDoNotDedupeTogether(t *testing.T) {
	matches := []ruleMatch{
		governanceWarning(
			"filter",
			newCommandSpec(commandCheckGoTest, nil, nil),
			ruleGovernanceCommandBlocked,
			"Command was blocked by the command gate",
			"blocked by test gate",
		),
		governanceWarning(
			"permission",
			newCommandSpec(commandCheckGoTest, nil, nil),
			ruleGovernancePermission,
			"Command requires permission review",
			"permission denied",
		),
		governanceWarning(
			"permission",
			newCommandSpec(commandCheckGoVet, nil, nil),
			ruleGovernancePermission,
			"Command requires permission review",
			"permission denied",
		),
	}
	finalized := finalizeRuleMatches(matches)
	if len(finalized.Warnings) != 3 {
		t.Fatalf("governance warnings = %+v, want 3 distinct warnings", finalized.Warnings)
	}
	if finalized.SuppressedMatches != 0 {
		t.Fatalf("suppressed matches = %d, want 0", finalized.SuppressedMatches)
	}
}

func TestLifecycleConfidenceRouting(t *testing.T) {
	singleHunk := strings.Join([]string{
		"diff --git a/leak.go b/leak.go",
		"--- a/leak.go",
		"+++ b/leak.go",
		"@@ -1,3 +1,8 @@",
		" package leak",
		"+func open(name string) error {",
		"+\tf, err := os.Open(name)",
		"+\tif err != nil { return err }",
		"+\t_ = f",
		"+\treturn nil",
		"+}",
	}, "\n")
	singleFinalized := finalizeRuleMatches(runRules(parseUnifiedDiff([]byte(singleHunk)), ""))
	if len(singleFinalized.Findings) != 1 || singleFinalized.Findings[0].RuleID != ruleUnclosedFile {
		t.Fatalf("single hunk finalized = %+v, want unclosed file finding", singleFinalized)
	}

	crossHunk := strings.Join([]string{
		"diff --git a/boundary.go b/boundary.go",
		"--- a/boundary.go",
		"+++ b/boundary.go",
		"@@ -1,3 +1,6 @@",
		" package boundary",
		"+func open(name string) error {",
		"+\tf, err := os.Open(name)",
		"+\tif err != nil { return err }",
		"@@ -20,3 +23,5 @@ func open(name string) error {",
		"+\t_ = f",
		"+\treturn nil",
		"+}",
	}, "\n")
	crossFinalized := finalizeRuleMatches(runRules(parseUnifiedDiff([]byte(crossHunk)), ""))
	if len(crossFinalized.Findings) != 0 || len(crossFinalized.Warnings) != 1 ||
		crossFinalized.Warnings[0].RuleID != ruleUnclosedFile || !crossFinalized.NeedsHumanReview {
		t.Fatalf("cross hunk finalized = %+v, want unclosed file warning", crossFinalized)
	}
}

func TestRedactTextCoversCommonSecrets(t *testing.T) {
	samples := []struct {
		name     string
		input    string
		leak     string
		wantType string
	}{
		{name: "aws access key", input: "key AKIAIOSFODNN7EXAMPLE", leak: "AKIAIOSFODNN7EXAMPLE", wantType: "aws-key"},
		{name: "github token", input: "token ghp_TEST_ONLY_NOT_A_REAL_TOKEN_123456", leak: "ghp_TEST_ONLY_NOT_A_REAL_TOKEN_123456", wantType: "github-token"},
		{name: "openai token", input: "OPENAI_API_KEY=sk-test_only_not_a_real_token_123456", leak: "sk-test_only_not_a_real_token_123456", wantType: "openai-token"},
		{name: "api key assignment", input: `api_key = "abcdef123456"`, leak: "abcdef123456", wantType: "api-key"},
		{name: "token assignment", input: `serviceToken := "tokenvalue123"`, leak: "tokenvalue123", wantType: "token"},
		{name: "password assignment", input: `password = "hunter2!"`, leak: "hunter2!", wantType: "password"},
		{name: "secret assignment", input: `secret: "supersecret"`, leak: "supersecret", wantType: "secret"},
		{name: "private key assignment", input: `private_key = "abcdef123456"`, leak: "abcdef123456", wantType: "private-key"},
		{name: "pem block", input: "-----BEGIN PRIVATE KEY-----\nabcdef\n-----END PRIVATE KEY-----", leak: "abcdef", wantType: "private-key"},
		{name: "authorization bearer", input: "Authorization: Bearer abcdefghijklmnopqrstuvwxyz", leak: "abcdefghijklmnopqrstuvwxyz", wantType: "authorization"},
		{name: "authorization basic", input: "Authorization: Basic dXNlcjpwYXNzMTIz", leak: "dXNlcjpwYXNzMTIz", wantType: "authorization"},
		{name: "bare bearer", input: "Bearer abcdefghijklmnopqrstuvwxyz", leak: "abcdefghijklmnopqrstuvwxyz", wantType: "bearer-token"},
		{name: "x api key", input: "X-API-Key: abcdefghijklmnop", leak: "abcdefghijklmnop", wantType: "api-key"},
		{name: "cookie", input: "Cookie: session=abcdef123456; path=/", leak: "abcdef123456", wantType: "cookie"},
		{name: "set cookie", input: "Set-Cookie: session=abcdef123456; HttpOnly", leak: "abcdef123456", wantType: "cookie"},
		{name: "postgres dsn", input: "postgres://user:pass123456@example.com/db", leak: "pass123456", wantType: "connection-string"},
		{name: "mysql dsn", input: "mysql://user:pass123456@example.com/db", leak: "pass123456", wantType: "connection-string"},
		{name: "redis dsn", input: "redis://:pass123456@example.com:6379/0", leak: "pass123456", wantType: "connection-string"},
		{name: "mongodb dsn", input: "mongodb+srv://user:pass123456@example.com/db", leak: "pass123456", wantType: "connection-string"},
		{name: "url userinfo", input: "https://user:pass123456@example.com/path", leak: "pass123456", wantType: "url-userinfo"},
	}

	for _, sample := range samples {
		t.Run(sample.name, func(t *testing.T) {
			redacted := redactText(sample.input)
			if redacted.Count == 0 {
				t.Fatalf("redaction count = 0 for %q", sample.input)
			}
			if strings.Contains(redacted.Text, sample.leak) {
				t.Fatalf("redacted text %q still contains %q", redacted.Text, sample.leak)
			}
			if !containsString(redacted.Types, sample.wantType) {
				t.Fatalf("redaction types = %#v, want %q", redacted.Types, sample.wantType)
			}
			again := redactText(redacted.Text)
			if again.Count != 0 || again.Text != redacted.Text {
				t.Fatalf("redaction is not idempotent: first=%+v second=%+v", redacted, again)
			}
		})
	}
}

func TestParseWarningMessagesAreRedacted(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/bad.go b/bad.go",
		"--- a/bad.go",
		"+++ b/bad.go",
		"@@ AKIAIOSFODNN7EXAMPLE broken",
		"+func bad() {}",
	}, "\n")
	parsed := parseUnifiedDiff([]byte(diff))
	messages, redactions := redactParseWarningMessages(parsed.Warnings)
	if redactions == 0 || len(messages) == 0 {
		t.Fatalf("messages=%+v redactions=%d, want redacted parse warning", messages, redactions)
	}
	if strings.Contains(strings.Join(messages, "\n"), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("parse warning leaked secret: %+v", messages)
	}
}

func TestCLIFinalSummaryFromFixtures(t *testing.T) {
	tests := []struct {
		fixture               string
		wantFindings          int
		wantWarnings          int
		wantNeedsHumanReview  bool
		wantSuppressedMatches int
		wantFindingRuleIDs    []string
		wantWarningRuleIDs    []string
	}{
		{fixture: "clean"},
		{
			fixture:            "secret_leak",
			wantFindings:       1,
			wantFindingRuleIDs: []string{ruleSecretHardcoded},
		},
		{
			fixture:              "missing_tests",
			wantWarnings:         1,
			wantNeedsHumanReview: true,
			wantWarningRuleIDs:   []string{ruleMissingTests},
		},
		{
			fixture:               "duplicate_finding",
			wantFindings:          1,
			wantSuppressedMatches: 1,
			wantFindingRuleIDs:    []string{ruleSecretHardcoded},
		},
		{
			fixture:              "sandbox_failure",
			wantWarnings:         3,
			wantNeedsHumanReview: true,
			wantWarningRuleIDs:   []string{ruleSandboxRunFailed, ruleSandboxRunSkipped},
		},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			code, stdout, stderr := runForTest(t, []string{"--fixture", tt.fixture, "--dry-run"}, nil, nil)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
			}
			var got reviewSummary
			mustUnmarshalSummary(t, stdout, &got)
			if got.Findings != tt.wantFindings || got.Warnings != tt.wantWarnings ||
				got.NeedsHumanReview != tt.wantNeedsHumanReview ||
				got.SuppressedMatches != tt.wantSuppressedMatches {
				t.Fatalf("summary = %+v", got)
			}
			assertStringSlice(t, got.FindingRuleIDs, tt.wantFindingRuleIDs)
			assertStringSlice(t, got.WarningRuleIDs, tt.wantWarningRuleIDs)
			for _, leaked := range []string{"ghp_", "serviceToken", "diff --git"} {
				if strings.Contains(stdout, leaked) {
					t.Fatalf("stdout leaked %q in summary: %s", leaked, stdout)
				}
			}
		})
	}
}

func runForTest(
	t *testing.T,
	args []string,
	env map[string]string,
	runner gitDiffRunner,
) (int, string, string) {
	t.Helper()
	return runForTestWithHooks(t, args, env, runner, runtimeHooks{})
}

func runForTestWithHooks(
	t *testing.T,
	args []string,
	env map[string]string,
	runner gitDiffRunner,
	hooks runtimeHooks,
) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if !endsWithValueFlag(args) && !hasFlagArg(args, "output-dir") {
		args = append(args, "--output-dir", filepath.Join(t.TempDir(), "output"))
	}
	if !endsWithValueFlag(args) && !hasFlagArg(args, "db-path") {
		args = append(args, "--db-path", filepath.Join(t.TempDir(), "reviews.db"))
	}
	if hooks.reviewStore == nil {
		hooks.reviewStore = newMemoryReviewStore()
	}
	if hooks.taskID == "" {
		hooks.taskID = "review-test"
	}
	if env == nil {
		env = map[string]string{}
	}
	getenv := func(key string) string {
		return env[key]
	}
	if runner == nil {
		runner = func(context.Context, string, []string) ([]byte, []byte, error) {
			return nil, nil, errors.New("unexpected git runner call")
		}
	}
	code := runWithHooks(args, &stdout, &stderr, getenv, runner, hooks)
	return code, stdout.String(), stderr.String()
}

func runRawForTest(
	t *testing.T,
	args []string,
	env map[string]string,
	runner gitDiffRunner,
	hooks runtimeHooks,
) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if env == nil {
		env = map[string]string{}
	}
	getenv := func(key string) string {
		return env[key]
	}
	if runner == nil {
		runner = func(context.Context, string, []string) ([]byte, []byte, error) {
			return nil, nil, errors.New("unexpected git runner call")
		}
	}
	code := runWithHooks(args, &stdout, &stderr, getenv, runner, hooks)
	return code, stdout.String(), stderr.String()
}

func hasFlagArg(args []string, name string) bool {
	flagName := "--" + name
	for i, arg := range args {
		if arg == flagName || strings.HasPrefix(arg, flagName+"=") {
			return true
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if i > 0 && args[i-1] == flagName {
			return true
		}
	}
	return false
}

func endsWithValueFlag(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[len(args)-1] {
	case "--diff-file", "--repo-path", "--files", "--fixture", "--show-task",
		"--runtime", "--e2b-template", "--db-path", "--output-dir":
		return true
	default:
		return false
	}
}

type recordingSandboxRunner struct {
	calls []commandSpec
}

func (r *recordingSandboxRunner) RunSandboxCommand(_ context.Context, spec commandSpec) sandboxRun {
	r.calls = append(r.calls, spec)
	return sandboxRun{
		Runtime:    runtimeFake,
		Command:    string(spec.Kind),
		ExitCode:   0,
		Stdout:     "ok",
		TimedOut:   false,
		DurationMS: 1,
	}
}

func mustUnmarshalSummary(t *testing.T, stdout string, got *reviewSummary) {
	t.Helper()
	if err := json.Unmarshal([]byte(stdout), got); err != nil {
		t.Fatalf("unmarshal summary: %v\n%s", err, stdout)
	}
}

func readReportFromSummary(t *testing.T, summary reviewSummary) reviewReport {
	t.Helper()
	if summary.ReportPaths.JSON == "" {
		t.Fatalf("summary has no json report path: %+v", summary)
	}
	data, err := os.ReadFile(filepath.FromSlash(summary.ReportPaths.JSON))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report reviewReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal report: %v\n%s", err, data)
	}
	return report
}

func requireSQLiteDriver(t *testing.T) {
	t.Helper()
	if !sqlDriverAvailable(sqliteDriverName) {
		t.Skip("sqlite3 driver is unavailable in this build")
	}
}

func sampleReviewReport(taskID string) reviewReport {
	started := time.Unix(1000, 0).UTC()
	finished := started.Add(25 * time.Millisecond)
	return reviewReport{
		TaskID:     taskID,
		Status:     reviewStatusCompleted,
		Conclusion: reviewConclusionFindings,
		StartedAt:  started,
		FinishedAt: finished,
		DurationMS: 25,
		Input: reportInput{
			Kind:       inputKindFixture,
			Source:     "sample",
			DiffBytes:  12,
			DiffSHA256: "abc123",
			Files: []reportFileSummary{{
				Path: "sample.go",
				Hunks: []reportHunkSummary{{
					Header:     "@@ -1 +1 @@",
					OldStart:   1,
					OldCount:   1,
					NewStart:   1,
					NewCount:   1,
					AddedLines: []int{1},
				}},
			}},
		},
		Runtime: reportRuntime{
			Runtime:   runtimeFake,
			DryRun:    true,
			OutputDir: "output",
			DBPath:    "reviews.db",
		},
		Parse: reportParse{
			ChangedFiles:   1,
			Hunks:          1,
			CandidateLines: 1,
		},
		Rules: reportRules{
			RuleMatches:    1,
			Findings:       1,
			FindingRuleIDs: []string{ruleSecretHardcoded},
			SeverityCounts: map[string]int{"high": 1},
		},
		Governance: reportGovernance{
			SkillName:        codeReviewSkillName,
			SkillDigest:      "digest",
			CommandsPlanned:  1,
			CommandsAllowed:  1,
			FilterDecisions:  []governanceDecision{{Command: "checkGoVersion", Decision: governanceDecisionAllow}},
			SandboxRuns:      []sandboxRun{{Runtime: runtimeFake, Command: "checkGoVersion", ExitCode: 0, DurationMS: 1}},
			PermissionBlocks: 0,
		},
		Findings: []reviewFinding{{
			Severity:       "high",
			Category:       "security",
			File:           "sample.go",
			Line:           1,
			Title:          "sample finding",
			Evidence:       "<redacted:token>",
			Recommendation: "remove the secret",
			Confidence:     0.9,
			Source:         "diff",
			RuleID:         ruleSecretHardcoded,
		}},
		Metrics: reportMetrics{
			TotalDurationMS: 25,
			ToolCalls:       1,
			Findings:        1,
			SeverityCounts:  map[string]int{"high": 1},
			Redactions:      1,
		},
		Artifacts: []reportArtifact{
			{Kind: artifactKindJSONReport, Path: "output/task/review_report.json", SHA256: "jsonsha", Bytes: 10},
			{Kind: artifactKindMarkdownReport, Path: "output/task/review_report.md", SHA256: "mdsha", Bytes: 8},
		},
		ReportPaths: reportPaths{
			JSON:     "output/task/review_report.json",
			Markdown: "output/task/review_report.md",
		},
	}
}

func minimalDiff() string {
	return strings.Join([]string{
		"diff --git a/hello.go b/hello.go",
		"index 1111111..2222222 100644",
		"--- a/hello.go",
		"+++ b/hello.go",
		"@@ -1,3 +1,4 @@",
		" package hello",
		"+func message() string { return \"hello\" }",
		"",
	}, "\n")
}

func splitRuleIDs(matches []ruleMatch) (map[string]int, map[string]int) {
	findings := map[string]int{}
	warnings := map[string]int{}
	for _, match := range matches {
		if match.Confidence >= 0.80 {
			findings[match.RuleID]++
			continue
		}
		warnings[match.RuleID]++
	}
	return findings, warnings
}

func assertRuleIDs(t *testing.T, got map[string]int, want []string) {
	t.Helper()
	wantSet := map[string]bool{}
	for _, id := range want {
		wantSet[id] = true
		if got[id] == 0 {
			t.Fatalf("rule IDs = %+v, missing %s", got, id)
		}
	}
	for id := range got {
		if !wantSet[id] {
			t.Fatalf("rule IDs = %+v, unexpected %s", got, id)
		}
	}
}

func assertStringSlice(t *testing.T, got []string, want []string) {
	t.Helper()
	if got == nil {
		got = []string{}
	}
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slice = %#v, want %#v", got, want)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
