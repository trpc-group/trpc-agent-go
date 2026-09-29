//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cragent "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/agent"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

type stubReviewAgent struct {
	run      func(context.Context, cragent.Request) error
	closeErr error
	closed   bool
}

func (a *stubReviewAgent) Run(ctx context.Context, req cragent.Request) (review.Result, error) {
	return review.Result{}, a.run(ctx, req)
}

func (a *stubReviewAgent) Close() error {
	a.closed = true
	return a.closeErr
}

func TestRunWithContextPropagatesCancellationAndClosesAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ag := &stubReviewAgent{
		run: func(ctx context.Context, _ cragent.Request) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	err := runWithContext(ctx, testRunOptions(t), func(cragent.Config) (reviewAgent, error) {
		return ag, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if !ag.closed {
		t.Fatal("expected Agent.Close to run after cancellation")
	}
}

func TestRunWithContextJoinsRunAndCleanupErrors(t *testing.T) {
	runErr := errors.New("review failed")
	closeErr := errors.New("cleanup failed")
	ag := &stubReviewAgent{
		run:      func(context.Context, cragent.Request) error { return runErr },
		closeErr: closeErr,
	}

	err := runWithContext(context.Background(), testRunOptions(t), func(cragent.Config) (reviewAgent, error) {
		return ag, nil
	})
	if !errors.Is(err, runErr) || !errors.Is(err, closeErr) {
		t.Fatalf("expected joined run and cleanup errors, got %v", err)
	}
	if !ag.closed {
		t.Fatal("expected Agent.Close to run")
	}
}

func TestRunWithContextReturnsCleanupFailureAfterSuccessfulReview(t *testing.T) {
	closeErr := errors.New("cleanup failed")
	ag := &stubReviewAgent{
		run:      func(context.Context, cragent.Request) error { return nil },
		closeErr: closeErr,
	}

	err := runWithContext(context.Background(), testRunOptions(t), func(cragent.Config) (reviewAgent, error) {
		return ag, nil
	})
	if !errors.Is(err, closeErr) {
		t.Fatalf("expected cleanup error, got %v", err)
	}
}

func testRunOptions(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "sample.diff")
	if err := os.WriteFile(diffPath, []byte(""+
		"diff --git a/foo.go b/foo.go\n"+
		"--- a/foo.go\n"+
		"+++ b/foo.go\n"+
		"@@ -1 +1 @@\n"+
		"+package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Options{
		DiffFile:   diffPath,
		OutputDir:  dir,
		Mode:       cragent.ModeRuleOnly,
		Runtime:    cragent.RuntimeLocalFallback,
		SkillsRoot: filepath.Join("..", "..", "skills"),
	}
}

func TestRunWritesReportFiles(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "sample.diff")
	if err := os.WriteFile(diffPath, []byte(""+
		"diff --git a/foo.go b/foo.go\n"+
		"index 1111111..2222222 100644\n"+
		"--- a/foo.go\n"+
		"+++ b/foo.go\n"+
		"@@ -1,1 +1,2 @@\n"+
		" package foo\n"+
		"+func Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := Options{
		DiffFile:   diffPath,
		OutputDir:  dir,
		Mode:       cragent.ModeRuleOnly,
		Runtime:    cragent.RuntimeLocalFallback,
		SkillsRoot: filepath.Join("..", "..", "skills"),
	}
	if err := Run(opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "review_report.json")); err != nil {
		t.Fatalf("expected json report: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "review_report.md")); err != nil {
		t.Fatalf("expected md report: %v", err)
	}
}

func TestRunRejectsUnknownModelProvider(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "sample.diff")
	if err := os.WriteFile(diffPath, []byte(""+
		"diff --git a/foo.go b/foo.go\n"+
		"--- a/foo.go\n"+
		"+++ b/foo.go\n"+
		"@@ -1,1 +1,2 @@\n"+
		" package foo\n"+
		"+func Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Run(Options{
		DiffFile:      diffPath,
		OutputDir:     dir,
		Mode:          cragent.ModeFakeModel,
		Runtime:       cragent.RuntimeLocalFallback,
		SkillsRoot:    filepath.Join("..", "..", "skills"),
		ModelProvider: "unknown",
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported model provider "unknown"`) {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}

func TestParseOptionsTracksExplicitFalseCapabilities(t *testing.T) {
	opts, err := parseOptions([]string{"--sandbox=false", "--model-enabled=false"})
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if opts.SandboxEnabled == nil || *opts.SandboxEnabled || opts.ModelEnabled == nil || *opts.ModelEnabled {
		t.Fatalf("explicit false capabilities were not retained: sandbox=%v model=%v", opts.SandboxEnabled, opts.ModelEnabled)
	}
	if !opts.ExplicitFlags["sandbox"] || !opts.ExplicitFlags["model-enabled"] {
		t.Fatalf("explicit capability flags not tracked: %+v", opts.ExplicitFlags)
	}
}

func TestRunCanUseFileListForDiffGeneration(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package foo\n\nfunc Bad() { panic(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("foo.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	opts := Options{
		FileList:   listPath,
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

func TestRunCarriesBaseHeadRefsToReport(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "sample.diff")
	if err := os.WriteFile(diffPath, []byte(""+
		"diff --git a/foo.go b/foo.go\n"+
		"--- a/foo.go\n"+
		"+++ b/foo.go\n"+
		"@@ -1,1 +1,2 @@\n"+
		" package foo\n"+
		"+func Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := Options{
		DiffFile:   diffPath,
		BaseRef:    "main",
		HeadRef:    "feature/review-agent",
		OutputDir:  dir,
		Mode:       cragent.ModeRuleOnly,
		Runtime:    cragent.RuntimeLocalFallback,
		SkillsRoot: filepath.Join("..", "..", "skills"),
	}
	if err := Run(opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "review_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(data), `"base_ref": "main"`) || !strings.Contains(string(data), `"head_ref": "feature/review-agent"`) {
		t.Fatalf("expected base/head refs in report: %s", data)
	}
}
