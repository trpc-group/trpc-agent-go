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
	"log"
)

var (
	dataDir                    = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir                  = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName                  = flag.String("model", "deepseek-v4-flash", "Model identifier used by the candidate agent")
	candidateInstruction       = flag.String("candidate-instruction", defaultCandidateInstruction, "Instruction used by the candidate agent")
	judgeModelName             = flag.String("judge-model", "gpt-5.4", "Model identifier used by the judge agent")
	workerModelName            = flag.String("worker-model", "gpt-5.4", "Model identifier used by the PromptIter backwarder, aggregator, and optimizer agents")
	maxRounds                  = flag.Int("max-rounds", 4, "Maximum PromptIter optimization rounds")
	minScoreGain               = flag.Float64("min-score-gain", 0.005, "Minimum validation score gain required to accept a patch")
	maxRoundsWithoutAcceptance = flag.Int("max-rounds-without-acceptance", 5, "Maximum consecutive rejected rounds before stopping")
	targetScore                = flag.Float64("target-score", 1.0, "Target validation score that stops optimization when reached")
	evalCaseParallelism        = flag.Int("eval-case-parallelism", 8, "Maximum number of eval cases processed in parallel")
	parallelInferenceEnabled   = flag.Bool("parallel-inference", true, "Enable parallel inference across eval cases")
	parallelEvaluationEnabled  = flag.Bool("parallel-evaluation", true, "Enable parallel evaluation across eval cases")
	debugIO                    = flag.Bool("debug-io", false, "Log candidate, judge, backwarder, aggregator, and optimizer inputs and outputs for troubleshooting")
)

func main() {
	flag.Parse()
	logger := log.New(log.Writer(), "", log.LstdFlags|log.Lmicroseconds)
	cfg := syncRunConfig{
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
		ParallelInferenceEnabled:   *parallelInferenceEnabled,
		ParallelEvaluationEnabled:  *parallelEvaluationEnabled,
		DebugIO:                    *debugIO,
		Logger:                     logger,
	}
	if err := runSyncRunExample(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}
