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
	"log"
	"os"
	"strings"
)

var (
	dataDir                    = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir                  = flag.String("output-dir", "./output", "Directory where evaluation results and the optimization report are stored")
	modelName                  = flag.String("model", "deepseek-v4-flash", "Model identifier used by the candidate sports recap agent")
	candidateInstruction       = flag.String("candidate-instruction", defaultCandidateInstruction, "Instruction used by the candidate agent")
	judgeModelName             = flag.String("judge-model", "deepseek-v4-flash", "Model identifier used by the judge agent")
	workerModelName            = flag.String("worker-model", "deepseek-v4-flash", "Model identifier used by the PromptIter worker agents")
	maxRounds                  = flag.Int("max-rounds", 4, "Maximum PromptIter optimization rounds")
	evalCaseParallelism        = flag.Int("eval-case-parallelism", 16, "Maximum number of eval cases processed in parallel")
	backwardCaseParallelism    = flag.Int("backward-case-parallelism", 16, "Maximum number of train eval cases processed in parallel during backward; 0 uses GOMAXPROCS")
	aggregationParallelism     = flag.Int("aggregation-parallelism", 16, "Maximum number of target surfaces aggregated in parallel; 0 uses GOMAXPROCS")
	optimizerParallelism       = flag.Int("optimizer-parallelism", 16, "Maximum number of target surfaces optimized in parallel; 0 uses GOMAXPROCS")
	parallelInferenceEnabled   = flag.Bool("parallel-inference", true, "Enable parallel inference across eval cases")
	parallelEvaluationEnabled  = flag.Bool("parallel-evaluation", true, "Enable parallel evaluation across eval cases")
	parallelBackwardEnabled    = flag.Bool("parallel-backward", false, "Enable parallel backward processing across train eval cases")
	parallelAggregationEnabled = flag.Bool("parallel-aggregation", true, "Enable parallel aggregation across target surfaces")
	parallelOptimizerEnabled   = flag.Bool("parallel-optimization", true, "Enable parallel optimization across target surfaces")
	minScoreGain               = flag.Float64("min-score-gain", 0.01, "Minimum validation score gain required to accept a patch")
	maxRoundsWithoutAcceptance = flag.Int("max-rounds-without-acceptance", 3, "Maximum consecutive rejected rounds before stopping")
	targetScore                = flag.Float64("target-score", 1.0, "Target validation score that stops optimization when reached")
	debugIO                    = flag.Bool("debug-io", false, "Log candidate, judge, backwarder, aggregator, and optimizer inputs and outputs for troubleshooting")
	fakeEnabled                = flag.Bool("fake", false, "Use a deterministic, offline fake model so the whole pipeline runs without an API key")
	fakeScenarioFlag           = flag.String("fake-scenario", "happy", "Fake scenario for -fake mode: happy (optimization succeeds, candidate accepted), no-gain (no improvement, candidate rejected), regression (optimization regresses, candidate rejected)")
	keyCases                   = flag.String("key-cases", "", "Comma-separated case IDs that must not regress (key cases); wired into GateConfig.KeyCaseIDs")
	// Pipeline-level configuration surface.
	configFile       = flag.String("config", "", "Path to a JSON pipeline config file (all serializable options). CLI flags override file values.")
	promptFile       = flag.String("prompt-file", "", "Path to a file whose contents become the candidate instruction (overrides -candidate-instruction).")
	promptType       = flag.String("prompt-type", "agent", "Prompt surface to optimize: agent|system|skill|router")
	targetSurf       = flag.String("target-surface", "", "Explicit full target surface id(s), comma-separated; overrides -prompt-type")
	costBudget       = flag.Float64("cost-budget", 0, "Max estimated run cost; 0 = unlimited")
	costPerEval      = flag.Float64("cost-per-eval", 0.01, "Unit cost per eval case")
	costPerWorker    = flag.Float64("cost-per-worker", 0.05, "Unit cost per PromptIter worker call")
	seedFlag         = flag.Int("seed", 42, "Random seed recorded in the audit report for reproducibility (the deterministic fake runner is unaffected by its value)")
	attribution      = flag.String("attribution", "rule", "Failure attribution method: rule (deterministic, default) | llm (LLM-enhanced reasons) | auto (llm when a real LLM is available, otherwise rule)")
	attributionModel = flag.String("attribution-model", "deepseek-v4-flash", "Model used for LLM attribution (per-case reason + cross-case insight); defaults to deepseek-v4-flash. Requires OPENAI_BASE_URL/OPENAI_API_KEY.")
)

