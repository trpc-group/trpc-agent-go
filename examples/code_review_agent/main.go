//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/orchestrator"
)

func main() {
	var (
		fixtureDir = flag.String("fixture-dir", "testdata/fixtures", "directory containing diff fixtures")
		diffFile   = flag.String("diff-file", "", "git or raw unified diff file to review")
		repoPath   = flag.String("repo-path", "", "repository path whose git workspace diff should be reviewed")
		fileList   = flag.String("file-list", "", "newline-delimited changed file path list")
		outDir     = flag.String("out-dir", "./out", "directory for reports and the default SQLite database")
		dbPath     = flag.String("db-path", "", "SQLite database path; defaults to <out-dir>/review_agent.db")
		modelName  = flag.String("model", os.Getenv("MODEL"), "OpenAI-compatible model name")
		runtime    = flag.String("runtime", "container", "workspace runtime: container, e2b, local, or fake")
		timeout    = flag.Duration("sandbox-timeout", 30*time.Second, "per-command sandbox timeout")
	)
	flag.Parse()

	if *dbPath == "" {
		*dbPath = filepath.Join(*outDir, "review_agent.db")
	}
	result, err := orchestrator.Run(context.Background(), orchestrator.Options{
		FixtureDir:     *fixtureDir,
		DiffFile:       *diffFile,
		RepoPath:       *repoPath,
		FileList:       *fileList,
		OutDir:         *outDir,
		DBPath:         *dbPath,
		Model:          *modelName,
		Runtime:        *runtime,
		SandboxTimeout: *timeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "run review: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Code review completed\n")
	fmt.Printf("- task:        %s\n", result.TaskID)
	fmt.Printf("- conclusion:  %s\n", result.Report.Conclusion)
	fmt.Printf("- findings:    %d\n", len(result.Report.Findings))
	fmt.Printf("- json report: %s\n", result.JSONPath)
	fmt.Printf("- md report:   %s\n", result.MarkdownPath)
	fmt.Printf("- store:       %s\n", result.DBPath)
}
