//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command promptiter_regression_loop runs an Evaluation + Optimization regression loop on top of
// the PromptIter engine and Evaluation service: it evaluates a baseline prompt, optimizes it,
// re-evaluates the candidate on a held-out validation set, and (in later phases) applies a
// multi-criterion acceptance gate and writes an audit report.
//
// This is phase P0: the scaffold forked from examples/evaluation/promptiter/syncrun with a
// model-source switch. It runs the raw optimization loop and prints a per-round summary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/pipeline"
)

// errCandidateRejected signals that the pipeline ran successfully but the acceptance gate rejected
// the optimized candidate. main() turns it into a non-zero exit code so CI can block on it, while
// keeping it distinct from an actual execution failure.
var errCandidateRejected = errors.New("candidate rejected by acceptance gate")

var (
	dataDir            = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir          = flag.String("output-dir", "./output", "Directory where evaluation results and reports are written")
	fixturesDir        = flag.String("fixtures-dir", "./fixtures", "Directory containing deterministic fake-model fixtures")
	baselinePromptFile = flag.String("baseline-prompt-file", "./data/promptiter-regression-loop-app/baseline-prompt.txt", "File holding the baseline candidate instruction; used when -candidate-instruction is empty")
	modelSrc           = flag.String("model-source", "fake", "Model source for all LLM roles: fake (no API key) or openai")

	modelName            = flag.String("model", "deepseek-v3.2", "Model identifier for the candidate agent (openai source)")
	candidateInstruction = flag.String("candidate-instruction", "", "Baseline instruction for the candidate agent; overrides -baseline-prompt-file when set")
	judgeModelName       = flag.String("judge-model", "gpt-5.2", "Model identifier for the judge agent (openai source)")
	workerModelName      = flag.String("worker-model", "gpt-5.2", "Model identifier for the PromptIter worker agents (openai source)")

	maxRounds                  = flag.Int("max-rounds", 3, "Maximum PromptIter optimization rounds")
	minScoreGain               = flag.Float64("min-score-gain", 0.01, "Minimum validation score gain the engine requires to accept a patch")
	maxRoundsWithoutAcceptance = flag.Int("max-rounds-without-acceptance", 2, "Maximum consecutive rejected rounds before stopping")
	targetScore                = flag.Float64("target-score", 1.0, "Validation score that stops optimization when reached")

	gateMinValidationGain  = flag.Float64("gate-min-validation-gain", 0.01, "Minimum validation mean-score gain the acceptance gate requires")
	keyCases               = flag.String("key-cases", "val_resolved_freeform_KEY", "Comma-separated validation case IDs that must not regress pass→fail (key_cases_protected gate criterion)")
	maxCandidateModelCalls = flag.Int("max-candidate-model-calls", 0, "Budget: maximum candidate model invocations the gate allows (0 disables the within_budget criterion)")

	evalCaseParallelism        = flag.Int("eval-case-parallelism", 8, "Maximum number of eval cases processed in parallel")
	backwardCaseParallelism    = flag.Int("backward-case-parallelism", 8, "Maximum train eval cases processed in parallel during backward; 0 uses GOMAXPROCS")
	aggregationParallelism     = flag.Int("aggregation-parallelism", 8, "Maximum target surfaces aggregated in parallel; 0 uses GOMAXPROCS")
	optimizerParallelism       = flag.Int("optimizer-parallelism", 8, "Maximum target surfaces optimized in parallel; 0 uses GOMAXPROCS")
	parallelInferenceEnabled   = flag.Bool("parallel-inference", true, "Enable parallel inference across eval cases")
	parallelEvaluationEnabled  = flag.Bool("parallel-evaluation", true, "Enable parallel evaluation across eval cases")
	parallelBackwardEnabled    = flag.Bool("parallel-backward", false, "Enable parallel backward processing across train eval cases")
	parallelAggregationEnabled = flag.Bool("parallel-aggregation", true, "Enable parallel aggregation across target surfaces")
	parallelOptimizerEnabled   = flag.Bool("parallel-optimization", true, "Enable parallel optimization across target surfaces")

	debugIO = flag.Bool("debug-io", false, "Log candidate, judge, and worker agent inputs and outputs")
)

