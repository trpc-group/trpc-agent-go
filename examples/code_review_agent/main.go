package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/agent"
)

func main() {
	var cfg agent.Config
	var fixture string
	var filesCSV string
	var timeout time.Duration

	flag.StringVar(&cfg.DiffFile, "diff-file", "", "path to a unified diff or PR patch")
	flag.StringVar(&cfg.RepoPath, "repo-path", "", "path to a git working tree to review")
	flag.StringVar(&filesCSV, "files", "", "comma-separated changed file paths when no diff is available")
	flag.StringVar(&fixture, "fixture", "", "test fixture name from testdata/fixtures without .diff")
	flag.StringVar(&cfg.SkillPath, "skill-path", filepath.Join("skills", "code-review"), "path to the code-review skill")
	flag.StringVar(&cfg.Runtime, "runtime", "fake", "sandbox runtime: fake, container, e2b, local")
	flag.StringVar(&cfg.OutDir, "out-dir", "out", "directory for review_report.json and review_report.md")
	flag.StringVar(&cfg.StorePath, "store-path", filepath.Join("out", "review_store.json"), "persistent store path")
	flag.BoolVar(&cfg.DryRun, "dry-run", true, "use deterministic rule-only execution without a real model")
	flag.BoolVar(&cfg.RuleOnly, "rule-only", true, "disable model calls and only run deterministic rules")
	flag.BoolVar(&cfg.EnableStaticcheck, "staticcheck", false, "request staticcheck in the sandbox policy")
	flag.Int64Var(&cfg.MaxOutputBytes, "max-output-bytes", 64*1024, "maximum captured sandbox output bytes")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "sandbox command timeout")
	flag.BoolVar(&cfg.ForceSandboxFailure, "force-sandbox-failure", false, "testing hook: make the sandbox return a failure")
	flag.Parse()

	cfg.Timeout = timeout
	if fixture != "" {
		cfg.DiffFile = filepath.Join("testdata", "fixtures", fixture+".diff")
	}
	if filesCSV != "" {
		cfg.Files = agent.SplitCSV(filesCSV)
	}

	if cfg.DiffFile == "" && cfg.RepoPath == "" && len(cfg.Files) == 0 {
		fmt.Fprintln(os.Stderr, "one of --diff-file, --repo-path, --files, or --fixture is required")
		os.Exit(2)
	}

	reviewer := agent.NewReviewer(cfg)
	report, err := reviewer.Run(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "review failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("task_id=%s\n", report.Task.ID)
	fmt.Printf("json=%s\n", report.ReportJSONPath)
	fmt.Printf("markdown=%s\n", report.ReportMarkdownPath)
}
