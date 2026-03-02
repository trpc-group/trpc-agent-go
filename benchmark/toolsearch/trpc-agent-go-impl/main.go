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
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
)

const (
	defaultAppName   = "toolsearch-benchmark"
	defaultEvalSetID = "toolsearch-mathtools-multiturn"
)

func main() {
	var (
		flagModel      = flag.String("model", defaultModelName(), "chat model name (e.g. deepseek-chat, gpt-4o-mini)")
		flagMode       = flag.String("mode", string(ModeLLMSearch), "toolsearch mode: none | llm | knowledge")
		flagMaxTools   = flag.Int("max-tools", 3, "max tools selected by toolsearch")
		flagEmbedModel = flag.String("embed-model", "text-embedding-3-small", "embedding model name (knowledge mode)")
		flagDataDir    = flag.String("data-dir", "../data", "base dir for evalset/metrics")
		flagOutputDir  = flag.String("output-dir", "../output", "output dir for eval results")
		flagEvalSetID  = flag.String("evalset", defaultEvalSetID, "eval set id")
		flagAppName    = flag.String("app", defaultAppName, "app name")
		flagNumRuns    = flag.Int("num-runs", 1, "evaluation runs (>=1)")
	)
	flag.Parse()

	mode, err := ParseMode(*flagMode)
	must(err, "parse -mode")
	if *flagNumRuns < 1 {
		must(fmt.Errorf("num-runs must be >= 1"), "flags")
	}

	dataDir, err := filepath.Abs(*flagDataDir)
	must(err, "abs data-dir")
	outputDir, err := filepath.Abs(*flagOutputDir)
	must(err, "abs output-dir")

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		must(err, "mkdir output-dir")
	}

	cfg := BenchmarkConfig{
		AppName:    strings.TrimSpace(*flagAppName),
		EvalSetID:  strings.TrimSpace(*flagEvalSetID),
		DataDir:    dataDir,
		OutputDir:  outputDir,
		NumRuns:    *flagNumRuns,
		ModelName:  strings.TrimSpace(*flagModel),
		Mode:       mode,
		MaxTools:   *flagMaxTools,
		EmbedModel: strings.TrimSpace(*flagEmbedModel),
	}
	must(cfg.Validate(), "config")

	log.Printf("=== toolsearch benchmark (evaluation) ===")
	log.Printf("app=%s evalset=%s mode=%s model=%s", cfg.AppName, cfg.EvalSetID, cfg.Mode, cfg.ModelName)
	log.Printf("dataDir=%s outputDir=%s", cfg.DataDir, cfg.OutputDir)

	ctx := context.Background()

	collector := NewCollector()
	baseRunner, err := NewInstrumentedRunner(cfg, collector)
	must(err, "build runner")
	defer func() {
		_ = baseRunner.Close()
	}()

	// Managers
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(cfg.DataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))
	reg := registry.New()

	agentEvaluator, err := evaluation.New(
		cfg.AppName,
		baseRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(reg),
		evaluation.WithNumRuns(cfg.NumRuns),
	)
	must(err, "create evaluation")
	defer func() { _ = agentEvaluator.Close() }()

	start := time.Now()
	result, err := agentEvaluator.Evaluate(ctx, cfg.EvalSetID)
	if err != nil {
		must(err, "evaluate")
	}
	wall := time.Since(start)
	report := BuildReport(cfg, result, collector)

	summaryPath, err := WriteSummaryFile(cfg, result, report, wall)
	must(err, "write summary file")
	log.Printf("summaryFile=%s", summaryPath)

	log.Printf("\n=== SUMMARY ===")
	log.Printf("overall=%s executionTime=%s wall=%s", result.OverallStatus, result.ExecutionTime.Round(time.Millisecond), wall.Round(time.Millisecond))
	log.Printf("tokens: chat(p=%d c=%d t=%d) toolsearch(p=%d c=%d t=%d) total(t=%d)",
		report.Chat.Prompt, report.Chat.Completion, report.Chat.Total,
		report.ToolSearch.Prompt, report.ToolSearch.Completion, report.ToolSearch.Total,
		report.Total.Total,
	)

	PrintCaseReport(result, report)
}

func defaultModelName() string {
	if v := strings.TrimSpace(os.Getenv("MODEL_NAME")); v != "" {
		return v
	}
	return "gpt-4o-mini"
}

func must(err error, msg string) {
	if err == nil {
		return
	}
	log.Fatalf("%s: %v", msg, err)
}

// PrintCaseReport prints a concise per-case/per-turn summary.
func PrintCaseReport(result *evaluation.EvaluationResult, report *Report) {
	if result == nil {
		return
	}
	for _, c := range result.EvalCases {
		if c == nil {
			continue
		}
		log.Printf("\n--- case=%s overall=%s executionTime=%s ---", c.EvalCaseID, c.OverallStatus, result.ExecutionTime.Round(time.Millisecond))
		if len(c.EvalCaseResults) == 0 {
			continue
		}
		// Use run 1 details.
		r := c.EvalCaseResults[0]
		if r == nil {
			continue
		}
		log.Printf("run=%d status=%s sessionID=%s", r.RunID, r.FinalEvalStatus, r.SessionID)
		// Prefer per-invocation details (contains actual/expected invocations).
		for _, invRes := range r.EvalMetricResultPerInvocation {
			if invRes == nil {
				continue
			}
			turnID := ""
			expected := ""
			actualTools := ""
			if invRes.ExpectedInvocation != nil {
				turnID = invRes.ExpectedInvocation.InvocationID
				expected = FirstToolName(invRes.ExpectedInvocation)
			}
			if invRes.ActualInvocation != nil {
				actualTools = strings.Join(ToolNames(invRes.ActualInvocation), ",")
			}

			turnStats := report.LookupTurn(r.SessionID, turnID)
			metricScore := firstMetricScore(invRes)

			log.Printf("turn=%s metric=%.2f expected=%s actual=%s duration=%s chatTokens=%d toolsearchTokens=%d",
				turnID,
				metricScore,
				expected,
				actualTools,
				turnStats.Duration.Round(time.Millisecond),
				turnStats.Chat.Total,
				turnStats.ToolSearch.Total,
			)
		}
	}
}

func firstMetricScore(invRes *evalresult.EvalMetricResultPerInvocation) float64 {
	if invRes == nil {
		return 0
	}
	if len(invRes.EvalMetricResults) == 0 || invRes.EvalMetricResults[0] == nil {
		return 0
	}
	return invRes.EvalMetricResults[0].Score
}
