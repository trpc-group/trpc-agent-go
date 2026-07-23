//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is the CLI entrypoint for the code review agent example.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/assist"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/orchestrator"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// main is the CLI entrypoint for the code review agent example.
func main() {
	var (
		diffFile     = flag.String("diff-file", "", "path to a unified diff / patch")
		repoPath     = flag.String("repo-path", "", "git repository path")
		files        = flag.String("files", "", "comma-separated file paths, or @listfile")
		fixture      = flag.String("fixture", "", "fixture name under testdata/fixtures")
		executor     = flag.String("executor", "container", "sandbox executor: container|e2b|local|fake")
		mode         = flag.String("mode", review.ModeRuleOnly, "rule-only|dry-run|llm")
		llmBackend   = flag.String("llm", assist.LLMFake, "llm backend when --mode=llm: fake|openai|auto")
		modelName    = flag.String("model", "gpt-4o-mini", "model name for --llm=openai/auto")
		baseURL      = flag.String("base-url", "", "OpenAI-compatible base URL (default: OPENAI_BASE_URL)")
		modelVariant = flag.String("model-variant", "", "optional variant: openai|deepseek|...")
		dbPath       = flag.String("db", "", "sqlite db path (default: <out>/review.db)")
		outDir       = flag.String("out", "./out", "output directory")
		skillsRoot   = flag.String("skills-root", "skills", "skills root directory")
		fixtures     = flag.String("fixtures-root", "testdata/fixtures", "fixtures root")
		threshold    = flag.Float64("confidence-threshold", 0.75, "finding confidence threshold")
		enableTest   = flag.Bool("enable-go-test", false, "also schedule go vet in sandbox plan")
		enableStatic = flag.Bool("enable-staticcheck", false, "schedule staticcheck script when available")
		allowFB      = flag.Bool("allow-local-fallback", false, "if container/e2b unavailable, fall back to local (off by default)")
	)
	flag.Parse()

	if *diffFile == "" && *repoPath == "" && *fixture == "" && *files == "" {
		fmt.Fprintln(os.Stderr, "error: one of --diff-file, --repo-path, --files, or --fixture is required")
		flag.Usage()
		os.Exit(2)
	}

	cfg := orchestrator.Config{
		Mode:                *mode,
		Executor:            *executor,
		DiffFile:            *diffFile,
		RepoPath:            *repoPath,
		Files:               *files,
		Fixture:             *fixture,
		FixturesRoot:        *fixtures,
		SkillsRoot:          *skillsRoot,
		DBPath:              *dbPath,
		OutDir:              *outDir,
		ConfidenceThreshold: *threshold,
		EnableGoTest:        *enableTest,
		EnableStaticcheck:   *enableStatic,
		AllowLocalFallback:  *allowFB,
	}

	timeout := 2 * time.Minute
	if strings.EqualFold(*mode, review.ModeLLM) {
		mdl, backend, err := assist.ResolveModel(*llmBackend, assist.OpenAIModelOptions{
			Model:   *modelName,
			BaseURL: *baseURL,
			Variant: *modelVariant,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "llm model: %v\n", err)
			os.Exit(2)
		}
		cfg.Model = mdl
		if backend == assist.LLMOpenAI {
			timeout = 5 * time.Minute
		}
		fmt.Fprintf(os.Stderr, "llm_backend=%s model=%s\n", backend, mdl.Info().Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	res, err := orchestrator.Run(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "review failed: %v\n", err)
		os.Exit(1)
	}

	absJSON, _ := filepath.Abs(res.JSONPath)
	absMD, _ := filepath.Abs(res.MarkdownPath)
	absDB, _ := filepath.Abs(res.DBPath)
	fmt.Printf("task_id=%s status=%s findings=%d warnings=%d\n",
		res.TaskID, res.Report.Status, len(res.Report.Findings), len(res.Report.Warnings))
	fmt.Printf("report_json=%s\n", absJSON)
	fmt.Printf("report_md=%s\n", absMD)
	fmt.Printf("db=%s\n", absDB)
	if res.Report.Governance.ExecutorFallback != "" {
		fmt.Printf("executor_fallback=%s\n", res.Report.Governance.ExecutorFallback)
	}
	if res.Report.Governance.AgentAssistNote != "" {
		fmt.Printf("agent_assist=%s\n", res.Report.Governance.AgentAssistNote)
	}
	fmt.Printf("conclusion=%s\n", res.Report.Conclusion)
}
