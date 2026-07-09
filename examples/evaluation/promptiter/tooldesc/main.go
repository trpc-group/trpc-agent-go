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
	dataDir                      = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir                    = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName                    = flag.String("model", "deepseek-v3.2", "Model identifier used by the candidate travel agent")
	judgeModelName               = flag.String("judge-model", "gpt-5.2", "Model identifier used by the judge agent")
	workerModelName              = flag.String("worker-model", "gpt-5.2", "Model identifier used by the PromptIter worker agents")
	maxRounds                    = flag.Int("max-rounds", 4, "Maximum PromptIter optimization rounds")
	evalCaseParallelism          = flag.Int("eval-case-parallelism", 8, "Maximum number of eval cases processed in parallel")
	parallelInferenceEnabled     = flag.Bool("parallel-inference", true, "Enable parallel inference across eval cases")
	parallelEvaluationEnabled    = flag.Bool("parallel-evaluation", true, "Enable parallel evaluation across eval cases")
	backwardCaseParallelism      = flag.Int("backward-case-parallelism", 8, "Maximum number of train eval cases processed in parallel during backward; 0 uses GOMAXPROCS")
	parallelBackwardEnabled      = flag.Bool("parallel-backward", true, "Enable parallel backward processing across train eval cases")
	surfaceParallelism           = flag.Int("surface-parallelism", 8, "Maximum number of target surfaces processed in parallel; 0 uses GOMAXPROCS")
	parallelSurfaceStagesEnabled = flag.Bool("parallel-surface-stages", true, "Enable parallel aggregation and optimization across target surfaces")
	minScoreGain                 = flag.Float64("min-score-gain", 0.01, "Minimum validation score gain required to accept a patch")
	maxRoundsWithoutAcceptance   = flag.Int("max-rounds-without-acceptance", 3, "Maximum consecutive rejected rounds before stopping")
	targetScore                  = flag.Float64("target-score", 1.0, "Target validation score that stops optimization when reached")
)

func main() {
	flag.Parse()
	cfg := toolDescConfig{
		DataDir:                        *dataDir,
		OutputDir:                      *outputDir,
		CandidateModelName:             *modelName,
		JudgeModelName:                 *judgeModelName,
		WorkerModelName:                *workerModelName,
		MaxRounds:                      *maxRounds,
		MinScoreGain:                   *minScoreGain,
		MaxRoundsWithoutAcceptance:     *maxRoundsWithoutAcceptance,
		TargetScore:                    *targetScore,
		EvalCaseParallelism:            *evalCaseParallelism,
		EvalCaseParallelInference:      *parallelInferenceEnabled,
		EvalCaseParallelEvaluation:     *parallelEvaluationEnabled,
		BackwardCaseParallelism:        *backwardCaseParallelism,
		BackwardCaseParallelismEnabled: *parallelBackwardEnabled,
		SurfaceParallelism:             *surfaceParallelism,
		SurfaceParallelismEnabled:      *parallelSurfaceStagesEnabled,
	}
	if err := runToolDescExample(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}
