//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a code review agent built with Agent Skills,
// governed workspace execution, persistent task records, and structured reports.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewer"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewinput"
)

var (
	mode      = flag.String("mode", "", "set to fake-model to run a registered deterministic fixture scenario")
	modelName = flag.String("model", "deepseek-v4-flash", "model name for agent")
	apiKey    = flag.String("api-key", "", "API key for the model")
	baseURL   = flag.String("base-url", "", "Base URL for the model")
	sandbox   = flag.String("sandbox", "container", "workspace execution backend: container or local")
	dbPath    = flag.String("db-path", "cr.db", "SQLite database path for review task records")
	diffFile  = flag.String("diff-file", "", "unified diff or PR patch to review")
	repoPath  = flag.String("repo-path", "", "local Git worktree used as review input or repository context")
	fixture   = flag.String("fixture", "", "named review fixture under testdata/fixtures")
	paths     = flag.String("paths", "", "comma-separated repository-relative paths that define the review scope")
	pathsFile = flag.String("paths-file", "", "file containing repository-relative review paths, one per line")
	outputDir = flag.String("output-dir", "review-output", "directory for task-scoped JSON and Markdown reports")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (retErr error) {
	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill, syscall.SIGTERM)
	defer stop()

	// Sanitizer is shared by input preparation, tool callbacks, Review Store
	// writers, and Session's final persistence hook. One rule set keeps
	// model-visible and durable representations consistent.
	sanitizer := redact.New()
	persistenceResources, err := persistence.Open(
		ctx,
		*dbPath,
		redact.AppendEventHook(sanitizer),
	)
	if err != nil {
		return fmt.Errorf("initialize persistence: %w", err)
	}

	persistenceClosed := false
	defer func() {
		if persistenceClosed {
			return
		}
		if closeErr := persistenceResources.Close(); retErr == nil && closeErr != nil {
			retErr = fmt.Errorf("close persistence resources: %w", closeErr)
		}
	}()

	// Create review agent with dependencies and configuration
	reviewAgent, err := reviewer.NewReviewer(
		reviewer.Dependencies{
			Store:           persistenceResources.ReviewStore,
			SessionService:  persistenceResources.SessionService,
			ArtifactService: persistenceResources.ArtifactService,
			Sanitizer:       sanitizer,
		},
		reviewer.Config{
			Mode: *mode,
			Model: reviewer.ModelConfig{
				Name:    *modelName,
				BaseURL: *baseURL,
				APIKey:  *apiKey,
			},
			Sandbox: reviewer.SandboxConfig{
				Backend: *sandbox,
			},
			Approval: reviewer.ApprovalConfig{
				Input:  os.Stdin,
				Output: os.Stdout,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("create review agent: %w", err)
	}

	// Run code review
	outcome, err := reviewAgent.Review(ctx, reviewinput.Spec{
		DiffFile:  *diffFile,
		RepoPath:  *repoPath,
		Fixture:   *fixture,
		Paths:     splitPaths(*paths),
		PathsFile: *pathsFile,
	})
	if err != nil {
		return fmt.Errorf("run code review (task %s): %w", outcome.TaskID, err)
	}
	jsonPath, markdownPath, err := writeReviewReports(*outputDir, outcome)
	if err != nil {
		return fmt.Errorf(
			"review completed but materializing local reports for task %s failed: %w",
			outcome.TaskID,
			err,
		)
	}
	// Do not print success until the caller-owned persistence resources are
	// closed; otherwise a close failure produces both a success banner and a
	// non-zero process exit.
	if err := persistenceResources.Close(); err != nil {
		return fmt.Errorf("close persistence resources: %w", err)
	}
	persistenceClosed = true

	fmt.Printf("Review completed\nTask ID: %s\nJSON report: %s\nMarkdown report: %s\n",
		outcome.TaskID, jsonPath, markdownPath)
	return nil
}

// writeReviewReports materializes the already-persisted Artifact bytes as one
// local pair. Temporary files and compensating removal keep the CLI view from
// advertising JSON without its matching Markdown report, or vice versa.
func writeReviewReports(outputDir string, outcome reviewer.ReviewOutcome) (jsonPath, markdownPath string, err error) {
	if strings.TrimSpace(outcome.TaskID) == "" {
		return "", "", fmt.Errorf("review outcome has no task id")
	}
	taskDir, err := filepath.Abs(filepath.Join(outputDir, outcome.TaskID))
	if err != nil {
		return "", "", fmt.Errorf("resolve report directory: %w", err)
	}
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create report directory: %w", err)
	}
	jsonPath = filepath.Join(taskDir, "review_report.json")
	markdownPath = filepath.Join(taskDir, "review_report.md")
	jsonTemp, err := writeTemporaryReport(taskDir, "review-report-json-*", outcome.JSONReport)
	if err != nil {
		return "", "", fmt.Errorf("prepare JSON report: %w", err)
	}
	defer os.Remove(jsonTemp)
	markdownTemp, err := writeTemporaryReport(taskDir, "review-report-markdown-*", outcome.MarkdownReport)
	if err != nil {
		return "", "", fmt.Errorf("prepare Markdown report: %w", err)
	}
	defer os.Remove(markdownTemp)

	// Build both files before either final name becomes visible. If the second
	// rename fails, remove the first so callers never receive a half-published
	// report pair.
	if err := os.Rename(jsonTemp, jsonPath); err != nil {
		return "", "", fmt.Errorf("publish JSON report: %w", err)
	}
	if err := os.Rename(markdownTemp, markdownPath); err != nil {
		cleanupErr := os.Remove(jsonPath)
		if cleanupErr != nil {
			return "", "", fmt.Errorf(
				"publish Markdown report: %w (remove partial JSON report: %v)",
				err,
				cleanupErr,
			)
		}
		return "", "", fmt.Errorf("publish Markdown report: %w", err)
	}
	return jsonPath, markdownPath, nil
}

func writeTemporaryReport(directory, pattern string, data []byte) (path string, retErr error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		if closeErr := file.Close(); retErr == nil && closeErr != nil {
			retErr = closeErr
		}
		if retErr != nil {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	return path, nil
}

func splitPaths(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}
