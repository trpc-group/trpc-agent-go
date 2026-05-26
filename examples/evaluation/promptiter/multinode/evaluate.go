//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func newSportsRecapEvaluator(candidateRunner runner.Runner, judgeRunner runner.Runner) (evaluation.AgentEvaluator, error) {
	agentEvaluator, err := evaluation.New(
		sportsRecapAgentName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalsetlocal.New(evalset.WithBaseDir(*dataDir))),
		evaluation.WithMetricManager(metriclocal.New(
			metric.WithBaseDir(*dataDir),
			metric.WithLocator(sportsRecapMetricLocator{}),
		)),
		evaluation.WithEvalResultManager(evalresultlocal.New(evalresult.WithBaseDir(*outputDir))),
		evaluation.WithRegistry(registry.New()),
		evaluation.WithJudgeRunner(judgeRunner),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	return agentEvaluator, nil
}

type sportsRecapMetricLocator struct{}

func (sportsRecapMetricLocator) Build(baseDir string, appName string, _ string) string {
	return filepath.Join(baseDir, appName, "sports-recap.metrics.json")
}

func newJudgeAgent(m model.Model) agent.Agent {
	return llmagent.New(
		"judge",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(32768),
			Temperature: floatPtr(0.0),
			Stream:      false,
		}),
	)
}
