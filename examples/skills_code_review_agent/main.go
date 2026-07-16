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
	"io"
	"log"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/review"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		log.Fatalf("code review failed: %v", err)
	}
}

func run(args []string, out io.Writer) error {
	var cfg review.ReviewConfig
	fs := flag.NewFlagSet("skills_code_review_agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.DiffFile, "diff-file", "", "Path to a unified diff or PR patch file")
	fs.StringVar(&cfg.RepoPath, "repo-path", "", "Path to a git repository to diff and run Go checks against")
	fs.StringVar(&cfg.FileList, "file-list", "", "Path to a newline-delimited changed file list")
	fs.StringVar(&cfg.Fixture, "fixture", "", "Fixture name under fixtures without .diff")
	fs.BoolVar(&cfg.ContainerSmoke, "container-smoke", false, "Generate a temporary no-dependency Go repo and run a container success smoke review")
	fs.StringVar(&cfg.OutputDir, "output-dir", "output", "Directory for review_report.json and review_report.md")
	fs.StringVar(&cfg.DBPath, "db", "", "SQLite database path; defaults to output-dir/reviews.sqlite")
	fs.StringVar(&cfg.Executor, "executor", "container", "Sandbox executor: container|e2b|local|fake|fake-fail")
	fs.StringVar(&cfg.ContainerBaseImage, "container-base-image", "", "Override container sandbox base image, for example a regional mirror of golang:1.23-bookworm")
	fs.BoolVar(&cfg.InstallStaticcheck, "container-install-staticcheck", false, "Install staticcheck into the container image during sandbox image build")
	fs.BoolVar(&cfg.AllowLocalFallback, "allow-local-fallback", false, "Allow local executor for development fallback")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Run deterministic rule-only flow and skip real sandbox execution")
	fs.BoolVar(&cfg.RuleOnly, "rule-only", true, "Use deterministic rules instead of a real model")
	fs.BoolVar(&cfg.FakeModel, "fake-model", false, "Run the llmagent + code-review Skill path with a deterministic fake model")
	fs.StringVar(&cfg.ModelProvider, "model-provider", "", "Model provider: fake|openai|openai-compatible|http|deepseek")
	fs.StringVar(&cfg.Model, "model", "gpt-4o-mini", "OpenAI model name used when --rule-only=false")
	fs.StringVar(&cfg.ModelBaseURL, "model-base-url", "", "OpenAI-compatible model base URL for http/openai-compatible providers")
	fs.DurationVar(&cfg.Timeout, "timeout", 30*time.Second, "Per-command sandbox timeout")
	fs.IntVar(&cfg.OutputLimitBytes, "output-limit-bytes", 64*1024, "Maximum stdout/stderr bytes stored per stream")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cfg.ModelProvider == "fake" && !cfg.RuleOnly {
		cfg.FakeModel = true
	}
	cfg.LLMReview = !cfg.RuleOnly

	report, jsonPath, mdPath, err := review.RunReview(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "task_id=%s\n", report.Task.ID)
	fmt.Fprintf(out, "json_report=%s\n", jsonPath)
	fmt.Fprintf(out, "markdown_report=%s\n", mdPath)
	fmt.Fprintf(out, "findings=%d warnings=%d needs_human_review=%d\n",
		len(report.Findings), len(report.Warnings), len(report.NeedsHumanReview))
	return nil
}
