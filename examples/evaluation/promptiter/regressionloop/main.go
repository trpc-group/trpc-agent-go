//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main runs the deterministic PromptIter regression-loop example.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/regressionloop/internal/config"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/regressionloop/internal/fakeenv"
)

func main() {
	configPath := flag.String("config", "data/promptiter-recap-app/promptiter.json", "path to promptiter loop config")
	outputDir := flag.String("output", "", "override audit output directory")
	flag.Parse()
	if err := run(context.Background(), *configPath, *outputDir); err != nil {
		fmt.Fprintln(os.Stderr, "promptiter regression loop:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath, outputOverride string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	baseDir := filepath.Dir(configPath)
	prompt, err := os.ReadFile(filepath.Join(baseDir, cfg.Prompt.SourceFile))
	if err != nil {
		return fmt.Errorf("read baseline prompt: %w", err)
	}
	environment, err := fakeenv.New(ctx, baseDir, string(prompt), cfg)
	if err != nil {
		return err
	}
	defer environment.Close()
	output := filepath.Join(baseDir, cfg.Audit.OutputDir)
	if outputOverride != "" {
		output = outputOverride
	}
	writer, err := regression.NewFileArtifactWriterWithInputs(output,
		filepath.Join(baseDir, cfg.Prompt.SourceFile), filepath.Join(baseDir, cfg.Evaluation.TrainFile),
		filepath.Join(baseDir, cfg.Evaluation.ValidationFile), filepath.Join(baseDir, cfg.Evaluation.MetricsFile),
	)
	if err != nil {
		return err
	}
	report, err := regression.Run(ctx, regression.Options{
		Config: pipelineConfig(cfg), Engine: environment.Engine, Evaluator: environment.Evaluator,
		Meter: environment.Evaluator, InitialProfile: environment.InitialProfile, Artifacts: writer,
	})
	if err != nil {
		return err
	}
	fmt.Printf("report: %s\naccepted profile: %s\nrecommended for write-back: %t\n", filepath.Join(output, "optimization_report.md"), report.WriteBack.AcceptedProfileRef, report.WriteBack.RecommendedForWriteBack)
	return nil
}

func pipelineConfig(cfg *config.Config) regression.Config {
	return regression.Config{
		Seed: cfg.Seed, TrainEvalSetID: cfg.Evaluation.TrainEvalSetID,
		ValidationEvalSetID: cfg.Evaluation.ValidationEvalSetID,
		TargetSurfaceIDs:    append([]string(nil), cfg.Prompt.TargetSurfaceIDs...),
		MaxRounds:           cfg.Optimization.MaxRounds, MaxRoundsWithoutRelease: cfg.Optimization.MaxRoundsWithoutAcceptance,
		PromptIterMinScoreGain: cfg.Optimization.MinScoreGain, ReleaseGate: cfg.Gate,
		ModelConfig:   regression.ModelConfig{Mode: cfg.Mode, Name: "fake-deterministic", Config: map[string]any{"seed": cfg.Seed}},
		EstimatedCost: regression.EstimatedCost{Currency: "USD", Amount: 0, Source: "fake-model"},
		SaveArtifacts: cfg.Audit.SaveRoundArtifacts, BaselineProfileRef: "baseline/input_profile.json",
		PerformedWriteBack: false, ExpectedAgentName: cfg.Evaluation.ExpectedAgentName,
	}
}
