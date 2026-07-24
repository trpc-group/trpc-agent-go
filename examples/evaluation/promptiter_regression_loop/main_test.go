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
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

func TestRunDeterministicNativePipeline(t *testing.T) {
	outputDir := t.TempDir()
	report, err := run(context.Background(), "./data", outputDir)
	require.NoError(t, err)
	require.Equal(t, regression.PipelineSucceeded, report.Status)
	require.Len(t, report.BaselineTrain.Cases, 3)
	require.Len(t, report.BaselineValidation.Cases, 3)
	require.Len(t, report.Candidates, 2)
	require.Equal(t, regression.DecisionAccepted, report.Candidates[0].SearchDecision.Status)
	require.Equal(t, regression.DecisionAccepted, report.Candidates[0].ReleaseDecision.Status)
	require.Equal(t, regression.DecisionAccepted, report.Candidates[1].SearchDecision.Status)
	require.Equal(t, regression.DecisionRejected, report.Candidates[1].ReleaseDecision.Status)
	require.Equal(t, report.Candidates[0].Profile.Hash, report.ReleasedProfile.Hash)
	require.Contains(
		t,
		strings.Join(report.Candidates[1].ReleaseDecision.Reasons, "\n"),
		"critical",
	)
	require.True(t, report.Resources.Cumulative.ModelCalls.Available)
	require.Greater(t, report.Resources.Cumulative.ModelCalls.Value, int64(0))

	categories := make(map[regression.FailureCategory]bool)
	for _, snapshot := range []*regression.EvaluationSnapshot{
		report.BaselineTrain,
		report.BaselineValidation,
	} {
		for _, attribution := range snapshot.Attributions {
			categories[attribution.PrimaryCategory] = true
			require.NotEmpty(t, attribution.Reason)
		}
	}
	for _, category := range []regression.FailureCategory{
		regression.FailureResponseMismatch,
		regression.FailureWrongTool,
		regression.FailureWrongArguments,
		regression.FailureWrongRoute,
		regression.FailureInvalidFormat,
		regression.FailureKnowledgeRecall,
	} {
		require.Truef(t, categories[category], "missing category %s", category)
	}

	jsonPath := filepath.Join(outputDir, "optimization_report.json")
	markdownPath := filepath.Join(outputDir, "optimization_report.md")
	require.FileExists(t, jsonPath)
	require.FileExists(t, markdownPath)
	data, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	var persisted regression.Report
	require.NoError(t, json.Unmarshal(data, &persisted))
	require.Equal(t, report.RunID, persisted.RunID)
}

func TestDeterministicOptimizerUsesCurrentProfileHintsAndSeedOnly(t *testing.T) {
	current := "baseline"
	surface := &astructure.Surface{
		SurfaceID: targetSurfaceID,
		NodeID:    agentName,
		Type:      astructure.SurfaceTypeInstruction,
		Value:     astructure.SurfaceValue{Text: &current},
	}
	request := func(hint string) *optimizer.Request {
		return &optimizer.Request{
			Surface: surface,
			Gradient: &promptiter.AggregatedSurfaceGradient{
				SurfaceID: targetSurfaceID,
				NodeID:    agentName,
				Type:      astructure.SurfaceTypeInstruction,
				Gradients: []promptiter.SurfaceGradient{{
					SurfaceID: targetSurfaceID,
					Gradient:  hint,
				}},
			},
		}
	}
	first, err := (&deterministicOptimizer{seed: 2003}).Optimize(
		context.Background(),
		request("response mismatch"),
	)
	require.NoError(t, err)
	repeated, err := (&deterministicOptimizer{seed: 2003}).Optimize(
		context.Background(),
		request("response mismatch"),
	)
	require.NoError(t, err)
	require.Equal(t, first.Patch.Value.Text, repeated.Patch.Value.Text)
	require.Contains(t, *first.Patch.Value.Text, responseRuleMarker)
	require.NotContains(t, *first.Patch.Value.Text, toolRuleMarker)

	differentHint, err := (&deterministicOptimizer{seed: 2003}).Optimize(
		context.Background(),
		request("wrong tool"),
	)
	require.NoError(t, err)
	require.NotEqual(t, *first.Patch.Value.Text, *differentHint.Patch.Value.Text)
	require.Contains(t, *differentHint.Patch.Value.Text, toolRuleMarker)
	require.Contains(t, *differentHint.Patch.Value.Text, overRouteRuleMarker)
	require.NotContains(t, *differentHint.Patch.Value.Text, responseRuleMarker)

	wrongArguments, err := (&deterministicOptimizer{seed: 2003}).Optimize(
		context.Background(),
		request("wrong_arguments: exact orderId was not preserved"),
	)
	require.NoError(t, err)
	require.Contains(t, *wrongArguments.Patch.Value.Text, toolRuleMarker)
	require.NotContains(t, *wrongArguments.Patch.Value.Text, overRouteRuleMarker)

	invalidFormat, err := (&deterministicOptimizer{seed: 2003}).Optimize(
		context.Background(),
		request("invalid_format: expected strict JSON"),
	)
	require.NoError(t, err)
	require.Contains(t, *invalidFormat.Patch.Value.Text, formatRuleMarker)
	require.NotContains(t, *invalidFormat.Patch.Value.Text, toolRuleMarker)

	wrongRoute, err := (&deterministicOptimizer{seed: 2003}).Optimize(
		context.Background(),
		request("wrong_route: expected support branch"),
	)
	require.NoError(t, err)
	require.Contains(t, *wrongRoute.Patch.Value.Text, routeRuleMarker)
	require.NotContains(t, *wrongRoute.Patch.Value.Text, responseRuleMarker)

	differentSeed, err := (&deterministicOptimizer{seed: 99}).Optimize(
		context.Background(),
		request("response mismatch"),
	)
	require.NoError(t, err)
	require.NotEqual(t, *first.Patch.Value.Text, *differentSeed.Patch.Value.Text)
	require.Equal(t, targetSurfaceID, first.Patch.SurfaceID)
	require.Equal(t, "baseline", current)
}

