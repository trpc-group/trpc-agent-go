//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("promptiter_regression_loop", flag.ContinueOnError)
	configPath := flags.String("config", "", "Path to promptiter regression loop config")
	outputDir := flags.String("output-dir", "", "Directory for optimization reports")
	seed := flags.Int64("seed", 0, "Deterministic seed override")
	deterministic := flags.Bool("deterministic", true, "Enable deterministic fake/trace mode")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := regressionloop.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if *outputDir != "" {
		cfg.Output.Dir = *outputDir
		cfg.Output.JSONReport = joinOutput(*outputDir, "optimization_report.json")
		cfg.Output.MarkdownReport = joinOutput(*outputDir, "optimization_report.md")
	}
	if *seed != 0 {
		cfg.Seed = *seed
	}
	cfg.Runner.Deterministic = *deterministic
	pipeline := &regressionloop.Pipeline{
		Evaluator: newFakeEvaluator(),
		Optimizer: fakeOptimizer{},
	}
	result, err := pipeline.Run(ctx, *cfg)
	if err != nil {
		return err
	}
	report := result.Report
	bestScore := 0.0
	if report.SelectedCandidate != nil {
		bestScore = report.SelectedCandidate.Validation.Score
	} else if len(report.Candidates) > 0 {
		bestScore = report.Candidates[0].Validation.Score
	}
	fmt.Printf("Baseline validation score: %.2f\n", report.Baseline.Validation.Score)
	fmt.Printf("Best candidate validation score: %.2f\n", bestScore)
	fmt.Printf("Gate accepted: %t\n", report.GateDecision.Accepted)
	fmt.Printf("JSON report: %s\n", result.JSONPath)
	fmt.Printf("Markdown report: %s\n", result.MarkdownPath)
	return nil
}

func joinOutput(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + string(os.PathSeparator) + name
}
