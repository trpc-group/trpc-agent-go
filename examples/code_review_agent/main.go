//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "review failed: %v\n", err)
		os.Exit(1)
	}
}

func runCLI(args []string) error {
	var opts ReviewOptions
	var timeout string
	var evalLabels string
	fs := flag.NewFlagSet("code_review_agent", flag.ContinueOnError)
	fs.StringVar(&opts.DiffFile, "diff-file", "", "path to a unified diff file")
	fs.StringVar(&opts.RepoPath, "repo-path", "", "path to a git repository to review")
	fs.StringVar(&opts.FileList, "file-list", "", "comma-separated changed file list")
	fs.StringVar(&opts.Fixture, "fixture", "", "fixture name or all")
	fs.StringVar(&opts.FixtureDir, "fixture-dir", "", "fixture directory")
	fs.StringVar(&opts.OutDir, "out-dir", "code_review_agent_out", "output directory")
	fs.StringVar(&opts.DBPath, "db-path", "", "SQLite database path")
	fs.StringVar(&opts.Runtime, "runtime", "container", "sandbox runtime: container|e2b|fake|local")
	fs.BoolVar(&opts.AllowTrustedLocal, "allow-trusted-local", false, "allow local runtime")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "record allowed commands without executing them")
	fs.StringVar(&timeout, "sandbox-timeout", "30s", "sandbox command timeout")
	fs.Int64Var(&opts.OutputLimit, "output-limit", 10<<20, "sandbox output limit in bytes")
	fs.IntVar(&opts.MaxDiffLines, "max-diff-lines", defaultMaxDiffLines, "maximum diff lines before sandbox execution is skipped")
	fs.IntVar(&opts.MaxChangedFiles, "max-files", defaultMaxChangedFiles, "maximum changed files before sandbox execution is skipped")
	fs.StringVar(&opts.SkillsRoot, "skills-root", "", "skills root")
	fs.StringVar(&evalLabels, "eval-labels", "", "labeled fixture manifest for measurable recall, false positive, and redaction metrics")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var err error
	opts.SandboxTimeout, err = time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid --sandbox-timeout: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if evalLabels != "" {
		report, jsonPath, mdPath, err := RunEvaluation(ctx, opts, evalLabels)
		if err != nil {
			return err
		}
		fmt.Printf("eval: fixtures=%d recall=%.4f false_positive_rate=%.4f redaction_rate=%.4f\njson: %s\nmarkdown: %s\n",
			report.FixtureCount, report.Recall, report.FalsePositiveRate, report.RedactionRate, jsonPath, mdPath)
		return nil
	}
	if opts.Fixture == "all" {
		if err := runAllFixtures(ctx, opts); err != nil {
			return err
		}
		return nil
	}
	report, jsonPath, mdPath, err := RunReview(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Printf("task: %s\njson: %s\nmarkdown: %s\nfindings: %d\n", report.Task.ID, jsonPath, mdPath, len(report.Findings))
	return nil
}

func runAllFixtures(ctx context.Context, opts ReviewOptions) error {
	names, err := fixtureNames(opts.FixtureDir)
	if err != nil {
		return err
	}
	for _, name := range names {
		next := opts
		next.Fixture = name
		next.OutDir = filepath.Join(opts.OutDir, name)
		next.DBPath = filepath.Join(next.OutDir, "review_agent.db")
		report, jsonPath, mdPath, err := RunReview(ctx, next)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Printf("fixture=%s task=%s json=%s markdown=%s findings=%d\n", name, report.Task.ID, jsonPath, mdPath, len(report.Findings))
	}
	return nil
}
