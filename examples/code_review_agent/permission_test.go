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
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCommandGateDecisions(t *testing.T) {
	gate := NewCommandGate()
	allow, err := gate.Check(context.Background(), "task", "go test ./...")
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, allow.Action)
	deny, err := gate.Check(context.Background(), "task", "rm -rf /")
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, deny.Action)
	ask, err := gate.Check(context.Background(), "task", "go get example.com/x")
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, ask.Action)
	require.Len(t, gate.Records(), 3)
}

func TestFakeSandboxDeniedCommandDoesNotExecute(t *testing.T) {
	gate := NewCommandGate()
	r := &fakeRunner{runtime: "fake", outputLimit: 100}
	runs, err := r.Run(context.Background(), "task", []string{"curl http://example.com"}, gate)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "deny", runs[0].Status)
	require.Empty(t, runs[0].Output)
}

func TestFakeSandboxTimeoutAndOutputCap(t *testing.T) {
	gate := NewCommandGate()
	r := &fakeRunner{runtime: "fake", outputLimit: 100, timeout: true}
	runs, err := r.Run(context.Background(), "task", []string{"go test ./..."}, gate)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.True(t, runs[0].TimedOut)
	require.Equal(t, "timeout", runs[0].ErrorType)

	out, truncated := limitOutput("abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz", 40)
	require.True(t, truncated)
	require.Contains(t, out, "[output truncated]")
}
