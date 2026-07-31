//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Command optclosedloop runs the Evaluation + Prompt Optimization closed-loop
// pipeline over a fake-deterministic (default), trace-mode, or real runner.
//
// Usage:
//
//	go run . \
//	  -mode fake_deterministic \
//	  -data-dir ./data \
//	  -output-dir ./output \
//	  -max-rounds 3 \
//	  -min-score-gain 0.05 \
//	  -seed 20250101
//
// The CLI is intentionally dependency-light on real models so it can execute
// end-to-end without API keys and produce the exact audit artifacts required
// by issue #2003: optimization_report.json, optimization_report.md, and
// detailed per-round records.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/optclosedloop/pipeline"
)

const appName = "trpc-agent-go-optclosedloop-demo"

func main() {
	var (
		modeStr            = flag.String("mode", "fake_deterministic", "Runner mode: fake_deterministic | trace_mode | real")
		dataDir            = flag.String("data-dir", "./data", "Directory containing evalset/metric/prompt configs (JSON)")
		outputDir          = flag.String("output-dir", "./output", "Directory for writing audit artifacts")
		trainSetID         = flag.String("train-set", "train", "Train eval set id")
		valSetID           = flag.String("val-set", "validation", "Validation eval set id")
		maxRounds          = flag.Int("max-rounds", 3, "Maximum optimization rounds (>=1)")
		minScoreGain       = flag.Float64("min-score-gain", 0.05, "Acceptance gate: minimum validation score gain")
		allowNewHardFail   = flag.Bool("allow-new-hard-fail", false, "Acceptance gate: allow pass->fail regressions")
		maxCostBudget      = flag.Float64("max-cost-budget", 0.0, "Acceptance gate: max $ cost per round (0 disables)")
		seed               = flag.Int64("seed", 20250101, "Deterministic random seed (fake mode + trace mode)")
		surfaces           = flag.String("surfaces", "", "Comma-separated target surfaces to optimize; empty means all")
		printPromptPatches = flag.Bool("print-patches", true, "Print per-round prompt patch diff summaries")
	)
	flag.Parse()
	logger := log.New(os.Stdout, "[optclosedloop] ", log.LstdFlags|log.Lmicroseconds)

	cfg := pipeline.Config{
		AppName:    appName,
		Mode:       pipeline.Mode(*modeStr),
		DataDir:    *dataDir,
		OutputDir:  *outputDir,
		TrainSetID: *trainSetID,
		ValSetID:   *valSetID,
		MaxRounds:  *maxRounds,
		RandomSeed: *seed,
		GateConfig: pipeline.AcceptanceGateConfig{
			MinValidationScoreGain: *minScoreGain,
			AllowNewHardFail:       *allowNewHardFail,
			MaxCostBudget:          *maxCostBudget,
		},
		PromptsBaseline:  loadBaselinePrompts(*dataDir),
		TargetSurfaceIDs: splitAndTrim(*surfaces),
	}
	if len(cfg.TargetSurfaceIDs) == 0 {
		// Default: optimize all known prompt surfaces in a round-robin order.
		cfg.TargetSurfaceIDs = []string{
			"system_prompt",
			"tool_desc_calc",
			"router_prompt",
			"agent_instruction",
		}
	}

	printBanner(logger, cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	p := pipeline.New(cfg)
	report, jsonPath, mdPath, err := p.Run(ctx)
	if err != nil {
		logger.Fatalf("pipeline.Run failed: %v", err)
	}

	printSummary(logger, report, jsonPath, mdPath, *printPromptPatches)
}

func defaultBaselinePrompts() map[string]string {
	return map[string]string{
		"system_prompt":     "# System Prompt (baseline)\nYou are a helpful general-purpose AI assistant. Answer the user's question clearly and concisely using the available tools. If you are unsure, say you don't know.",
		"agent_instruction": "# Agent Instruction (baseline)\nUse the available tools to gather evidence, then synthesize a final answer. Always cite your sources when possible.",
		"router_prompt":     "# Router Prompt (baseline)\nChoose between MathAgent, EmailAgent, or GeneralAgent. When in doubt, route to GeneralAgent.",
		"tool_desc_calc":    "# Tool: calculator (baseline)\nPerforms arithmetic. Takes a, b, op.",
	}
}

// loadBaselinePrompts loads baseline prompts from data-dir/baseline_prompts.json
// if it exists, falling back to the built-in defaults.
func loadBaselinePrompts(dataDir string) map[string]string {
	path := dataDir + "/baseline_prompts.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		return defaultBaselinePrompts()
	}
	var prompts map[string]string
	if err := json.Unmarshal(raw, &prompts); err != nil {
		logger := log.New(os.Stderr, "[optclosedloop] ", log.LstdFlags)
		logger.Printf("warning: failed to parse %s: %v; using built-in defaults", path, err)
		return defaultBaselinePrompts()
	}
	return prompts
}