func main() {
	flag.Parse()
	logger := log.New(log.Writer(), "", log.LstdFlags|log.Lmicroseconds)
	instruction, baselineSource := resolveBaselineInstruction(*candidateInstruction, *baselinePromptFile)
	cfg := runConfig{
		DataDir:                    *dataDir,
		OutputDir:                  *outputDir,
		FixturesDir:                *fixturesDir,
		BaselinePromptFile:         baselineSource,
		ModelSource:                modelSource(*modelSrc),
		CandidateModelName:         *modelName,
		CandidateInstruction:       instruction,
		JudgeModelName:             *judgeModelName,
		WorkerModelName:            *workerModelName,
		MaxRounds:                  *maxRounds,
		MinScoreGain:               *minScoreGain,
		MaxRoundsWithoutAcceptance: *maxRoundsWithoutAcceptance,
		TargetScore:                *targetScore,
		GateMinValidationGain:      *gateMinValidationGain,
		KeyCaseIDs:                 splitCaseIDs(*keyCases),
		MaxCandidateModelCalls:     *maxCandidateModelCalls,
		EvalCaseParallelism:        *evalCaseParallelism,
		BackwardCaseParallelism:    *backwardCaseParallelism,
		AggregationParallelism:     *aggregationParallelism,
		OptimizerParallelism:       *optimizerParallelism,
		ParallelInferenceEnabled:   *parallelInferenceEnabled,
		ParallelEvaluationEnabled:  *parallelEvaluationEnabled,
		ParallelBackwardEnabled:    *parallelBackwardEnabled,
		ParallelAggregationEnabled: *parallelAggregationEnabled,
		ParallelOptimizerEnabled:   *parallelOptimizerEnabled,
		DebugIO:                    *debugIO,
		Logger:                     logger,
		modelCalls:                 &callCounter{},
	}
	if err := run(context.Background(), cfg); err != nil {
		if errors.Is(err, errCandidateRejected) {
			fmt.Fprintln(os.Stderr, "regression loop: "+err.Error())
			os.Exit(1)
		}
		log.Fatal(err)
	}
}

// run executes the optimization loop. Later phases wrap this with baseline evaluation, failure
// attribution, a multi-criterion acceptance gate, and audit-report emission.
func run(ctx context.Context, cfg runConfig) error {
	start := time.Now()
	fixture, err := loadFixtureIfFake(cfg)
	if err != nil {
		return err
	}
	runtime, err := buildPromptIterRuntime(ctx, cfg, fixture)
	if err != nil {
		return err
	}
	defer runtime.close()

	// P3: evaluate the baseline prompt on train + validation and attribute its failures before
	// optimization runs. This snapshot also feeds the per-case delta gate below.
	baseline, err := evaluateSnapshot(ctx, runtime.agentEvaluator)
	if err != nil {
		return err
	}
	printAttribution(baseline)

	surfaceID := targetSurfaceID()
	result, err := runtime.engine.Run(ctx, buildRunRequest(cfg, surfaceID))
	if err != nil {
		return err
	}

	// P4: evaluate the optimizer's accepted instruction on the held-out sets, then apply the
	// multi-criterion acceptance gate. The gate is a second veto on top of the engine's own
	// acceptance: the engine can accept a candidate that raises the validation mean while breaking
	// a previously-passing case; the gate rejects that overfit.
	accepted := acceptedInstructionText(result, cfg.CandidateInstruction, surfaceID)
	candidate, closeCandidate, err := evaluateCandidate(ctx, cfg, fixture, accepted)
	if err != nil {
		return err
	}
	defer closeCandidate()

	decision := pipeline.ApplyGate(
		pipeline.GatePolicy{
			MinValidationGain:      cfg.GateMinValidationGain,
			KeyCaseIDs:             cfg.KeyCaseIDs,
			MaxCandidateModelCalls: cfg.MaxCandidateModelCalls,
		},
		baseline.validation,
		candidate.validation,
		pipeline.GateObservations{CandidateModelCalls: cfg.modelCalls.count()},
	)
	printGateDecision(decision)

	// P5: emit the audit report (JSON + Markdown) capturing baseline, candidate, per-case deltas,
	// and the gate decision.
	if err := writeReport(cfg, result, baseline, candidate, decision, accepted, time.Since(start)); err != nil {
		return err
	}
	if err := printRunSummary(result, cfg.CandidateInstruction, surfaceID); err != nil {
		return err
	}

	// A rejected candidate is a valid, successful pipeline outcome; surface it as a non-zero exit
	// so CI blocks the change, but distinct from an execution error.
	if !decision.Accepted {
		return errCandidateRejected
	}
	return nil
}

// splitCaseIDs parses a comma-separated list of eval case IDs, dropping empties and whitespace.
func splitCaseIDs(raw string) []string {
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	return ids
}
