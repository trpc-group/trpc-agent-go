//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	cragent "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/agent"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage/sqlite"
)

func TestRunCanUseRepoPathForDiffGeneration(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	opts := Options{
		RepoPath:   repo,
		OutputDir:  out,
		Mode:       cragent.ModeRuleOnly,
		Runtime:    cragent.RuntimeLocalFallback,
		SkillsRoot: filepath.Join("..", "..", "skills"),
	}
	if err := Run(opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "review_report.json")); err != nil {
		t.Fatalf("expected json report: %v", err)
	}
}

func TestRunInfersCurrentDirectoryRepoPathWhenInputIsOmitted(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/reviewme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package foo\n\nfunc Bad() { panic(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsRoot, err := filepath.Abs(filepath.Join("..", "..", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	}()

	out := t.TempDir()
	opts := Options{
		OutputDir:  out,
		Mode:       cragent.ModeRuleOnly,
		Runtime:    cragent.RuntimeLocalFallback,
		SkillsRoot: skillsRoot,
	}
	if err := Run(opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "review_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(data), `"module_path": "example.com/reviewme"`) ||
		!strings.Contains(string(data), `"file": "foo.go"`) {
		t.Fatalf("expected report to use inferred current directory repo input, got %s", data)
	}
}

func TestRunUsesGeneratedRepoFixtureWithBaseAndHeadRefs(t *testing.T) {
	repo := createRiskyGitRepoFixture(t)

	out := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	if err := Run(Options{
		RepoPath:   repo,
		BaseRef:    "base",
		HeadRef:    "head",
		OutputDir:  out,
		SQLitePath: dbPath,
		Mode:       cragent.ModeRuleOnly,
		Runtime:    cragent.RuntimeLocalFallback,
		SkillsRoot: filepath.Join("..", "..", "skills"),
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	reportBytes, err := os.ReadFile(filepath.Join(out, "review_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report reportData
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	for _, ruleID := range []string{"secret-leak", "panic-direct", "context-leak", "resource-leak", "db-lifecycle", "missing-test-hint"} {
		if !reportHasRuleID(report, ruleID) {
			t.Fatalf("expected %s from generated git repo, findings=%+v warnings=%+v", ruleID, report.Findings, report.Warnings)
		}
	}
	if reportHasRuleID(report, "goroutine-leak") {
		t.Fatalf("goroutine waiting on ctx.Done must not be reported as unguarded: %+v", report.Findings)
	}
	if !strings.Contains(string(report.InputMetadata), `"module_path": "example.com/risky-service"`) ||
		!strings.Contains(string(report.InputMetadata), `"service.go"`) ||
		!strings.Contains(string(report.InputMetadata), `"service"`) ||
		!strings.Contains(string(report.InputMetadata), `"base_ref": "base"`) ||
		!strings.Contains(string(report.InputMetadata), `"head_ref": "head"`) {
		t.Fatalf("expected git repo metadata in report, got %s", report.InputMetadata)
	}

	diagnosticsBytes, err := os.ReadFile(filepath.Join(out, "review_diagnostics.json"))
	if err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	for _, want := range []string{`"input_metadata"`, `"module_path": "example.com/risky-service"`, `"changed_go_files"`, `"service.go"`} {
		if !strings.Contains(string(diagnosticsBytes), want) {
			t.Fatalf("diagnostics missing %q: %s", want, diagnosticsBytes)
		}
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	task, err := store.TaskByID(ctx, report.TaskID)
	if err != nil {
		t.Fatalf("query task: %v", err)
	}
	if task.Status != "done" || task.RepoPath != repo {
		t.Fatalf("unexpected task record: %+v", task)
	}
	if findings, err := store.FindingsByTaskID(ctx, report.TaskID); err != nil || len(findings) == 0 {
		t.Fatalf("expected sqlite findings, findings=%+v err=%v", findings, err)
	}
	if decisions, err := store.DecisionsByTaskID(ctx, report.TaskID); err != nil || len(decisions) == 0 {
		t.Fatalf("expected sqlite permission decisions, decisions=%+v err=%v", decisions, err)
	}
	if runs, err := store.SandboxRunsByTaskID(ctx, report.TaskID); err != nil || len(runs) == 0 {
		t.Fatalf("expected sqlite sandbox runs, runs=%+v err=%v", runs, err)
	}
	if metrics, err := store.MetricsByTaskID(ctx, report.TaskID); err != nil || metrics.FindingCount == 0 {
		t.Fatalf("expected sqlite metrics, metrics=%+v err=%v", metrics, err)
	}
	if artifacts, err := store.ArtifactsByTaskID(ctx, report.TaskID); err != nil || len(artifacts) < 3 {
		t.Fatalf("expected sqlite artifacts, artifacts=%+v err=%v", artifacts, err)
	}
}

func createRiskyGitRepoFixture(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "risky-service")
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "cr-agent@example.test")
	runGit(t, repo, "config", "user.name", "CR Agent Test")
	writeRepoFile(t, repo, "go.mod", "module example.com/risky-service\n\ngo 1.22\n")
	writeRepoFile(t, repo, "service.go", "package service\n\nfunc Existing() string { return \"ok\" }\n")
	writeRepoFile(t, repo, "service_test.go", "package service\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T) {\n\tif Existing() != \"ok\" {\n\t\tt.Fatal(\"unexpected result\")\n\t}\n}\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "--quiet", "-m", "base")
	runGit(t, repo, "tag", "base")
	writeRepoFile(t, repo, "service.go", `package service

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"time"
)

const adminToken = "sk-live-realistic1234567890abcdef"

func Existing() string { return "ok" }

func ImportHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	_ = cancel
	file, err := os.Open("payload.json")
	if err != nil {
		panic(err)
	}
	_ = file
	db, err := sql.Open("sqlite", "file:risky.db")
	if err != nil {
		panic(err)
	}
	_ = db
	go func() {
		<-ctx.Done()
	}()
	// TODO(ops): add focused tests before shipping this import path.
	w.WriteHeader(http.StatusAccepted)
}
`)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "--quiet", "-m", "head")
	runGit(t, repo, "tag", "head")
	return repo
}

func writeRepoFile(t *testing.T, repo string, name string, content string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmdArgs := args
	if args[0] != "init" {
		cmdArgs = append([]string{"-C", repo}, args...)
	} else if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	} else {
		cmdArgs = []string{"init", repo}
	}
	cmd := exec.Command("git", cmdArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func reportHasRuleID(report reportData, ruleID string) bool {
	for _, finding := range append(report.Findings, report.Warnings...) {
		if finding.RuleID == ruleID {
			return true
		}
	}
	return false
}
