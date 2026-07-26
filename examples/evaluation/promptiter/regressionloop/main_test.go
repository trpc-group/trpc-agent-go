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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	regressionloop "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

func TestFakeRegressionLoopGeneratesReportsAndRejectsOverfit(t *testing.T) {
	cfg, err := loadConfig(
		"./data/eval-optimization-regression-app/promptiter.json",
		t.TempDir(),
	)
	require.NoError(t, err)
	baseDir := filepath.Dir("./data/eval-optimization-regression-app/promptiter.json")
	pipeline := regressionloop.Pipeline{
		Evaluator:      fakeEvaluator{baseDir: baseDir, appName: cfg.AppName},
		PromptIterator: fakePromptIterator{baseDir: baseDir, appName: cfg.AppName, metricsPath: cfg.MetricsPath},
		Clock:          &fixedClock{now: time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)},
	}
	result, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.False(t, result.Report.GateDecision.Accepted)
	assert.Equal(t, 1, result.Report.Delta.Summary.NewlyFailed)
	assert.FileExists(t, result.JSONPath)
	assert.FileExists(t, result.MarkdownPath)
	md, err := os.ReadFile(result.MarkdownPath)
	require.NoError(t, err)
	assert.Contains(t, string(md), "critical cases regressed")
}

func TestFakeEngineScenarios(t *testing.T) {
	for _, tt := range []struct {
		scenario string
		accepted bool
	}{
		{scenario: "success", accepted: true},
		{scenario: "ineffective", accepted: false},
		{scenario: "overfit", accepted: false},
	} {
		t.Run(tt.scenario, func(t *testing.T) {
			cfg, err := loadConfig(
				"./data/eval-optimization-regression-app/promptiter.json",
				t.TempDir(),
			)
			require.NoError(t, err)
			cfg.Scenario = tt.scenario
			result, err := runPipeline(
				context.Background(),
				cfg,
				filepath.Dir("./data/eval-optimization-regression-app/promptiter.json"),
				"fake-engine",
			)
			require.NoError(t, err)
			assert.Equal(t, tt.accepted, result.Report.GateDecision.Accepted)
			assert.NotEmpty(t, result.Report.Rounds)
			assert.FileExists(t, result.JSONPath)
			assert.FileExists(t, result.MarkdownPath)
			assert.True(t, result.Report.Cost.ModelCallsMeasured)
			assert.NotEmpty(t, result.Report.CandidateSurfaces)
			jsonReport, err := os.ReadFile(result.JSONPath)
			require.NoError(t, err)
			assert.Contains(t, string(jsonReport), `"modelCallsMeasured": true`)
			assert.Contains(t, string(jsonReport), `"candidateSurfaces"`)
			md, err := os.ReadFile(result.MarkdownPath)
			require.NoError(t, err)
			assert.Contains(t, string(md), "## Candidate Surfaces")
		})
	}
}

func TestTraceSmokeModeGeneratesAuditableReport(t *testing.T) {
	cfg, err := loadConfig(
		"./data/eval-optimization-regression-app/promptiter.json",
		t.TempDir(),
	)
	require.NoError(t, err)
	cfg.Scenario = "trace-smoke"
	result, err := runPipeline(
		context.Background(),
		cfg,
		filepath.Dir("./data/eval-optimization-regression-app/promptiter.json"),
		"trace-smoke",
	)
	require.NoError(t, err)
	assert.False(t, result.Report.GateDecision.Accepted)
	assert.FileExists(t, result.JSONPath)
	assert.FileExists(t, result.MarkdownPath)
	require.NotEmpty(t, result.Report.BaselineValidation.EvalSets)
	assert.NotNil(t, result.Report.BaselineValidation.EvalSets[0].Cases[0].Trace)
	assert.Equal(t, "trace-smoke", result.Report.Metadata.Scenario)
	assert.Contains(t, result.Report.Metadata.FakeConfig["optimization"], "skipped")
	assert.NotEmpty(t, result.Report.CandidateSurfaces)
	jsonReport, err := os.ReadFile(result.JSONPath)
	require.NoError(t, err)
	assert.Contains(t, string(jsonReport), `"candidateSurfaces"`)
	md, err := os.ReadFile(result.MarkdownPath)
	require.NoError(t, err)
	assert.Contains(t, string(md), "## Candidate Surfaces")
}

func TestTraceFakeEngineModeRunsFullOptimizationWithTraces(t *testing.T) {
	cfg, err := loadConfig(
		"./data/eval-optimization-regression-app/promptiter.json",
		t.TempDir(),
	)
	require.NoError(t, err)
	cfg.Scenario = "success"
	result, err := runPipeline(
		context.Background(),
		cfg,
		filepath.Dir("./data/eval-optimization-regression-app/promptiter.json"),
		"trace-fake-engine",
	)
	require.NoError(t, err)
	assert.True(t, result.Report.GateDecision.Accepted)
	assert.NotEmpty(t, result.Report.Rounds)
	assert.NotEmpty(t, result.Report.CandidatePrompt)
	require.NotEmpty(t, result.Report.CandidateValidation.EvalSets)
	assert.NotNil(t, result.Report.CandidateValidation.EvalSets[0].Cases[0].Trace)
	assert.Equal(t, "deterministic-trace-fake-engine", result.Report.Metadata.FakeConfig["runner"])
	assert.Contains(t, result.Report.Metadata.FakeConfig["optimization"], "complete")
}

func TestCommittedAuditFixturesMatchCurrentSchema(t *testing.T) {
	fixtures := []string{
		"optimization_report.json",
		"ineffective/optimization_report.json",
		"overfit/optimization_report.json",
		"success/optimization_report.json",
		"trace-fake-engine/ineffective/optimization_report.json",
		"trace-fake-engine/overfit/optimization_report.json",
		"trace-fake-engine/success/optimization_report.json",
		"trace-smoke/optimization_report.json",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			jsonPath := filepath.Join("output", fixture)
			data, err := os.ReadFile(jsonPath)
			require.NoError(t, err)
			var report struct {
				Cost struct {
					ModelCallsMeasured bool `json:"modelCallsMeasured"`
				} `json:"cost"`
				CandidateSurfaces []json.RawMessage `json:"candidateSurfaces"`
			}
			require.NoError(t, json.Unmarshal(data, &report))
			assert.True(t, report.Cost.ModelCallsMeasured)
			assert.NotEmpty(t, report.CandidateSurfaces)

			markdown, err := os.ReadFile(strings.TrimSuffix(jsonPath, ".json") + ".md")
			require.NoError(t, err)
			assert.Contains(t, string(markdown), "## Candidate Surfaces")
		})
	}
}

func TestLoadConfigResolvesDefaultPathFromModuleParent(t *testing.T) {
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Join("..", "..")))
	t.Cleanup(func() { require.NoError(t, os.Chdir(previous)) })

	cfg, err := loadConfig(
		"./data/eval-optimization-regression-app/promptiter.json",
		t.TempDir(),
	)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(cfg.PromptSource))
	assert.True(t, strings.HasSuffix(cfg.PromptSource, filepath.Join("data", "eval-optimization-regression-app", "baseline_prompt.txt")))
}

func TestSelectedScenariosDefaultsTraceSmokeModeToTraceSmoke(t *testing.T) {
	assert.Equal(t, []string{"trace-smoke"}, selectedScenarios("overfit", "", "trace-smoke"))
	assert.Equal(t, []string{"success"}, selectedScenarios("overfit", "success", "trace-smoke"))
}
