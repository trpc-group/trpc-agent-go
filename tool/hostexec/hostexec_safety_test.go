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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestHostExecSafetyScannerBlocksBeforeRun(t *testing.T) {
	execTool := &execCommandTool{safety: safety.NewScanner(safety.DefaultPolicy())}
	err := execTool.checkSafety(context.Background(), execInput{
		Command: "cat ~/.ssh/id_rsa",
	}, "")
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-PATH-001", blocked.Report.PrimaryRuleID())
}

func TestHostExecSafetyScannerScansResolvedWorkdir(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "protected")
	base := filepath.Join(root, "workspace", "nested")
	require.NoError(t, os.MkdirAll(base, 0o755))
	require.NoError(t, os.MkdirAll(protected, 0o755))

	policy := safety.DefaultPolicy()
	policy.DeniedPaths = []string{protected}
	set, err := NewToolSet(
		WithBaseDir(base),
		WithSafetyScanner(safety.NewScanner(policy)),
	)
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	_, err = execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command": "cat passwd",
		"workdir": "../../protected",
		"yieldMs": 0,
	}))
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-PATH-001", blocked.Report.PrimaryRuleID())
}

func TestHostExecSafetyScannerScansResolvedBaseDirWhenWorkdirEmpty(t *testing.T) {
	protected := t.TempDir()
	policy := safety.DefaultPolicy()
	policy.DeniedPaths = []string{protected}
	set, err := NewToolSet(
		WithBaseDir(protected),
		WithSafetyScanner(safety.NewScanner(policy)),
	)
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	_, err = execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command": "cat passwd",
		"yieldMs": 0,
	}))
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-PATH-001", blocked.Report.PrimaryRuleID())
}

func TestHostExecSafetyScannerAppliesEffectiveDefaultTimeout(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.DeniedPaths = []string{}
	scanner := safety.NewScanner(policy)
	execTool := &execCommandTool{safety: scanner}

	tests := []struct {
		name       string
		timeoutSec *int
	}{
		{name: "omitted"},
		{name: "zero", timeoutSec: intPtr(0)},
		{name: "negative", timeoutSec: intPtr(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := execTool.checkSafety(context.Background(), execInput{
				Command:    "echo ok",
				TimeoutSec: tt.timeoutSec,
			}, "/workspace")
			require.Error(t, err)
			var blocked *safety.BlockedError
			require.True(t, errors.As(err, &blocked), err)
			require.Equal(t, safety.DecisionAsk, blocked.Report.Decision)
			require.Equal(t, "TSG-RES-001", blocked.Report.PrimaryRuleID())
		})
	}
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

func TestHostExecSafetyScannerTruncatesOversizedOutput(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.MaxOutputBytes = 10
	execTool := &execCommandTool{safety: safety.NewScanner(policy)}
	out := execTool.scanExecOutput(context.Background(), execResult{
		Status: "exited",
		Output: "0123456789x",
	})
	require.Len(t, out["output"].(string), 10)
	require.Equal(t, "0123456789", out["output"])
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

func TestHostExecSafetyScannerAllowsConfiguredInteractiveStdin(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.InteractiveStdinAction = safety.DecisionAllow
	w := &writeStdinTool{safety: safety.NewScanner(policy)}

	require.NoError(t, w.checkSafety(context.Background(), "session-1", "y", true))

	err := w.checkSafety(context.Background(), "session-1", "rm -rf /", true)
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-CMD-001", blocked.Report.PrimaryRuleID())
}

func TestHostExecSafetyScannerAllowsConfiguredInteractiveStdinWrite(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	policy := safety.DefaultPolicy()
	policy.BackgroundAction = safety.DecisionAllow
	policy.InteractiveStdinAction = safety.DecisionAllow
	set, err := NewToolSet(
		WithJobTTL(10*time.Second),
		WithSafetyScanner(safety.NewScanner(policy)),
	)
	require.NoError(t, err)
	defer set.Close()

	execTool, writeTool, killTool, mgr := toolSetTools(t, set)
	out, err := execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command":     "cat",
		"background":  true,
		"timeout_sec": 5,
	}))
	require.NoError(t, err)
	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	writeOut, err := writeTool.Call(context.Background(), mustJSON(t, map[string]any{
		"session_id":     sessionID,
		"chars":          "y",
		"append_newline": true,
		"yieldMs":        100,
	}))
	require.NoError(t, err)
	output := outputField(writeOut.(map[string]any))
	if !strings.Contains(output, "y") {
		output += waitForOutputContains(t, mgr, sessionID, "y")
	}
	require.Contains(t, output, "y")

	_, err = writeTool.Call(context.Background(), mustJSON(t, map[string]any{
		"session_id":     sessionID,
		"chars":          "rm -rf /",
		"append_newline": true,
	}))
	require.Error(t, err)
	var blocked *safety.BlockedError
	require.True(t, errors.As(err, &blocked), err)
	require.Equal(t, safety.DecisionDeny, blocked.Report.Decision)
	require.Equal(t, "TSG-CMD-001", blocked.Report.PrimaryRuleID())

	_, _ = killTool.Call(context.Background(), mustJSON(t, map[string]any{
		"session_id": sessionID,
	}))
}
