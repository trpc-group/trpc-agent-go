//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main is the entry point for the skills_code_review_agent example.
// It reads a unified diff (or scans a repo working tree), applies static
// analysis rules, persists results to SQLite, and emits JSON + Markdown
// review reports.
//
// Usage:
//
//	go run . --diff-file=changes.patch [--db=review.db] [--dry-run]
//	go run . --repo-path=/path/to/repo   [--db=review.db] [--dry-run]
package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/parser"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/reporter"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/storage"
)

var (
	flagDiffFile = flag.String("diff-file", "", "Path to a unified diff file to review")
	flagRepoPath = flag.String("repo-path", "", "Path to a git repo; runs 'git diff HEAD' to obtain the diff")
	flagDB       = flag.String("db", "review.db", "Path to the SQLite database file")
	flagDryRun   = flag.Bool("dry-run", false, "Run without a sandbox executor (rule-only mode)")
	flagOut      = flag.String("out", ".", "Directory to write review_report.json and review_report.md")
)

func main() {
	flag.Parse()

	if *flagDiffFile == "" && *flagRepoPath == "" {
		log.Fatal("provide --diff-file or --repo-path")
	}

	start := time.Now()

	diffContent, err := loadDiff()
	if err != nil {
		log.Fatalf("load diff: %v", err)
	}

	diffs, err := parser.Parse(strings.NewReader(diffContent))
	if err != nil {
		log.Fatalf("parse diff: %v", err)
	}

	taskID := uuid.New().String()
	diffHash := fmt.Sprintf("%x", sha256.Sum256([]byte(diffContent)))

	db, err := storage.Open(*flagDB)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer db.Close()

	if err := db.InsertTask(taskID, diffHash, *flagRepoPath); err != nil {
		log.Fatalf("insert task: %v", err)
	}

	findings := rules.Run(diffs)

	var sandboxDuration int64
	var toolCalls int

	if !*flagDryRun {
		sandboxDuration, toolCalls = runSandbox(taskID, db, diffs)
	}

	totalMs := time.Since(start).Milliseconds()

	rpt := reporter.Build(taskID, *flagDiffFile, *flagRepoPath, findings, reporter.Metrics{
		TotalDurationMs:   totalMs,
		SandboxDurationMs: sandboxDuration,
		ToolCallCount:     toolCalls,
	})

	jsonBody, err := reporter.ToJSON(rpt)
	if err != nil {
		log.Fatalf("marshal report: %v", err)
	}
	mdBody := reporter.ToMarkdown(rpt)

	taskStatus := storage.StatusDone

	if err := db.SaveReport(taskID, jsonBody, mdBody); err != nil {
		log.Printf("save report: %v", err)
		taskStatus = storage.StatusFailed
	}

	rows := make([]storage.FindingRow, 0, len(findings))
	for _, f := range findings {
		rows = append(rows, storage.FindingRow{
			TaskID:         taskID,
			Severity:       f.Severity,
			Category:       f.Category,
			File:           f.File,
			Line:           f.Line,
			Title:          f.Title,
			Evidence:       f.Evidence,
			Recommendation: f.Recommendation,
			Confidence:     f.Confidence,
			Source:         f.Source,
			RuleID:         f.RuleID,
		})
	}
	if err := db.InsertFindings(rows); err != nil {
		log.Printf("insert findings: %v", err)
		taskStatus = storage.StatusFailed
	}

	if err := db.FinishTask(taskID, taskStatus); err != nil {
		log.Printf("finish task: %v", err)
	}

	jsonPath := filepath.Join(*flagOut, "review_report.json")
	mdPath := filepath.Join(*flagOut, "review_report.md")
	if err := os.WriteFile(jsonPath, []byte(jsonBody), 0o644); err != nil {
		log.Fatalf("write json: %v", err)
	}
	if err := os.WriteFile(mdPath, []byte(mdBody), 0o644); err != nil {
		log.Fatalf("write md: %v", err)
	}

	fmt.Printf("Task %s: %d findings, %d warnings, reports written to %s and %s\n",
		taskID, len(rpt.Findings), len(rpt.Warnings), jsonPath, mdPath)
}

func loadDiff() (string, error) {
	if *flagDiffFile != "" {
		b, err := os.ReadFile(*flagDiffFile)
		return string(b), err
	}
	// Obtain diff from git working tree.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", *flagRepoPath, "diff", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		// Try staged diff as fallback.
		cmd2 := exec.CommandContext(ctx, "git", "-C", *flagRepoPath, "diff", "--cached")
		out, err = cmd2.Output()
	}
	return string(out), err
}

// runSandbox runs go vet on changed packages and records sandbox runs.
// Container runtime is preferred; local is the fallback per issue requirements.
func runSandbox(taskID string, db *storage.DB, diffs []parser.FileDiff) (durationMs int64, toolCalls int) {
	pkgs := changedGoPackages(diffs)
	if len(pkgs) == 0 {
		return 0, 0
	}

	start := time.Now()
	repoPath := *flagRepoPath
	if repoPath == "" {
		// Without an explicit repo path we cannot vet the right module.
		log.Printf("skipping sandbox: --repo-path required for go vet")
		return 0, 0
	}

	for _, pkg := range pkgs {
		toolCalls++
		t0 := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, "go", "vet", "./"+pkg+"/...")
		cmd.Dir = repoPath
		out, runErr := cmd.CombinedOutput()
		cancel()
		ms := time.Since(t0).Milliseconds()

		// ProcessState is nil when the process never started (missing binary, bad Dir, etc.).
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		} else if runErr != nil {
			log.Printf("sandbox go vet failed to start: %v", runErr)
		}

		runID := uuid.New().String()
		if err := db.InsertSandboxRun(storage.SandboxRun{
			ID:         runID,
			TaskID:     taskID,
			Command:    "go vet ./" + pkg + "/...",
			ExitCode:   exitCode,
			Output:     truncate(string(out), 4096),
			DurationMs: ms,
		}); err != nil {
			log.Printf("insert sandbox run: %v", err)
		}
	}
	return time.Since(start).Milliseconds(), toolCalls
}

func changedGoPackages(diffs []parser.FileDiff) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, fd := range diffs {
		file := fd.NewPath
		if !strings.HasSuffix(file, ".go") {
			continue
		}
		// Reject absolute paths and traversal components before building go vet target.
		if filepath.IsAbs(file) || strings.Contains(file, "..") {
			log.Printf("skipping unsafe diff path: %s", file)
			continue
		}
		pkg := filepath.Dir(file)
		if _, ok := seen[pkg]; !ok {
			seen[pkg] = struct{}{}
			out = append(out, pkg)
		}
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}
