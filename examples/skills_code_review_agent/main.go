//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main is the CLI entry for the deterministic code review agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/pipeline"
)

// 定义命令行参数
var (
	flagDiffFile  = flag.String("diff-file", "", "path to unified diff file")
	flagRepoPath  = flag.String("repo-path", "", "git repository path for workspace changes")
	flagFixture   = flag.String("fixture", "", "fixture name under fixtures/ (without .diff)")
	flagDryRun    = flag.Bool("dry-run", true, "run deterministic rule-only review without LLM")
	flagDBPath    = flag.String("db-path", "reviews.db", "sqlite database path")
	flagOutputDir = flag.String("output-dir", "output", "directory for review reports")
)

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		log.Fatalf("review failed: %v", err)
	}
}

func run(ctx context.Context) error {
	// 创建输出目录
	if err := os.MkdirAll(*flagOutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	opts := pipeline.Options{
		DiffFile:  *flagDiffFile,
		RepoPath:  *flagRepoPath,
		Fixture:   *flagFixture,
		DryRun:    *flagDryRun,
		DBPath:    *flagDBPath,
		OutputDir: *flagOutputDir,
	}
	// run的核心
	result, err := pipeline.Run(ctx, opts)
	if err != nil {
		return err
	}

	fmt.Printf("Review completed\n")
	fmt.Printf("  task_id: %s\n", result.TaskID)
	fmt.Printf("  json: %s\n", filepath.Clean(result.JSONPath))
	fmt.Printf("  markdown: %s\n", filepath.Clean(result.Markdown))
	fmt.Printf("  database: %s\n", filepath.Clean(*flagDBPath))
	return nil
}
