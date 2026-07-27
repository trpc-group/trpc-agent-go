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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeterministicRuntimeUsesRealPromptIterStages(t *testing.T) {
	runtime, err := buildRuntime(context.Background(), runtimeConfig{
		Config:    validConfig(),
		DataDir:   t.TempDir(),
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

func TestLiveRoleRequiresExplicitEndpointForGenericCredential(t *testing.T) {
	t.Setenv("CUSTOM_KEY", "secret")
	_, err := newLiveModel("candidate", roleConfig{Model: "custom", APIKeyEnv: "CUSTOM_KEY"}, newLedger())
	require.ErrorContains(t, err, "base URL")
}

func TestRuntimeCloseIsIdempotent(t *testing.T) {
	runtime, err := buildRuntime(context.Background(), runtimeConfig{
		Config: validConfig(), DataDir: t.TempDir(), OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, runtime.Close())
	require.NoError(t, runtime.Close())
}