func printBanner(logger *log.Logger, cfg pipeline.Config) {
	logger.Println("================================================================")
	logger.Println(" Evaluation + Prompt Optimization Closed-Loop (Issue #2003)")
	logger.Println("================================================================")
	logger.Printf("  App               : %s", cfg.AppName)
	logger.Printf("  Mode              : %s", cfg.Mode)
	logger.Printf("  Data dir          : %s", cfg.DataDir)
	logger.Printf("  Output dir        : %s", cfg.OutputDir)
	logger.Printf("  Train/Val sets    : %s / %s", cfg.TrainSetID, cfg.ValSetID)
	logger.Printf("  Max rounds        : %d", cfg.MaxRounds)
	logger.Printf("  Min score gain    : %+.4f", cfg.GateConfig.MinValidationScoreGain)
	logger.Printf("  Allow new hardfail: %t", cfg.GateConfig.AllowNewHardFail)
	logger.Printf("  Max cost budget   : $%.4f", cfg.GateConfig.MaxCostBudget)
	logger.Printf("  Seed              : %d", cfg.RandomSeed)
	logger.Printf("  Target surfaces   : %v", cfg.TargetSurfaceIDs)
	logger.Println("----------------------------------------------------------------")
}

func printSummary(logger *log.Logger, report *pipeline.OptimizationReport, jsonPath, mdPath string, printPatches bool) {
	logger.Println("================================================================")
	logger.Println(" Pipeline complete")
	logger.Println("================================================================")
	logger.Printf("  Final accepted       : %t", report.FinalAccepted)
	if report.BaselineVal != nil {
		logger.Printf("  Baseline val score   : %.4f", report.BaselineVal.OverallScore)
	} else {
		logger.Printf("  Baseline val score   : N/A (nil)")
	}
	logger.Printf("  Final val score      : %.4f", report.FinalValidationScore)
	logger.Printf("  Best val score       : %.4f (round %d)", report.BestValidationScore, report.BestRound)
	logger.Printf("  Rounds executed      : %d", len(report.Rounds))
	logger.Printf("  Total tokens (est.)  : %d", report.TotalCost.TotalTokens)
	logger.Printf("  Total cost (est.)    : $%.5f", report.TotalCost.EstimatedCostUSD)
	logger.Printf("  Wall-clock duration  : %.2fs", report.TotalCost.WallClockDuration.Seconds())
	logger.Printf("  JSON report          : %s", jsonPath)
	logger.Printf("  Markdown report      : %s", mdPath)
	logger.Println()

	if report.BaselineVal == nil {
		logger.Println("Baseline validation summary is nil; skipping per-case breakdown.")
		return
	}

	// Per-case baseline vs final table.
	logger.Println("Per-case breakdown (baseline → final accepted validation):")
	logger.Printf("  %-30s  %s → %s   %s\n", "Case", "Score", "Score", "Δ")
	byID := map[string]*pipeline.CaseEval{}
	for i := range report.BaselineVal.PerCase {
		c := &report.BaselineVal.PerCase[i]
		byID[c.EvalCaseID] = c
	}
	// Final per-case results are stored in the last round's ValCandidate if
	// the last round was accepted; otherwise fall back to the first accepted
	// round, ultimately to baseline.
	final := report.BaselineVal
	for i := range report.Rounds {
		r := &report.Rounds[i]
		if r.Acceptance != nil && r.Acceptance.Accepted && r.ValCandidate != nil {
			final = r.ValCandidate
		}
	}
	for i := range final.PerCase {
		c := &final.PerCase[i]
		base := byID[c.EvalCaseID]
		baseScore := 0.0
		if base != nil && len(base.Metrics) > 0 {
			baseScore = base.Metrics[0].Score
		}
		candScore := 0.0
		if len(c.Metrics) > 0 {
			candScore = c.Metrics[0].Score
		}
		logger.Printf("  %-30s  %.2f → %.2f   %+.2f\n",
			c.EvalCaseID, baseScore, candScore, candScore-baseScore)
	}

	if printPatches {
		logger.Println()
		logger.Println("Prompt patches:")
		for _, r := range report.Rounds {
			if r.Candidate == nil {
				continue
			}
			verdict := "REJECT"
			if r.Acceptance != nil && r.Acceptance.Accepted {
				verdict = "ACCEPT"
			}
			// Sort surface IDs for deterministic log output.
			surfaces := make([]string, 0, len(r.Candidate.Patches))
			for surface := range r.Candidate.Patches {
				surfaces = append(surfaces, surface)
			}
			sort.Strings(surfaces)
			for _, surface := range surfaces {
				logger.Printf("  round %d  %-6s  surface=%-18s  id=%s", r.Round, verdict, surface, r.Candidate.CandidateID)
			}
		}
	}
	fmt.Println()
	fmt.Println("Success. Artifacts written to:", mdPath)
}

func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := trimSpace(s[start:i])
			if part != "" {
				parts = append(parts, part)
			}
			start = i + 1
		}
	}
	return parts
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}
