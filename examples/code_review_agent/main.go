//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"flag"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/app"
)

type stringListFlag []string

func (f *stringListFlag) String() string { return fmt.Sprint([]string(*f)) }

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func main() {
	var cfg app.Config
	flag.StringVar(&cfg.DiffFile, "diff-file", "", "unified diff or PR patch file")
	flag.StringVar(&cfg.RepoPath, "repo-path", "", "repository path")
	flag.StringVar(&cfg.Fixture, "fixture", "", "bundled fixture name")
	flag.StringVar(&cfg.Runtime, "runtime", app.RuntimeContainer, "runtime: container, fake, or local")
	flag.StringVar(&cfg.Mode, "mode", app.ModeRuleOnly, "mode: rule-only or agent")
	flag.StringVar(&cfg.OutDir, "out", "out", "output directory")
	flag.StringVar(&cfg.DBPath, "db", "", "SQLite audit database path")
	flag.BoolVar(&cfg.AllowLocal, "allow-local", false, "allow local development runtime")
	flag.StringVar(&cfg.ShowTask, "show-task", "", "load a task from the audit database")
	flag.StringVar(&cfg.TaskID, "task-id", "", "override task id for deterministic fixtures")
	flag.Var((*stringListFlag)(&cfg.Files), "file", "repository-relative file to review; repeatable")
	flag.StringVar(&cfg.FileList, "file-list", "", "newline-delimited repository-relative files to review")
	flag.Parse()
	dto, err := app.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "code review failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("task_id=%s status=%s findings=%d human_review=%d\n", dto.TaskID, dto.Status, len(dto.Findings), len(dto.NeedsHumanReview))
}
