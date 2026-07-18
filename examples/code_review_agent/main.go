//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides the command-line entry point for the code review agent example.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var cfg review.Config
	var executor string
	flag.StringVar(&cfg.TaskID, "task-id", "", "stable task id for audit and replay")
	flag.StringVar(&cfg.DiffFile, "diff-file", "", "path to a unified diff")
	flag.StringVar(&cfg.RepoPath, "repo-path", "", "git working tree to review")
	flag.StringVar(&cfg.FileList, "file-list", "", "newline-separated files relative to --repo-path")
	flag.StringVar(&cfg.Fixture, "fixture", "", "fixture name under fixtures/")
	flag.StringVar(&cfg.OutputDir, "output-dir", "output", "report output directory")
	flag.StringVar(&cfg.DatabasePath, "db", "", "SQLite database path (defaults below --output-dir)")
	flag.StringVar(&executor, "executor", string(review.ExecutorContainer), "container, e2b, local, or fake")
	flag.BoolVar(&cfg.AllowLocal, "allow-local-fallback", false, "allow unsafe local development executor")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "run complete deterministic flow without executing commands")
	flag.BoolVar(&cfg.FakeModel, "fake-model", false, "record deterministic fake-model mode")
	flag.DurationVar(&cfg.Timeout, "timeout", 45*time.Second, "per-command timeout")
	flag.IntVar(&cfg.OutputLimit, "output-limit", 64*1024, "maximum stdout/stderr bytes per command")
	flag.Parse()
	cfg.Executor = review.Executor(executor)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, paths, err := review.Run(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("task_id=%s\njson_report=%s\nmarkdown_report=%s\n", report.Task.ID, paths.JSON, paths.Markdown)
	fmt.Printf("findings=%d warnings=%d needs_human_review=%d\n", len(report.Findings), len(report.Warnings), len(report.NeedsHumanReview))
	return nil
}
