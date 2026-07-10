//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestHostExecSafetyScannerBlocksBeforeRun(t *testing.T) {
	execTool := &execCommandTool{safety: safety.NewScanner(safety.DefaultPolicy())}
	err := execTool.checkSafety(context.Background(), execInput{
		Command: "cat ~/.ssh/id_rsa",
	})
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-PATH-001", blocked.Report.PrimaryRuleID())
}

func TestHostExecSafetyScannerRedactsOutput(t *testing.T) {
	execTool := &execCommandTool{safety: safety.NewScanner(safety.DefaultPolicy())}
	out := execTool.scanExecOutput(context.Background(), execResult{
		Status: "exited",
		Output: "sk-abcdefghijklmnopqrstuvwxyz",
	})
	require.Contains(t, out["output"], "[REDACTED]")
	require.NotContains(t, out["output"], "sk-abcdefghijklmnopqrstuvwxyz")
}

func TestHostExecSafetyScannerBlocksWriteStdinBeforeRun(t *testing.T) {
	w := &writeStdinTool{safety: safety.NewScanner(safety.DefaultPolicy())}
	err := w.checkSafety(context.Background(), "session-1", "cat ~/.ssh/id_rsa", true)
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-PATH-001", blocked.Report.PrimaryRuleID())
}
