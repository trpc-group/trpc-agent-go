//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testTimeout = 5 * time.Second
)

func TestSwarmRuntime_OnTransfer_MaxHandoffs(t *testing.T) {
	cfg := SwarmConfig{
		MaxHandoffs: 2,
		NodeTimeout: testTimeout,
	}
	rt := &swarmRuntime{cfg: cfg}

	_, err := rt.OnTransfer(context.Background(), "a", "b")
	require.NoError(t, err)
	_, err = rt.OnTransfer(context.Background(), "b", "c")
	require.NoError(t, err)
	_, err = rt.OnTransfer(context.Background(), "c", "d")
	require.Error(t, err)
}

func TestSwarmRuntime_OnTransfer_RepetitiveDetection(t *testing.T) {
	cfg := SwarmConfig{
		RepetitiveHandoffWindow:    3,
		RepetitiveHandoffMinUnique: 2,
	}
	rt := &swarmRuntime{cfg: cfg}

	_, err := rt.OnTransfer(context.Background(), "a", "x")
	require.NoError(t, err)
	_, err = rt.OnTransfer(context.Background(), "b", "x")
	require.NoError(t, err)
	_, err = rt.OnTransfer(context.Background(), "c", "x")
	require.ErrorIs(t, err, errRepetitiveHandoff)
}

func TestSwarmRuntime_OnTransfer_ReturnsNodeTimeout(t *testing.T) {
	cfg := SwarmConfig{
		NodeTimeout: testTimeout,
	}
	rt := &swarmRuntime{cfg: cfg}

	got, err := rt.OnTransfer(context.Background(), "a", "b")
	require.NoError(t, err)
	require.Equal(t, testTimeout, got)
}
