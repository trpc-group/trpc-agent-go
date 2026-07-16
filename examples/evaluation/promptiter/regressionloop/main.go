//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command regressionloop runs a deterministic Evaluation + Optimization loop:
// baseline eval -> PromptIter optimization -> validation regression -> acceptance.
// With --mode=fake (default) it uses scripted models, so it runs with no API key.
// The --scenario flag selects which outcome to demonstrate.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regloop"
)

var (
	mode         = flag.String("mode", "fake", "Execution mode: fake (deterministic, no API key)")
	scenarioName = flag.String("scenario", "success", "Scenario: success | ineffective | overfit | attribution | all")
	dataDir      = flag.String("data-dir", "./data", "Directory containing eval set and metric files")
	outputDir    = flag.String("output-dir", "./output", "Directory where reports are written (per scenario)")
)

func main() {
	flag.Parse()
	if *mode != "fake" {
		log.Fatalf("unsupported mode %q: only 'fake' is implemented in this step", *mode)
	}
	names := []string{*scenarioName}
	if *scenarioName == "all" {
		names = scenarioNames()
	}
	for _, name := range names {
		sc, err := scenarioByName(name)
		if err != nil {
			log.Fatal(err)
		}
		if err := run(context.Background(), *dataDir, filepath.Join(*outputDir, sc.name), sc); err != nil {
			log.Fatalf("scenario %s: %v", sc.name, err)
		}
	}
}

func run(ctx context.Context, dataDir, outputDir string, sc scenario) error {
	cfg, err := loadLoopConfig(dataDir)
	if err != nil {
		return err
	}
	rt, err := buildRuntime(ctx, dataDir, outputDir, cfg.BaselineInstruction, sc)
	if err != nil {
		return err
	}
	defer rt.close()

	start := time.Now()
	result, err := rt.engine.Run(ctx, buildRunRequest(rt.targetSurfaceID, cfg, sc))
	if err != nil {
		return fmt.Errorf("run promptiter: %w", err)
	}
	durationMs := time.Since(start).Milliseconds()

	gate := resolveGate(cfg, sc)
	report, err := regloop.Analyze(result, regloop.Options{
		App:  appName,
		Mode: "fake",
		Gate: gate,
		Cost: regloop.CostInput{
			DurationMs: durationMs,
			ModelCalls: rt.calls.snapshot(),
		},
		Config: map[string]any{
			"mode":                 "fake",
			"scenario":             sc.name,
			"deterministic":        true,
			"randomSeed":           0,
			"randomSeedApplicable": false,
			"minScoreGain":         resolveMinScoreGain(cfg, sc),
			"gateMinTotalGain":     gate.MinTotalGain,
			"gateMaxModelCalls":    gate.MaxModelCalls,
			"maxRounds":            cfg.MaxRounds,
			"targetSurfaceIds":     []string{rt.targetSurfaceID},
			"fakeModels": map[string]any{
				"marker": marker,
				"roles":  []string{"candidate", "judge", "backwarder", "aggregator", "optimizer"},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("analyze run result: %w", err)
	}
	if err := regloop.WriteFiles(outputDir, report); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	printSummary(sc, report)
	return nil
}

func printSummary(sc scenario, report *regloop.Report) {
	fmt.Printf("==== scenario: %s ====\n", sc.name)
	fmt.Printf("  %s\n", sc.description)
	fmt.Printf("  baseline=%.3f candidate=%.3f profileAccepted=%t\n",
		report.Baseline.OverallScore, report.Candidate.OverallScore, report.Candidate.ProfileAccepted)
	fmt.Printf("  delta: newlyPassed=%d newlyFailed=%d scoreUp=%d scoreDown=%d\n",
		report.Delta.Summary.NewlyPassed, report.Delta.Summary.NewlyFailed,
		report.Delta.Summary.ScoreUp, report.Delta.Summary.ScoreDown)
	fmt.Printf("  GATE RELEASED: %t\n", report.Gate.Released)
	for _, reason := range report.Gate.Reasons {
		fmt.Printf("    - %s\n", reason)
	}
}