func main() {
	flag.Parse()

	logger := log.New(log.Writer(), "", log.LstdFlags|log.Lmicroseconds)
	cfg := regressionConfig{
		DataDir:                    *dataDir,
		OutputDir:                  *outputDir,
		CandidateModelName:         *modelName,
		CandidateInstruction:       *candidateInstruction,
		JudgeModelName:             *judgeModelName,
		WorkerModelName:            *workerModelName,
		MaxRounds:                  *maxRounds,
		MinScoreGain:               *minScoreGain,
		MaxRoundsWithoutAcceptance: *maxRoundsWithoutAcceptance,
		TargetScore:                *targetScore,
		EvalCaseParallelism:        *evalCaseParallelism,
		BackwardCaseParallelism:    *backwardCaseParallelism,
		AggregationParallelism:     *aggregationParallelism,
		OptimizerParallelism:       *optimizerParallelism,
		ParallelInferenceEnabled:   *parallelInferenceEnabled,
		ParallelEvaluationEnabled:  *parallelEvaluationEnabled,
		ParallelBackwardEnabled:    *parallelBackwardEnabled,
		ParallelAggregationEnabled: *parallelAggregationEnabled,
		ParallelOptimizerEnabled:   *parallelOptimizerEnabled,
		Fake:                       *fakeEnabled,
		FakeScenario:               parseScenario(*fakeScenarioFlag),
		KeyCaseIDs:                 parseKeyCases(*keyCases),
		DebugIO:                    *debugIO,
		Logger:                     logger,
		PromptType:                 *promptType,
		TrainEvalSetID:             trainEvalSetID,
		ValidationEvalSetID:        validationEvalSetID,
		MetricFileID:               sharedMetricFileID,
		CostPerEval:                *costPerEval,
		CostPerWorker:              *costPerWorker,
		CostBudget:                 *costBudget,
		Seed:                       *seedFlag,
		Attribution:                *attribution,
		AttributionModelName:       *attributionModel,
	}

	// 1) Config file provides defaults for any unset option.
	if *configFile != "" {
		if err := loadConfigFile(*configFile, &cfg); err != nil {
			log.Fatalf("load config file: %v", err)
		}
	}
	// 2) Explicit CLI flags override config-file values.
	applyFlagOverrides(&cfg)
	// 3) Prompt source file overrides the candidate instruction.
	if s := strings.TrimSpace(*promptFile); s != "" {
		b, err := os.ReadFile(s)
		if err != nil {
			log.Fatalf("read prompt file: %v", err)
		}
		cfg.CandidateInstruction = strings.TrimSpace(string(b))
	}
	// 4) Explicit target surfaces override the prompt-type mapping.
	if s := strings.TrimSpace(*targetSurf); s != "" {
		cfgs := []string{}
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfgs = append(cfgs, p)
			}
		}
		if len(cfgs) > 0 {
			cfg.TargetSurfaces = cfgs
		}
	}

	// In fake mode, fall back to the plain baseline instruction unless the user
	// explicitly provided one: the real-model default already contains the
	// structured sections the fake candidate treats as "boosted", which would
	// leave the fake optimizer no gain to demonstrate.
	if cfg.Fake && cfg.CandidateInstruction == defaultCandidateInstruction {
		cfg.CandidateInstruction = fakeBaselineInstruction
	}

	// Propagate the (possibly overridden) metric file id to the shared locator.
	sharedMetricFileID = cfg.MetricFileID

	if err := runRegressionLoop(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}

// parseKeyCases splits a comma-separated key-case list into a slice.
func parseKeyCases(s string) []string {
	ids := []string{}
	if s = strings.TrimSpace(s); s == "" {
		return ids
	}
	for _, id := range strings.Split(s, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// applyFlagOverrides copies only the flags explicitly set on the command line
// over the current config (which may already include config-file values).
func applyFlagOverrides(cfg *regressionConfig) {
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "data-dir":
			cfg.DataDir = *dataDir
		case "output-dir":
			cfg.OutputDir = *outputDir
		case "model":
			cfg.CandidateModelName = *modelName
		case "candidate-instruction":
			cfg.CandidateInstruction = *candidateInstruction
		case "judge-model":
			cfg.JudgeModelName = *judgeModelName
		case "worker-model":
			cfg.WorkerModelName = *workerModelName
		case "max-rounds":
			cfg.MaxRounds = *maxRounds
		case "min-score-gain":
			cfg.MinScoreGain = *minScoreGain
		case "max-rounds-without-acceptance":
			cfg.MaxRoundsWithoutAcceptance = *maxRoundsWithoutAcceptance
		case "target-score":
			cfg.TargetScore = *targetScore
		case "eval-case-parallelism":
			cfg.EvalCaseParallelism = *evalCaseParallelism
		case "backward-case-parallelism":
			cfg.BackwardCaseParallelism = *backwardCaseParallelism
		case "aggregation-parallelism":
			cfg.AggregationParallelism = *aggregationParallelism
		case "optimizer-parallelism":
			cfg.OptimizerParallelism = *optimizerParallelism
		case "parallel-inference":
			cfg.ParallelInferenceEnabled = *parallelInferenceEnabled
		case "parallel-evaluation":
			cfg.ParallelEvaluationEnabled = *parallelEvaluationEnabled
		case "parallel-backward":
			cfg.ParallelBackwardEnabled = *parallelBackwardEnabled
		case "parallel-aggregation":
			cfg.ParallelAggregationEnabled = *parallelAggregationEnabled
		case "parallel-optimization":
			cfg.ParallelOptimizerEnabled = *parallelOptimizerEnabled
		case "fake":
			cfg.Fake = *fakeEnabled
		case "fake-scenario":
			cfg.FakeScenario = parseScenario(*fakeScenarioFlag)
		case "key-cases":
			cfg.KeyCaseIDs = parseKeyCases(*keyCases)
		case "prompt-type":
			cfg.PromptType = *promptType
		case "attribution":
			cfg.Attribution = *attribution
		case "attribution-model":
			cfg.AttributionModelName = *attributionModel
		case "cost-budget":
			cfg.CostBudget = *costBudget
		case "cost-per-eval":
			cfg.CostPerEval = *costPerEval
		case "cost-per-worker":
			cfg.CostPerWorker = *costPerWorker
		case "seed":
			cfg.Seed = *seedFlag
		}
	})
}

