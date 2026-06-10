//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/latencydiag"
)

func TestAppendLatencyDiagnosticsGatewayOptionDisabled(t *testing.T) {
	t.Parallel()

	opts := appendLatencyDiagnosticsGatewayOption(nil, t.TempDir(), false, false)
	require.Nil(t, opts)
}

func TestBuildLatencyDiagnosticsRunOptionResolver(t *testing.T) {
	t.Parallel()

	resolver := buildLatencyDiagnosticsRunOptionResolver(t.TempDir(), false)
	_, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)
	require.Len(t, runOpts, 2)

	cfg := agent.RunOptions{}
	for _, opt := range runOpts {
		opt(&cfg)
	}
	require.True(t, cfg.LatencyDiagnosticsEnabled)
	require.False(t, cfg.LatencyDiagnosticsEmitEvents)
}

func TestBuildLatencyDiagnosticsRunOptionResolverEvents(t *testing.T) {
	t.Parallel()

	resolver := buildLatencyDiagnosticsRunOptionResolver(t.TempDir(), true)
	_, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)
	require.Len(t, runOpts, 2)

	cfg := agent.RunOptions{}
	for _, opt := range runOpts {
		opt(&cfg)
	}
	require.True(t, cfg.LatencyDiagnosticsEnabled)
	require.True(t, cfg.LatencyDiagnosticsEmitEvents)
}

func TestBuildLatencyDiagnosticsRunOptionResolverStateOff(
	t *testing.T,
) {
	t.Parallel()

	stateDir := t.TempDir()
	require.NoError(t, latencydiag.SetEnabled(stateDir, false))

	resolver := buildLatencyDiagnosticsRunOptionResolver(stateDir, true)
	_, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)
	require.Nil(t, runOpts)
}
