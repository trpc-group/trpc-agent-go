//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestExecToolSafetyScannerBlocksBeforeRun(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithSafetyScanner(
		safety.NewScanner(safety.DefaultPolicy()),
	))

	_, err := tl.Call(context.Background(), []byte(`{"command":"rm -rf /"}`))
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-CMD-001", blocked.Report.PrimaryRuleID())
}

func TestExecToolSafetyScannerRedactsOutput(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithSafetyScanner(
		safety.NewScanner(safety.DefaultPolicy()),
	))
	out := tl.scanOutput(context.Background(), execOutput{
		Status: codeexecutor.ProgramStatusExited,
		Output: "sk-abcdefghijklmnopqrstuvwxyz",
	})
	require.Contains(t, out.Output, "[REDACTED]")
	require.NotContains(t, out.Output, "sk-abcdefghijklmnopqrstuvwxyz")
}

func TestExecToolSafetyScannerBlocksWriteStdinBeforeRun(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithSafetyScanner(
		safety.NewScanner(safety.DefaultPolicy()),
	))

	err := tl.checkStdinSafety(context.Background(), "session-1", "rm -rf /", true)
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-CMD-001", blocked.Report.PrimaryRuleID())
}