func TestPipelinePatchesOnlyTargetSurface(t *testing.T) {
	agent := newDeterministicAgent(nil)
	before, err := astructure.Export(context.Background(), agent)
	require.NoError(t, err)
	beforeDescription := agent.Info().Description

	report, err := run(context.Background(), "./data", t.TempDir())
	require.NoError(t, err)
	for _, candidate := range report.Candidates {
		require.NotEmpty(t, candidate.Patches)
		for _, patch := range candidate.Patches {
			require.Equal(t, targetSurfaceID, patch.SurfaceID)
		}
		require.NotNil(t, candidate.Profile)
		for _, override := range candidate.Profile.Profile.Overrides {
			require.Equal(t, targetSurfaceID, override.SurfaceID)
		}
	}

	after, err := astructure.Export(context.Background(), agent)
	require.NoError(t, err)
	require.Equal(t, beforeDescription, agent.Info().Description)
	require.Equal(t, before, after)
}

func TestCheckedInReportRegeneratesDeterministically(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	_, err := run(context.Background(), "./data", firstDir)
	require.NoError(t, err)
	_, err = run(context.Background(), "./data", secondDir)
	require.NoError(t, err)

	first := stableReportJSON(t, filepath.Join(firstDir, reportJSON))
	second := stableReportJSON(t, filepath.Join(secondDir, reportJSON))
	checkedIn := stableReportJSON(t, filepath.Join("example_output", reportJSON))
	require.Equal(t, first, second)
	require.Equal(t, checkedIn, first)

	firstMarkdown := stableReportMarkdown(t, filepath.Join(firstDir, reportMarkdown))
	secondMarkdown := stableReportMarkdown(t, filepath.Join(secondDir, reportMarkdown))
	checkedInMarkdown := stableReportMarkdown(
		t,
		filepath.Join("example_output", reportMarkdown),
	)
	require.Equal(t, firstMarkdown, secondMarkdown)
	require.Equal(t, checkedInMarkdown, firstMarkdown)
}

func stableReportJSON(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var value any
	require.NoError(t, json.Unmarshal(data, &value))
	root, ok := value.(map[string]any)
	require.True(t, ok)
	runID, ok := root["runId"].(string)
	require.True(t, ok)
	require.NotEmpty(t, runID)
	return normalizeStableReport(value, "", runID)
}

func normalizeStableReport(value any, path, runID string) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			switch {
			case key == "latencyMs":
				typed[key] = float64(0)
			case path == "artifacts":
				if text, ok := child.(string); ok {
					typed[key] = filepath.Base(text)
				}
			default:
				typed[key] = normalizeStableReport(child, childPath, runID)
			}
		}
	case []any:
		for index := range typed {
			typed[index] = normalizeStableReport(typed[index], path, runID)
		}
	case string:
		return strings.ReplaceAll(typed, runID, "<execution-run-id>")
	}
	return value
}

func stableReportMarkdown(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(data)
	runPattern := regexp.MustCompile("(?m)^- Run: `([^`]+)`$")
	matches := runPattern.FindStringSubmatch(text)
	require.Len(t, matches, 2)
	require.NotEmpty(t, matches[1])
	text = strings.ReplaceAll(text, matches[1], "<execution-run-id>")
	text = regexp.MustCompile("latency: `[0-9]+ ms`").ReplaceAllString(
		text,
		"latency: `<measured>`",
	)
	text = regexp.MustCompile("latency ms: `(?:[0-9]+|unavailable)`").ReplaceAllString(
		text,
		"latency ms: `<measured>`",
	)
	return regexp.MustCompile("(?m)^- (JSON|Markdown): `[^`]*`$").ReplaceAllString(
		text,
		"- $1: `<artifact>`",
	)
}
