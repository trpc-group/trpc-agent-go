//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
)

const structuredOutputGuardMetric = "structured_output_guard"

func newRegressionMetricRegistry() (metricregistry.Registry, error) {
	registry := metricregistry.New()
	err := registry.RegisterFinalResponseCompare(
		"expected_json_exact_when_requested",
		expectedJSONExactWhenRequested,
	)
	if err != nil {
		return nil, err
	}
	return registry, nil
}

func expectedJSONExactWhenRequested(actual, expected *evalset.Invocation) (bool, error) {
	expectedText := strings.TrimSpace(evalsetMessageText(expected))
	if !looksLikeJSONObject(expectedText) {
		return true, nil
	}
	actualText := strings.TrimSpace(evalsetMessageText(actual))
	criterion := &criterionjson.JSONCriterion{
		Valid:         true,
		MatchStrategy: criterionjson.JSONMatchStrategyExact,
	}
	return criterion.Match(json.RawMessage(actualText), json.RawMessage(expectedText))
}

func evalsetMessageText(invocation *evalset.Invocation) string {
	if invocation == nil || invocation.FinalResponse == nil {
		return ""
	}
	return invocation.FinalResponse.Content
}

func looksLikeJSONObject(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "{")
}
