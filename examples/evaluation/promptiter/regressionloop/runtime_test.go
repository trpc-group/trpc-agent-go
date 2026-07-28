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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeterministicRuntimeUsesRealPromptIterStages(t *testing.T) {
	dataDir := t.TempDir()
	writeRuntimeMetrics(t, dataDir, `[{"metricName":"quality"}]`)
	runtime, err := buildRuntime(context.Background(), runtimeConfig{
		Config:    validConfig(),
		DataDir:   dataDir,
		OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	assert.NotNil(t, runtime.engine)
	assert.NotNil(t, runtime.evaluator)
	assert.NotNil(t, runtime.backwarder)
	assert.NotNil(t, runtime.aggregator)
	assert.NotNil(t, runtime.optimizer)
}

func TestLiveRuntimeDoesNotRequireJudgeForNonLLMMetrics(t *testing.T) {
	t.Setenv(defaultAPIKeyEnv, "secret")
	dataDir := t.TempDir()
	writeRuntimeMetrics(t, dataDir, `[{
		"metricName":"quality",
		"evaluatorName":"final_response_avg_score",
		"criterion":{"finalResponse":{"text":{"matchStrategy":"exact"}}}
	}]`)
	cfg := validConfig()
	cfg.Mode = modeLive
	cfg.Candidate = validLiveRole()
	cfg.Worker = validLiveRole()

	runtime, err := buildRuntime(context.Background(), runtimeConfig{
		Config: cfg, DataDir: dataDir, OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	assert.False(t, runtime.judgeRequired)
	assert.Empty(t, runtime.ledger.errors())
}

func TestLiveRuntimeRequiresJudgeForLLMMetrics(t *testing.T) {
	t.Setenv(defaultAPIKeyEnv, "secret")
	dataDir := t.TempDir()
	writeRuntimeMetrics(t, dataDir, `[{
		"metricName":"quality",
		"evaluatorName":"llm_rubric_response",
		"criterion":{"llmJudge":{"rubrics":[{
			"id":"quality","content":{"text":"The answer is correct."}
		}]}}
	}]`)
	cfg := validConfig()
	cfg.Mode = modeLive
	cfg.Candidate = validLiveRole()
	cfg.Worker = validLiveRole()

	_, err := buildRuntime(context.Background(), runtimeConfig{
		Config: cfg, DataDir: dataDir, OutputDir: t.TempDir(),
	})
	require.ErrorContains(t, err, "judge model")
}

func TestRuntimeUsesSharedLLMMetricForEveryEvalSet(t *testing.T) {
	dataDir := t.TempDir()
	writeRuntimeMetrics(t, dataDir, `[{
		"metricName":"quality",
		"evaluatorName":"llm_rubric_response",
		"criterion":{"llmJudge":{"rubrics":[{
			"id":"quality","content":{"text":"The answer is correct."}
		}]}}
	}]`)

	runtime, err := buildRuntime(context.Background(), runtimeConfig{
		Config: validConfig(), DataDir: dataDir, OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	assert.True(t, runtime.judgeRequired)

	locator := &sharedMetricLocator{metricFileID: "metrics"}
	assert.Equal(t,
		locator.Build(dataDir, regressionAppName, "train"),
		locator.Build(dataDir, regressionAppName, "validation"),
	)
}

func TestLiveRoleRequiresExplicitEndpointForGenericCredential(t *testing.T) {
	t.Setenv("CUSTOM_KEY", "secret")
	_, err := newLiveModel("candidate", roleConfig{Model: "custom", APIKeyEnv: "CUSTOM_KEY"}, newLedger())
	require.ErrorContains(t, err, "base URL")
}

func TestRuntimeCloseIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	writeRuntimeMetrics(t, dataDir, `[{"metricName":"quality"}]`)
	runtime, err := buildRuntime(context.Background(), runtimeConfig{
		Config: validConfig(), DataDir: dataDir, OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, runtime.Close())
	require.NoError(t, runtime.Close())
}

func writeRuntimeMetrics(t *testing.T, dataDir, contents string) {
	t.Helper()
	dir := filepath.Join(dataDir, regressionAppName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "metrics.metrics.json"), []byte(contents), 0o600,
	))
}
