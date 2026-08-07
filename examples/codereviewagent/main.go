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
	"log"
	"path/filepath"
)

func main() {
	diffFile := flag.String("diff-file", "./fixtures/command-injection.diff", "Unified diff file to review")
	repoPath := flag.String("repo-path", "", "Repository whose working-tree diff should be reviewed")
	skillsRoot := flag.String("skills-root", "./skills", "Skills directory")
	outputDir := flag.String("output-dir", "./output", "Report and audit output directory")
	dryRun := flag.Bool("dry-run", true, "Use deterministic rule-only mode without executing the sandbox probe")
	flag.Parse()
	output := normalizeOutputDir(*outputDir)
	var runner sandboxRunner = drySandbox{}
	mode := "deterministic-rule-only"
	if !*dryRun {
		runner = managedSandbox{root: sandboxRoot(output)}
		mode = "managed-sandbox"
	}
	report, err := runReview(context.Background(), pipelineConfig{
		DiffFile: *diffFile, RepoPath: *repoPath, SkillsRoot: *skillsRoot,
		OutputDir: output, Database: filepath.Join(output, "review.db"), Mode: mode, Runner: runner,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Review completed: task=%s status=%s findings=%d\n", report.TaskID, report.Status, len(report.Findings))
}
