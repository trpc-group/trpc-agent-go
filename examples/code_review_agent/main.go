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
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) (err error) {
	flags := flag.NewFlagSet("code-review-agent", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	diffFile := flags.String("diff-file", "", "path to a unified diff file")
	repoPath := flags.String("repo-path", "", "path to the git repository (for sandbox execution)")
	files := flags.String("files", "", "comma-separated repository-relative paths (with --repo-path)")
	executor := flags.String("executor", "container", "sandbox executor: container|local (local is development-only)")
	dockerfile := flags.String("dockerfile", "", "container Dockerfile directory (auto-detected by default)")
	trustedModuleCache := flags.Bool("trusted-module-cache", false, "mount the host Go module cache read-only (trusted repositories only)")
	dryRun := flags.Bool("dry-run", true, "run rules only, skip sandbox execution (default true)")
	dbPath := flags.String("db-path", "review.db", "path to the SQLite database file")
	outputDir := flags.String("output-dir", ".", "directory for report output files")
	timeoutSec := flags.Int("timeout", 30, "sandbox execution timeout in seconds")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *diffFile == "" && *repoPath == "" {
		flags.Usage()
		return errors.New("either --diff-file or --repo-path is required")
	}

	if *diffFile != "" {
		if _, err := os.Stat(*diffFile); err != nil {
			return fmt.Errorf("cannot access diff file: %w", err)
		}
	}
	if *executor != "container" && *executor != "local" {
		return errors.New("--executor must be container or local")
	}

	// Create output directory if it doesn't exist.
	if *outputDir != "." {
		if err := os.MkdirAll(*outputDir, 0o755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
	}

	// Initialize storage.
	ctx := context.Background()
	storage, err := internal.NewSQLiteStorage(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("init storage: %w", err)
	}
	defer func() {
		if closeErr := storage.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close storage: %w", closeErr))
		}
	}()

	// Configure sandbox.
	sandboxCfg := internal.DefaultSandboxConfig()
	sandboxCfg.Timeout = time.Duration(*timeoutSec) * time.Second
	sandboxCfg.TrustedModuleCache = *trustedModuleCache
	if *repoPath != "" {
		sandboxCfg.WorkDir = *repoPath
	}
	if !*dryRun && *repoPath == "" {
		return errors.New("--repo-path is required when sandbox checks are enabled")
	}

	// Create agent. Dry-run never initializes Docker, while non-dry production
	// runs default to the repository's network-disabled container executor.
	var agent *internal.ReviewAgent
	var containerSandbox *internal.ContainerSandbox
	if !*dryRun && *executor == "container" {
		dockerfilePath, resolveErr := resolveDockerfilePath(*dockerfile)
		if resolveErr != nil {
			return resolveErr
		}
		containerSandbox, err = internal.NewContainerSandbox(sandboxCfg, *repoPath, dockerfilePath)
		if err != nil {
			return fmt.Errorf("init container sandbox: %w", err)
		}
		defer func() {
			if closeErr := containerSandbox.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close container sandbox: %w", closeErr))
			}
		}()
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
	fmt.Printf("Host cache:  %t\n", *trustedModuleCache)
	fmt.Println()

	result, err := agent.Review(ctx, input)
	if err != nil {
		return fmt.Errorf("review failed: %w", err)
	}

	// Write report files.
	jsonPath := filepath.Join(*outputDir, "review_report.json")
	mdPath := filepath.Join(*outputDir, "review_report.md")

	if err := os.WriteFile(jsonPath, []byte(result.ReportJSON), 0o644); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	if err := os.WriteFile(mdPath, []byte(result.ReportMD), 0o644); err != nil {
		return fmt.Errorf("write md report: %w", err)
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
	return nil
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
