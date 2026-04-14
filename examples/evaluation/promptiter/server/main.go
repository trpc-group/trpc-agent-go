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
	"strings"
)

var (
	addr                      = flag.String("addr", ":8080", "Listen address for the PromptIter server")
	basePath                  = flag.String("base-path", "/promptiter/v1/apps", "Base path exposed by the PromptIter server")
	dataDir                   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir                 = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName                 = flag.String("model", "deepseek-v3-local-II", "Model identifier used by the candidate agent")
	candidateInstruction      = flag.String("candidate-instruction", defaultCandidateInstruction, "Instruction used by the candidate agent")
	judgeModelName            = flag.String("judge-model", "gpt-5.4", "Model identifier used by the judge agent")
	workerModelName           = flag.String("worker-model", "gpt-5.4", "Model identifier used by the PromptIter backwarder, aggregator, and optimizer agents")
	numRuns                   = flag.Int("runs", 1, "Number of evaluation repeats per case")
	evalCaseParallelism       = flag.Int("eval-case-parallelism", 8, "Maximum number of eval cases processed in parallel")
	parallelInferenceEnabled  = flag.Bool("parallel-inference", true, "Enable parallel inference across eval cases")
	parallelEvaluationEnabled = flag.Bool("parallel-evaluation", true, "Enable parallel evaluation across eval cases")
)

func main() {
	flag.Parse()
	baseURL := strings.TrimRight(*basePath, "/")
	structureURL := fmt.Sprintf("%s/%s/structure", baseURL, appName)
	runsURL := fmt.Sprintf("%s/%s/runs", baseURL, appName)
	asyncRunsURL := fmt.Sprintf("%s/%s/async-runs", baseURL, appName)
	log.Printf("PromptIter server listening on %s%s", *addr, baseURL)
	log.Printf("PromptIter structure route: GET %s", structureURL)
	log.Printf("PromptIter runs route: POST %s", runsURL)
	log.Printf("PromptIter async runs route: POST %s", asyncRunsURL)
	if err := runPromptIterServer(context.Background(), serverConfig{
		Addr:                      *addr,
		BasePath:                  *basePath,
		DataDir:                   *dataDir,
		OutputDir:                 *outputDir,
		CandidateModelName:        *modelName,
		CandidateInstruction:      *candidateInstruction,
		JudgeModelName:            *judgeModelName,
		WorkerModelName:           *workerModelName,
		NumRuns:                   *numRuns,
		EvalCaseParallelism:       *evalCaseParallelism,
		ParallelInferenceEnabled:  *parallelInferenceEnabled,
		ParallelEvaluationEnabled: *parallelEvaluationEnabled,
	}); err != nil {
		log.Fatal(err)
	}
}