// loadConfigFile overlays the JSON config file onto cfg. Only keys present in the
// file are applied, so a file can partially override the CLI/example defaults.
// Zero values in the file (e.g. minScoreGain=0) are honored.
func loadConfigFile(path string, cfg *regressionConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	setStr := func(key string, dst *string) {
		if v, ok := raw[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				*dst = s
			}
		}
	}
	setStr("dataDir", &cfg.DataDir)
	setStr("outputDir", &cfg.OutputDir)
	setStr("candidateModelName", &cfg.CandidateModelName)
	setStr("candidateInstruction", &cfg.CandidateInstruction)
	setStr("judgeModelName", &cfg.JudgeModelName)
	setStr("workerModelName", &cfg.WorkerModelName)
	setStr("attribution", &cfg.Attribution)
	setStr("attributionModelName", &cfg.AttributionModelName)
	setStr("promptType", &cfg.PromptType)
	setStr("trainEvalSetID", &cfg.TrainEvalSetID)
	setStr("validationEvalSetID", &cfg.ValidationEvalSetID)
	setStr("metricFileID", &cfg.MetricFileID)

	setFloat := func(key string, dst *float64) {
		if v, ok := raw[key]; ok {
			var f float64
			if json.Unmarshal(v, &f) == nil {
				*dst = f
			}
		}
	}
	setFloat("minScoreGain", &cfg.MinScoreGain)
	setFloat("targetScore", &cfg.TargetScore)
	setFloat("costPerEval", &cfg.CostPerEval)
	setFloat("costPerWorker", &cfg.CostPerWorker)
	setFloat("costBudget", &cfg.CostBudget)

	setInt := func(key string, dst *int) {
		if v, ok := raw[key]; ok {
			var i int
			if json.Unmarshal(v, &i) == nil {
				*dst = i
			}
		}
	}
	setInt("maxRounds", &cfg.MaxRounds)
	setInt("maxRoundsWithoutAcceptance", &cfg.MaxRoundsWithoutAcceptance)
	setInt("seed", &cfg.Seed)
	setInt("evalCaseParallelism", &cfg.EvalCaseParallelism)
	setInt("backwardCaseParallelism", &cfg.BackwardCaseParallelism)
	setInt("aggregationParallelism", &cfg.AggregationParallelism)
	setInt("optimizerParallelism", &cfg.OptimizerParallelism)

	setBool := func(key string, dst *bool) {
		if v, ok := raw[key]; ok {
			var b bool
			if json.Unmarshal(v, &b) == nil {
				*dst = b
			}
		}
	}
	setBool("parallelInferenceEnabled", &cfg.ParallelInferenceEnabled)
	setBool("parallelEvaluationEnabled", &cfg.ParallelEvaluationEnabled)
	setBool("parallelBackwardEnabled", &cfg.ParallelBackwardEnabled)
	setBool("parallelAggregationEnabled", &cfg.ParallelAggregationEnabled)
	setBool("parallelOptimizerEnabled", &cfg.ParallelOptimizerEnabled)
	setBool("fake", &cfg.Fake)

	setStrSlice := func(key string, dst *[]string) {
		if v, ok := raw[key]; ok {
			var s []string
			if json.Unmarshal(v, &s) == nil {
				*dst = s
			}
		}
	}
	setStrSlice("keyCaseIDs", &cfg.KeyCaseIDs)
	setStrSlice("targetSurfaces", &cfg.TargetSurfaces)

	if v, ok := raw["fakeScenario"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			cfg.FakeScenario = parseScenario(s)
		}
	}
	return nil
}
