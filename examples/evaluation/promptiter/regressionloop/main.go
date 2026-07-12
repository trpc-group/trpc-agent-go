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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	regressionloop "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

var (
	configPath = flag.String("config", "./data/eval-optimization-regression-app/promptiter.json", "PromptIter regression-loop config")
	outputDir  = flag.String("output-dir", "./output", "Directory for optimization reports")
	mode       = flag.String("mode", "fake-engine", "Runner mode: fake-engine, trace-fake-engine, scripted, or trace-smoke")
	scenario   = flag.String("scenario", "", "Scenario: success, ineffective, overfit, or all")
)

func main() {
	flag.Parse()
	resolvedConfigPath := resolveConfigPath(*configPath)
	cfg, err := loadConfig(resolvedConfigPath, *outputDir)
	if err != nil {
		log.Fatal(err)
	}
	scenarios := selectedScenarios(cfg.Scenario, *scenario, *mode)
	if strings.EqualFold(strings.TrimSpace(*scenario), "all") {
		scenarios = []string{"success", "ineffective", "overfit"}
	}
	for _, scenarioName := range scenarios {
		runCfg := cfg
		runCfg.Scenario = scenarioName
		outDir := *outputDir
		if len(scenarios) > 1 {
			outDir = filepath.Join(*outputDir, scenarioName)
		}
		runCfg.OutputJSON = filepath.Join(outDir, "optimization_report.json")
		runCfg.OutputMarkdown = filepath.Join(outDir, "optimization_report.md")
		result, err := runPipeline(context.Background(), runCfg, filepath.Dir(resolvedConfigPath), *mode)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("scenario: %s\n", scenarioName)
		fmt.Printf("optimization report: %s\n", result.JSONPath)
		fmt.Printf("markdown report: %s\n", result.MarkdownPath)
		fmt.Printf("accepted: %t\n", result.Report.GateDecision.Accepted)
		for _, reason := range result.Report.GateDecision.Reasons {
			fmt.Printf("- %s\n", reason)
		}
	}
}

type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time {
	current := c.now
	c.now = c.now.Add(150 * time.Millisecond)
	return current
}

func loadConfig(path, outDir string) (regressionloop.Config, error) {
	path = resolveConfigPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return regressionloop.Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg regressionloop.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return regressionloop.Config{}, fmt.Errorf("decode config: %w", err)
	}
	baseDir := filepath.Dir(path)
	if !filepath.IsAbs(cfg.PromptSource) {
		cfg.PromptSource = filepath.Join(baseDir, cfg.PromptSource)
	}
	if !filepath.IsAbs(cfg.MetricsPath) {
		cfg.MetricsPath = filepath.Join(baseDir, cfg.MetricsPath)
	}
	cfg.OutputJSON = filepath.Join(outDir, "optimization_report.json")
	cfg.OutputMarkdown = filepath.Join(outDir, "optimization_report.md")
	return cfg, nil
}

func resolveConfigPath(path string) string {
	if filepath.IsAbs(path) || fileExists(path) {
		return path
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return path
	}
	candidate := filepath.Join(filepath.Dir(sourceFile), path)
	if fileExists(candidate) {
		return candidate
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runPipeline(
	ctx context.Context,
	cfg regressionloop.Config,
	baseDir string,
	mode string,
) (*regressionloop.Result, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if cfg.FakeConfig == nil {
		cfg.FakeConfig = map[string]string{}
	}
	cfg.FakeConfig["scenario"] = scenarioDescription(cfg.Scenario)
	if mode == "trace-fake-engine" {
		cfg.FakeConfig["runner"] = "deterministic-trace-fake-engine"
		cfg.FakeConfig["optimization"] = "complete: PromptIter fake engine with trace-bearing evaluations"
	}
	if mode == "trace-smoke" {
		cfg.Scenario = "trace-smoke"
		cfg.FakeConfig["scenario"] = scenarioDescription(cfg.Scenario)
		cfg.FakeConfig["runner"] = "deterministic-trace-replay"
		cfg.FakeConfig["optimization"] = "skipped: trace-smoke replays traces and audits reporting only"
	}
	evaluator := fakeEvaluator{
		baseDir:  baseDir,
		appName:  cfg.AppName,
		scenario: cfg.Scenario,
		trace:    true,
	}
	var iterator regressionloop.PromptIterator
	switch mode {
	case "", "fake-engine", "trace-fake-engine":
		built, err := newEnginePromptIterator(ctx, baseDir, cfg)
		if err != nil {
			return nil, err
		}
		iterator = built
	case "scripted":
		iterator = fakePromptIterator{
			baseDir:     baseDir,
			appName:     cfg.AppName,
			scenario:    cfg.Scenario,
			metricsPath: cfg.MetricsPath,
		}
	case "trace-smoke":
		iterator = traceSmokePromptIterator{
			baseDir:     baseDir,
			appName:     cfg.AppName,
			metricsPath: cfg.MetricsPath,
		}
	default:
		return nil, fmt.Errorf("unsupported mode %q", mode)
	}
	pipeline := regressionloop.Pipeline{
		Evaluator:      evaluator,
		PromptIterator: iterator,
		Clock:          &fixedClock{now: time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)},
	}
	return pipeline.Run(ctx, cfg)
}

func selectedScenarios(configScenario, flagScenario, mode string) []string {
	if strings.EqualFold(strings.TrimSpace(mode), "trace-smoke") && strings.TrimSpace(flagScenario) == "" {
		return []string{"trace-smoke"}
	}
	scenarioName := scenarioOrDefault(configScenario)
	if strings.TrimSpace(flagScenario) != "" {
		scenarioName = scenarioOrDefault(flagScenario)
	}
	return []string{scenarioName}
}

func scenarioDescription(scenario string) string {
	switch scenarioOrDefault(scenario) {
	case "success":
		return "validation improves from 0.333 to 1.000 without newly failed metrics"
	case "ineffective":
		return "train changes but validation has no score gain"
	case "trace-smoke":
		return "trace replay audits reporting without optimization"
	default:
		return "train improves but validation adds a hard fail"
	}
}
