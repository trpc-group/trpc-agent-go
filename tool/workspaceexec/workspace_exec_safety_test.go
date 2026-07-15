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
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestExecToolSafetyScannerTruncatesOversizedOutput(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.MaxOutputBytes = 10
	tl := NewExecTool(localexec.New(), WithSafetyScanner(
		safety.NewScanner(policy),
	))
	out := tl.scanOutput(context.Background(), execOutput{
		Status: codeexecutor.ProgramStatusExited,
		Output: "0123456789x",
	})
	require.Len(t, out.Output, 10)
	require.Equal(t, "0123456789", out.Output)
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

func TestExecToolSafetyScannerAllowsConfiguredInteractiveStdin(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.InteractiveStdinAction = safety.DecisionAllow
	tl := NewExecTool(localexec.New(), WithSafetyScanner(
		safety.NewScanner(policy),
	))

	require.NoError(t, tl.checkStdinSafety(context.Background(), "session-1", "y", true))

	err := tl.checkStdinSafety(context.Background(), "session-1", "rm -rf /", true)
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-CMD-001", blocked.Report.PrimaryRuleID())
}

func TestExecToolSafetyScannerAllowsConfiguredInteractiveStdinWrite(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.ParseErrorAction = safety.DecisionAllow
	policy.InteractiveStdinAction = safety.DecisionAllow
	execTool := NewExecTool(localexec.New(), WithSafetyScanner(
		safety.NewScanner(policy),
	))
	writeTool := NewWriteStdinTool(execTool)

	startEnc, err := json.Marshal(execInput{
		Command:     "printf 'ready\\n'; read v; echo out:$v; read w; echo out2:$w",
		Background:  true,
		YieldTimeMS: intPtr(100),
		Timeout:     timeoutSecSmall,
	})
	require.NoError(t, err)
	startRes, err := execTool.Call(context.Background(), startEnc)
	require.NoError(t, err)
	started := startRes.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusRunning, started.Status)
	require.NotEmpty(t, started.SessionID)
	require.Contains(t, started.Output, "ready")

	writeEnc, err := json.Marshal(writeInput{
		SessionID:     started.SessionID,
		Chars:         "y",
		AppendNewline: boolPtr(true),
		YieldTimeMS:   intPtr(100),
	})
	require.NoError(t, err)
	var out execOutput
	require.Eventually(t, func() bool {
		res, err := writeTool.Call(context.Background(), writeEnc)
		if err != nil {
			return false
		}
		out = res.(execOutput)
		return strings.Contains(out.Output, "out:y")
	}, 3*time.Second, 20*time.Millisecond)

	badEnc, err := json.Marshal(writeInput{
		SessionID:     started.SessionID,
		Chars:         "rm -rf /",
		AppendNewline: boolPtr(true),
	})
	require.NoError(t, err)
	_, err = writeTool.Call(context.Background(), badEnc)
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-CMD-001", blocked.Report.PrimaryRuleID())

	doneEnc, err := json.Marshal(writeInput{
		SessionID:     started.SessionID,
		Chars:         "done",
		AppendNewline: boolPtr(true),
		YieldTimeMS:   intPtr(100),
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		res, err := writeTool.Call(context.Background(), doneEnc)
		if err != nil {
			return false
		}
		out = res.(execOutput)
		return out.Status == codeexecutor.ProgramStatusExited
	}, 3*time.Second, 20*time.Millisecond)
	require.Contains(t, out.Output, "out2:done")
}
