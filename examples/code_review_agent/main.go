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

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/orchestrator"
)

func main() {
	var (
		fixtureDir = flag.String("fixture-dir", "testdata/fixtures", "directory containing diff fixtures")
		outDir     = flag.String("out-dir", "./out", "directory for reports and the default SQLite database")
		dbPath     = flag.String("db-path", "", "SQLite database path; defaults to <out-dir>/review_agent.db")
		modelName  = flag.String("model", os.Getenv("MODEL"), "OpenAI-compatible model name")
		runtime    = flag.String("runtime", "container", "workspace runtime: container, e2b, local, or fake")
	)
	flag.Parse()

	if *dbPath == "" {
		*dbPath = filepath.Join(*outDir, "review_agent.db")
	}
	if *modelName == "" {
		fmt.Fprintln(os.Stderr, "warning: no model configured; deterministic fixture review will run without model calls")
	}
	result, err := orchestrator.Run(context.Background(), orchestrator.Options{
		FixtureDir: *fixtureDir,
		OutDir:     *outDir,
		DBPath:     *dbPath,
		Model:      *modelName,
		Runtime:    *runtime,
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
