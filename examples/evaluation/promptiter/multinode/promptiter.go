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
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	promptitermanager "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/manager"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func runPromptIter(
	ctx context.Context,
	candidateModel model.Model,
	judgeModel model.Model,
	workerModel model.Model,
) (*promptiterengine.RunResult, error) {
	candidateAgent, err := newSportsRecapAgent(candidateModel)
	if err != nil {
		return nil, fmt.Errorf("create candidate agent: %w", err)
	}
	candidateRunner := runner.NewRunner(sportsRecapAgentName, candidateAgent)
	judgeRunner := runner.NewRunner("judge", newJudgeAgent(judgeModel))
	backwarderRunner := runner.NewRunner("backwarder", newPromptIterAgent("backwarder", workerModel))
	aggregatorRunner := runner.NewRunner("aggregator", newPromptIterAgent("aggregator", workerModel))
	optimizerRunner := runner.NewRunner("optimizer", newPromptIterAgent("optimizer", workerModel))
	defer closeRunners(candidateRunner, judgeRunner, backwarderRunner, aggregatorRunner, optimizerRunner)
	agentEvaluator, err := newSportsRecapEvaluator(candidateRunner, judgeRunner)
	if err != nil {
		return nil, err
	}
	defer agentEvaluator.Close()
	backwarderInstance, err := backwarder.New(ctx, backwarderRunner)
	if err != nil {
		return nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		return nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		return nil, fmt.Errorf("create optimizer: %w", err)
	}
	engineInstance, err := promptiterengine.New(ctx, candidateAgent, agentEvaluator, backwarderInstance, aggregatorInstance, optimizerInstance)
	if err != nil {
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	managerInstance, err := promptitermanager.New(sportsRecapAgentName, engineInstance)
	if err != nil {
		return nil, fmt.Errorf("create promptiter manager: %w", err)
	}
	defer managerInstance.Close()
	run, err := managerInstance.Start(ctx, buildPromptIterRunRequest())
	if err != nil {
		return nil, fmt.Errorf("start promptiter run: %w", err)
	}
	fmt.Printf("Started PromptIter manager run: %s\n", run.ID)
	result, err := waitForRun(ctx, managerInstance, run.ID, *pollInterval)
	if err != nil {
		return nil, fmt.Errorf("wait for promptiter run: %w", err)
	}
	return result, nil
}

func buildPromptIterRunRequest() *promptiterengine.RunRequest {
	desiredScore := *targetScore
	return &promptiterengine.RunRequest{
		Train:      []promptiterengine.EvalSetInput{{EvalSetID: "sports-recap-train"}},
		Validation: []promptiterengine.EvalSetInput{{EvalSetID: "sports-recap-validation"}},
		EvaluationOptions: promptiterengine.EvaluationOptions{
			EvalCaseParallelism:               *evalCaseParallelism,
			EvalCaseParallelInferenceEnabled:  *parallelInferenceEnabled,
			EvalCaseParallelEvaluationEnabled: *parallelEvaluationEnabled,
		},
		BackwardOptions: promptiterengine.BackwardOptions{
			CaseParallelismEnabled: *parallelBackwardEnabled,
			CaseParallelism:        *backwardCaseParallelism,
		},
		AggregationOptions: promptiterengine.AggregationOptions{
			SurfaceParallelismEnabled: *parallelAggregationEnabled,
			SurfaceParallelism:        *aggregationParallelism,
		},
		OptimizerOptions: promptiterengine.OptimizerOptions{
			SurfaceParallelismEnabled: *parallelOptimizerEnabled,
			SurfaceParallelism:        *optimizerParallelism,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: *minScoreGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: *maxRoundsWithoutAcceptance,
			TargetScore:                &desiredScore,
		},
		MaxRounds:        *maxRounds,
		TargetSurfaceIDs: candidateSurfaceIDs(),
	}
}

func waitForRun(
	ctx context.Context,
	manager promptitermanager.Manager,
	runID string,
	interval time.Duration,
) (*promptiterengine.RunResult, error) {
	if interval <= 0 {
		return nil, errors.New("poll interval must be greater than 0")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	reportedBaseline := false
	reportedTrainRounds := make(map[int]struct{})
	reportedValidationRounds := make(map[int]struct{})
	reportedCompletedRounds := make(map[int]struct{})
	for {
		run, err := manager.Get(ctx, runID)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, errors.New("manager returned nil run")
		}
		printProgress(runID, run)
		reportedBaseline = reportBaseline(runID, run, reportedBaseline)
		reportRoundMilestones(runID, run, reportedTrainRounds, reportedValidationRounds, reportedCompletedRounds)
		if isTerminalRunStatus(run.Status) {
			if err := terminalRunError(run); err != nil {
				return nil, err
			}
			return run, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func newPromptIterAgent(name string, m model.Model) agent.Agent {
	return llmagent.New(
		name,
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(32768),
			Temperature: floatPtr(0.0),
			Stream:      false,
		}),
	)
}

func candidateSurfaceIDs() []string {
	return []string{
		astructure.SurfaceID(sportsRecapAgentName+"/"+headlineAgentName, astructure.SurfaceTypeInstruction),
		astructure.SurfaceID(sportsRecapAgentName+"/"+highlightsAgentName, astructure.SurfaceTypeInstruction),
		astructure.SurfaceID(sportsRecapAgentName+"/"+statsAngleAgentName, astructure.SurfaceTypeInstruction),
		astructure.SurfaceID(sportsRecapAgentName+"/"+recapWriterAgentName, astructure.SurfaceTypeInstruction),
		astructure.SurfaceID(sportsRecapAgentName+"/"+sportsEditorAgentName, astructure.SurfaceTypeInstruction),
	}
}

func isTerminalRunStatus(status promptiterengine.RunStatus) bool {
	switch status {
	case promptiterengine.RunStatusSucceeded,
		promptiterengine.RunStatusFailed,
		promptiterengine.RunStatusCanceled:
		return true
	default:
		return false
	}
}

func terminalRunError(run *promptiterengine.RunResult) error {
	switch run.Status {
	case promptiterengine.RunStatusSucceeded:
		return nil
	case promptiterengine.RunStatusFailed:
		message := run.ErrorMessage
		if message == "" {
			message = "unknown error"
		}
		return fmt.Errorf("run %s failed: %s", run.ID, message)
	case promptiterengine.RunStatusCanceled:
		return fmt.Errorf("run %s canceled", run.ID)
	default:
		return nil
	}
}

func closeRunners(runners ...runner.Runner) {
	for _, r := range runners {
		if r != nil {
			_ = r.Close()
		}
	}
}
