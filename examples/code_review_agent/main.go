//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main implements a CLI entry point for the code review agent.
// It reads a git diff, runs rules and optional sandbox checks, stores
// results in SQLite, and outputs JSON + Markdown reports.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal"
)

func main() {
	diffFile := flag.String("diff-file", "", "path to a unified diff file")
	repoPath := flag.String("repo-path", "", "path to the git repository (for sandbox execution)")
	files := flag.String("files", "", "comma-separated repository-relative paths (with --repo-path)")
	executor := flag.String("executor", "container", "sandbox executor: container|local (local is development-only)")
	dockerfile := flag.String("dockerfile", "", "container Dockerfile directory (auto-detected by default)")
	dryRun := flag.Bool("dry-run", true, "run rules only, skip sandbox execution (default true)")
	dbPath := flag.String("db-path", "review.db", "path to the SQLite database file")
	outputDir := flag.String("output-dir", ".", "directory for report output files")
	timeoutSec := flag.Int("timeout", 30, "sandbox execution timeout in seconds")
	flag.Parse()

	if *diffFile == "" && *repoPath == "" {
		fmt.Fprintln(os.Stderr, "Error: either --diff-file or --repo-path is required")
		flag.Usage()
		os.Exit(1)
	}

	if *diffFile != "" {
		if _, err := os.Stat(*diffFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot access diff file: %v\n", err)
			os.Exit(1)
		}
	}
	if *executor != "container" && *executor != "local" {
		fmt.Fprintln(os.Stderr, "Error: --executor must be container or local")
		os.Exit(1)
	}

	// Create output directory if it doesn't exist.
	if *outputDir != "." {
		if err := os.MkdirAll(*outputDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Error: create output dir: %v\n", err)
			os.Exit(1)
		}
	}

	// Initialize storage.
	ctx := context.Background()
	storage, err := internal.NewSQLiteStorage(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: init storage: %v\n", err)
		os.Exit(1)
	}
	defer storage.Close()

	// Configure sandbox.
	sandboxCfg := internal.DefaultSandboxConfig()
	sandboxCfg.Timeout = time.Duration(*timeoutSec) * time.Second
	if *repoPath != "" {
		sandboxCfg.WorkDir = *repoPath
	}
	if !*dryRun && *repoPath == "" {
		fmt.Fprintln(os.Stderr, "Error: --repo-path is required when sandbox checks are enabled")
		os.Exit(1)
	}

	// Create agent. Dry-run never initializes Docker, while non-dry production
	// runs default to the repository's network-disabled container executor.
	var agent *internal.ReviewAgent
	var containerSandbox *internal.ContainerSandbox
	if !*dryRun && *executor == "container" {
		dockerfilePath, resolveErr := resolveDockerfilePath(*dockerfile)
		if resolveErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", resolveErr)
			os.Exit(1)
		}
		containerSandbox, err = internal.NewContainerSandbox(sandboxCfg, *repoPath, dockerfilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: init container sandbox: %v\n", err)
			os.Exit(1)
		}
		defer containerSandbox.Close()
		agent = internal.NewReviewAgentWithSandbox(storage, containerSandbox)
	} else {
		agent = internal.NewReviewAgentWithConfig(storage, sandboxCfg)
	}

	// Run review.
	input := internal.ReviewInput{
		DiffFile: *diffFile,
		RepoPath: *repoPath,
		DryRun:   *dryRun,
	}
	if strings.TrimSpace(*files) != "" {
		for _, name := range strings.Split(*files, ",") {
			if name = strings.TrimSpace(name); name != "" {
				input.FilePaths = append(input.FilePaths, name)
			}
		}
	}

	fmt.Printf("Code Review Agent\n")
	fmt.Printf("=================\n")
	fmt.Printf("Diff file:   %s\n", *diffFile)
	fmt.Printf("Repo path:   %s\n", *repoPath)
	fmt.Printf("Executor:    %s\n", *executor)
	fmt.Printf("Dry run:     %t\n", *dryRun)
	fmt.Printf("Database:    %s\n", *dbPath)
	fmt.Printf("Output dir:  %s\n", *outputDir)
	fmt.Printf("Timeout:     %ds\n", *timeoutSec)
	fmt.Println()

	result, err := agent.Review(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: review failed: %v\n", err)
		os.Exit(1)
	}

	// Write report files.
	jsonPath := filepath.Join(*outputDir, "review_report.json")
	mdPath := filepath.Join(*outputDir, "review_report.md")

	if err := os.WriteFile(jsonPath, []byte(result.ReportJSON), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write json report: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(mdPath, []byte(result.ReportMD), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write md report: %v\n", err)
		os.Exit(1)
	}

	// Print summary.
	fmt.Printf("Review Complete\n")
	fmt.Printf("----------------\n")
	fmt.Printf("Task ID:       %s\n", result.TaskID)
	fmt.Printf("Status:        %s\n", result.Task.Status)
	fmt.Printf("Findings:      %d\n", len(result.Findings))
	if result.Monitoring != nil {
		fmt.Printf("  Critical:    %d\n", result.Monitoring.SeverityCounts[internal.SeverityCritical])
		fmt.Printf("  High:        %d\n", result.Monitoring.SeverityCounts[internal.SeverityHigh])
		fmt.Printf("  Medium:      %d\n", result.Monitoring.SeverityCounts[internal.SeverityMedium])
		fmt.Printf("  Low:         %d\n", result.Monitoring.SeverityCounts[internal.SeverityLow])
	}
	fmt.Printf("Warnings:      %d\n", len(result.Warnings))
	fmt.Printf("Sandbox runs:  %d\n", len(result.SandboxRuns))
	fmt.Printf("Perm blocks:   %d\n", len(result.PermissionRecords))
	fmt.Printf("Duration:      %dms\n", result.Monitoring.TotalDurationMs)
	fmt.Println()
	fmt.Printf("Reports written:\n")
	fmt.Printf("  %s\n", jsonPath)
	fmt.Printf("  %s\n", mdPath)
	fmt.Printf("Database: %s\n", *dbPath)
}

func resolveDockerfilePath(configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(filepath.Join(configured, "Dockerfile")); err != nil {
			return "", fmt.Errorf("invalid --dockerfile directory: %w", err)
		}
		return configured, nil
	}
	for _, candidate := range []string{
		filepath.Join("skills", "code-review", "sandbox"),
		filepath.Join("examples", "code_review_agent", "skills", "code-review", "sandbox"),
	} {
		if _, err := os.Stat(filepath.Join(candidate, "Dockerfile")); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot locate code-review sandbox Dockerfile; use --dockerfile")
}
