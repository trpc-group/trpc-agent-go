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
	"log"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

var (
	dataDirFlag   = flag.String("data-dir", "./data", "directory containing train/validation evalsets and metrics")
	configFlag    = flag.String("config", "./promptiter.json", "PromptIter regression-loop configuration")
	promptFlag    = flag.String("prompt", "./baseline_prompt.txt", "baseline prompt source file")
	outputDirFlag = flag.String("output-dir", "./output", "directory for optimization reports")
	timeoutFlag   = flag.Duration("timeout", 3*time.Minute, "maximum pipeline runtime")
)

func main() {
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()
	if err := run(ctx, *dataDirFlag, *configFlag, *promptFlag, *outputDirFlag); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dataDir, configPath, promptPath, outputDir string) error {
	config, err := regression.LoadConfig(configPath)
	if err != nil {
		return err
	}
	prompt, err := regression.LoadPrompt(promptPath)
	if err != nil {
		return err
	}
	train, err := regression.LoadEvalSet(filepath.Join(dataDir, "train.evalset.json"))
	if err != nil {
		return err
	}
	validation, err := regression.LoadEvalSet(filepath.Join(dataDir, "validation.evalset.json"))
	if err != nil {
		return err
	}
	metrics, err := regression.LoadMetrics(filepath.Join(dataDir, "metrics.json"))
	if err != nil {
		return err
	}
	evaluator, err := regression.NewLocalEvaluator(metrics, config.FakeEngine.FallbackVariant, config.Mode)
	if err != nil {
		return err
	}
	optimizer, err := regression.NewDeterministicPromptIter(*config)
	if err != nil {
		return err
	}
	pipeline, err := regression.NewPipeline(*config, evaluator, optimizer, time.Now)
	if err != nil {
		return err
	}
	report, err := pipeline.Run(ctx, prompt, train, validation)
	if err != nil {
		return err
	}
	if err := regression.WriteReports(report, outputDir); err != nil {
		return err
	}
	decision := "REJECTED"
	if report.GateDecision.Accepted {
		decision = "ACCEPTED"
	}
	fmt.Printf(
		"%s candidate %s: validation %.6f -> %.6f (%+.6f)\nreports: %s, %s\n",
		decision,
		report.CandidatePrompt.ID,
		report.Baseline.Validation.OverallScore,
		report.Candidate.Validation.OverallScore,
		report.Delta.Validation.ScoreDelta,
		filepath.Join(outputDir, regression.JSONReportName),
		filepath.Join(outputDir, regression.MarkdownReportName),
	)
	return nil
}
