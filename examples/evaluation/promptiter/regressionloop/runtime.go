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
	"fmt"
	"path/filepath"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName             = "eval-optimization-app"
	trainEvalSetID      = "train"
	validationEvalSetID = "validation"
	metricFileID        = "eval-optimization"

	// marker is the sentinel the optimizer injects into the instruction; the
	// candidate model returns the gold answer only when it sees this marker. The
	// baseline instruction is the single source of truth in baseline.instruction.txt.
	marker    = "USE_STRUCTURED_RECAP"
	poorRecap = "比赛结束。"
)

// golds maps a stable score token in the user input to the exact gold answer,
// so the candidate can pass exact-match scoring once the instruction is
// optimized. The score tokens are unique across all train and validation cases.
var golds = map[string]string{
	"100-90":  "红队以100-90战胜蓝队。",
	"77-70":   "绿队以77-70战胜黄队。",
	"3-2":     "黑队以3-2战胜白队。",
	"88-80":   "金队以88-80战胜银队。",
	"5-4":     "火队以5-4战胜冰队。",
	"112-108": "山队以112-108战胜海队。",
}

// optimizedInstruction is what the fake optimizer proposes; it carries marker.
const optimizedInstruction = marker + " 严格按 \"<胜队>以<比分>战胜<负队>。\" 的格式输出简体中文战报，只输出一句话。"

type runtime struct {
	engine          promptiterengine.Engine
	targetSurfaceID string
	close           func()
}

type sharedMetricLocator struct{ metricFileID string }

// Build maps every eval set to the scenario's metric file.
func (l sharedMetricLocator) Build(baseDir, app, _ string) string {
	return filepath.Join(baseDir, app, l.metricFileID+".metrics.json")
}

func buildRuntime(ctx context.Context, dataDir, outputDir, instruction string, sc scenario) (*runtime, error) {
	targetSurfaceID := astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction)

	// Deterministic worker payloads (final assistant content decoded as JSON).
	backwardContent, err := json.Marshal(map[string]any{
		"Gradients": []map[string]any{
			{
				"SurfaceID": targetSurfaceID,
				"Severity":  "P1",
				"Gradient":  "baseline instruction is too vague; require the exact structured recap format",
			},
		},
		"Upstream": []any{},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal backward content: %w", err)
	}
	aggregateContent, err := json.Marshal(map[string]any{
		"Gradients": []map[string]any{
			{"Severity": "P1", "Gradient": "instruction must demand the exact structured recap format"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal aggregate content: %w", err)
	}
	optimizeContent, err := json.Marshal(map[string]any{
		"Value":  map[string]any{"Text": optimizedInstruction},
		"Reason": "add an explicit one-sentence structured output format",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal optimize content: %w", err)
	}

	candidateModel := newCandidateModel("candidate-model", marker, poorRecap, sc.baselineGolds, sc.optimizedGolds)
	judgeModel := newStaticModel("judge-model", "{}")
	backwarderModel := newStaticModel("backwarder-model", string(backwardContent))
	aggregatorModel := newStaticModel("aggregator-model", string(aggregateContent))
	optimizerModel := newStaticModel("optimizer-model", string(optimizeContent))

	candidateAgent := newCandidateAgent(candidateModel, instruction)
	judgeAgent := newJudgeAgent(judgeModel)
	backwarderAgent := newBackwarderAgent(backwarderModel)
	aggregatorAgent := newAggregatorAgent(aggregatorModel)
	optimizerAgent := newOptimizerAgent(optimizerModel)

	candidateRunner := runner.NewRunner(appName, candidateAgent)
	judgeRunner := runner.NewRunner(appName+"-judge", judgeAgent)
	backwarderRunner := runner.NewRunner(appName+"-backwarder", backwarderAgent)
	aggregatorRunner := runner.NewRunner(appName+"-aggregator", aggregatorAgent)
	optimizerRunner := runner.NewRunner(appName+"-optimizer", optimizerAgent)
	closeAll := func() {
		candidateRunner.Close()
		judgeRunner.Close()
		backwarderRunner.Close()
		aggregatorRunner.Close()
		optimizerRunner.Close()
	}

	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(dataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(dataDir),
		metric.WithLocator(sharedMetricLocator{metricFileID: sc.metricFileID}),
	)
	// Engine eval-result dumps carry non-deterministic UUID filenames; keep them
	// in a subdir so the committed report stays clean.
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(filepath.Join(outputDir, "evalresults")))

	agentEvaluator, err := evaluation.New(
		appName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithJudgeRunner(judgeRunner),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("create evaluator: %w", err)
	}

	backwarderInstance, err := backwarder.New(ctx, backwarderRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create optimizer: %w", err)
	}

	engineInstance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(candidateAgent),
		promptiterengine.WithAgentEvaluator(agentEvaluator),
		promptiterengine.WithBackwarder(backwarderInstance),
		promptiterengine.WithAggregator(aggregatorInstance),
		promptiterengine.WithOptimizer(optimizerInstance),
	)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}

	return &runtime{
		engine:          engineInstance,
		targetSurfaceID: targetSurfaceID,
		close: func() {
			agentEvaluator.Close()
			closeAll()
		},
	}, nil
}

func buildRunRequest(targetSurfaceID string, cfg *loopConfig, sc scenario) *promptiterengine.RunRequest {
	targetScore := cfg.TargetScore
	return &promptiterengine.RunRequest{
		Train:      []promptiterengine.EvalSetInput{{EvalSetID: sc.trainEvalSetID}},
		Validation: []promptiterengine.EvalSetInput{{EvalSetID: sc.validationEvalSetID}},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			// From config by default; the overfit scenario lowers it so the engine
			// accepts a candidate the harness gate then rejects on a regressed case.
			MinScoreGain: resolveMinScoreGain(cfg, sc),
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: cfg.MaxRoundsWithoutAcceptance,
			TargetScore:                &targetScore,
		},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: []string{targetSurfaceID},
	}
}
