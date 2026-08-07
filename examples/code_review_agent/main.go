//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command code_review_agent runs automated Go code reviews against
// unified diffs or repository paths, with sandbox execution and
// SQLite persistence.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/app"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
)

func main() {
	diffFile := flag.String("diff-file", "", "Path to a unified diff file")
	repoPath := flag.String("repo-path", "", "Path to a Go repository")
	dryRun := flag.Bool("dry-run", false, "Run without real model or sandbox execution")
	sandboxType := flag.String("sandbox", "container", "Sandbox type: container (default), local (dev fallback only), fake")
	dbPath := flag.String("db", "review.db", "SQLite database path")
	outputJSON := flag.String("output-json", "review_report.json", "JSON report output path")
	outputMD := flag.String("output-md", "review_report.md", "Markdown report output path")
	flag.Parse()

	if *diffFile == "" && *repoPath == "" {
		fmt.Println("Usage: code_review_agent --diff-file <path> [flags]")
		fmt.Println("       code_review_agent --repo-path <path> [flags]")
		flag.PrintDefaults()
		return
	}

	var rt sandbox.RuntimeType
	switch *sandboxType {
	case "container":
		rt = sandbox.RuntimeContainer
	case "fake":
		rt = sandbox.RuntimeFake
	default:
		rt = sandbox.RuntimeLocal
	}

	if *dryRun {
		rt = sandbox.RuntimeFake
	}

	cfg := app.Config{
		DiffFile:        *diffFile,
		RepoPath:        *repoPath,
		DryRun:          *dryRun,
		SandboxType:     rt,
		DBPath:          *dbPath,
		OutputJSON:      *outputJSON,
		OutputMD:        *outputMD,
		AllowedCommands: governance.DefaultAllowedCommands(),
		DeniedCommands:  governance.DefaultDeniedCommands(),
	}

	ctx := context.Background()
	if err := app.Run(ctx, cfg); err != nil {
		log.Fatalf("review failed: %v", err)
	}
}
