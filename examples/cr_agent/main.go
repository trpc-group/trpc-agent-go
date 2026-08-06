//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main implements the entry point for the CR (Code Review)
// agent example.
//
// The agent reads a diff, file list, or git working-tree change,
// runs static-analysis rules, optionally executes go vet / go test in
// a sandboxed environment, deduplicates findings, and writes a
// structured review report (JSON + Markdown) to disk. All review
// tasks, findings, and sandbox runs are persisted to a SQLite
// database for later querying, monitoring, and replay.
//
// Usage:
//
//	go run . --diff-file path/to/changes.patch
//	go run . --repo-path /path/to/repo --commit-range HEAD~3..HEAD
//	go run . --files file1.go,file2.go
//	go run . --fixture  # run against the built-in test fixture
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/pipeline"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/storage/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

var (
	flagDiffFile    = flag.String("diff-file", "", "path to a unified diff file to review")
	flagRepoPath    = flag.String("repo-path", "", "path to a git repository to diff")
	flagCommitRange = flag.String(
		"commit-range", "HEAD~1..HEAD",
		"git revision range to review (used with --repo-path)",
	)
	flagFiles = flag.String(
		"files", "",
		"comma-separated list of file paths to review in full",
	)
	flagFixture = flag.Bool(
		"fixture", false,
		"run against the built-in test fixture (sample_diff.patch)",
	)
	flagDBPath = flag.String(
		"db", "cr_agent.db",
		"path to the SQLite database file",
	)
	flagOutputDir = flag.String(
		"output-dir", "",
		"directory for report files (default: current directory)",
	)
	flagNoSandbox = flag.Bool(
		"no-sandbox", false,
		"disable sandbox execution (go vet, go test)",
	)
	flagTimeout = flag.Duration(
		"timeout", 60*time.Second,
		"per-command sandbox timeout",
	)
	flagConfidence = flag.Float64(
		"confidence-threshold", 0.5,
		"findings below this confidence are demoted to warnings",
	)
)

func main() {
	flag.Parse()

	input, err := resolveInput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Set up the SQLite store.
	store := sqlite.New("file:" + *flagDBPath + "?_pragma=journal_mode(WAL)")
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: init store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Configure the pipeline.
	cfg := pipeline.DefaultConfig()
	cfg.ConfidenceThreshold = *flagConfidence
	cfg.SandboxEnabled = !*flagNoSandbox
	cfg.SandboxTimeout = *flagTimeout
	if *flagOutputDir != "" {
		cfg.OutputDir = *flagOutputDir
	}
	if *flagRepoPath != "" {
		cfg.RepoPath = *flagRepoPath
	}

	p := pipeline.New(store, cfg)

	// Run the review.
	report, err := p.Run(ctx, *input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Print a summary.
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Printf("Code Review Complete\n")
	fmt.Printf("  Task ID:     %s\n", report.TaskID)
	fmt.Printf("  Status:      completed\n")
	fmt.Printf("  Findings:    %d\n", len(report.Findings))
	fmt.Printf("    Critical:  %d\n", report.Summary.Critical)
	fmt.Printf("    High:      %d\n", report.Summary.High)
	fmt.Printf("    Medium:    %d\n", report.Summary.Medium)
	fmt.Printf("    Low:       %d\n", report.Summary.Low)
	fmt.Printf("    Warning:   %d\n", report.Summary.Warning)
	fmt.Printf("  Warnings:    %d\n", len(report.Warnings))
	fmt.Printf("  Files:       %d\n", report.Summary.TotalFiles)
	fmt.Printf("  Duration:    %dms\n", report.Metrics.TotalDurationMs)
	fmt.Printf("    Sandbox:   %dms\n", report.Metrics.SandboxDurationMs)
	fmt.Printf("    Rules:     %d evaluated\n", report.Metrics.RulesEvaluated)
	fmt.Printf("    Tool calls: %d\n", report.Metrics.ToolCalls)
	fmt.Printf("    Denied:    %d\n", report.Metrics.PermissionDenials)
	fmt.Printf("  Reports:\n")
	jsonPath := filepath.Join(cfg.OutputDir, "review_report.json")
	mdPath := filepath.Join(cfg.OutputDir, "review_report.md")
	fmt.Printf("    JSON: %s\n", jsonPath)
	fmt.Printf("    MD:   %s\n", mdPath)
	fmt.Printf("  Database:    %s\n", *flagDBPath)
	fmt.Println("=" + strings.Repeat("=", 59))
}

func resolveInput() (*types.ReviewInput, error) {
	count := 0
	var input types.ReviewInput

	if *flagFixture {
		count++
		// Resolve the fixture relative to the example directory.
		fixturePath := filepath.Join("fixtures", "sample_diff.patch")
		data, err := os.ReadFile(fixturePath)
		if err != nil {
			return nil, fmt.Errorf("read fixture: %w", err)
		}
		input = types.ReviewInput{
			Type:        types.InputTypeDiff,
			DiffContent: string(data),
		}
	}

	if *flagDiffFile != "" {
		count++
		data, err := os.ReadFile(*flagDiffFile)
		if err != nil {
			return nil, fmt.Errorf("read diff file: %w", err)
		}
		input = types.ReviewInput{
			Type:        types.InputTypeDiff,
			DiffContent: string(data),
		}
	}

	if *flagRepoPath != "" {
		count++
		input = types.ReviewInput{
			Type:        types.InputTypeGit,
			RepoPath:    *flagRepoPath,
			CommitRange: *flagCommitRange,
		}
	}

	if *flagFiles != "" {
		count++
		input = types.ReviewInput{
			Type:      types.InputTypeFiles,
			FilePaths: splitCSV(*flagFiles),
		}
	}

	if count == 0 {
		flag.Usage()
		return nil, fmt.Errorf(
			"no input specified: use --diff-file, --repo-path, --files, or --fixture")
	}
	if count > 1 {
		return nil, fmt.Errorf(
			"only one input mode may be specified at a time")
	}

	return &input, nil
}

func splitCSV(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
