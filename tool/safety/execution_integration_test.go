//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolretry"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
)

const (
	retryMaxAttempts = 3
	retryInterval    = time.Nanosecond
)

type canaryCodeExecutor struct {
	marker string
	calls  int
	result codeexecutor.CodeExecutionResult
}

func (executor *canaryCodeExecutor) ExecuteCode(
	_ context.Context,
	_ codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	executor.calls++
	if executor.marker != "" {
		if err := os.WriteFile(executor.marker, []byte("called"), 0o600); err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("write canary: %w", err)
		}
	}
	return executor.result, nil
}

func (*canaryCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestPermissionPolicyBlocksRealHostExecRequest(t *testing.T) {
	baseDir := t.TempDir()
	toolSet, err := hostexec.NewToolSet(hostexec.WithBaseDir(baseDir))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, toolSet.Close()) })
	inner := findToolByName(t, toolSet.Tools(context.Background()), "exec_command")
	guard, auditor := newWrapperGuard(t, nil)
	policy, err := NewPermissionPolicy(
		guard, BindHostExec("exec_command", baseDir),
	)
	require.NoError(t, err)

	decision, err := policy.CheckToolPermission(
		context.Background(), boundPermissionRequest(inner,
			[]byte(`{"command":"echo blocked > safety-canary.txt"}`)),
	)
	require.NoError(t, err)
	require.NotEqual(t, tool.PermissionActionAllow, decision.Action)
	require.Len(t, auditor.events, 1)
	require.True(t, auditor.events[0].Blocked)
}

func TestPermissionPolicyBlocksRealCodeExecBeforeExecutor(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "codeexec-canary.txt")
	executor := &canaryCodeExecutor{marker: marker}
	inner := codeexec.NewTool(executor)
	guard, auditor := newWrapperGuard(t, nil)
	policy, err := NewPermissionPolicy(
		guard, BindCodeExec("execute_code", BackendLocal),
	)
	require.NoError(t, err)

	decision, err := policy.CheckToolPermission(
		context.Background(), boundPermissionRequest(inner, []byte(
			`{"code_blocks":[{"language":"bash","code":"echo blocked > canary"}]}`,
		)),
	)
	require.NoError(t, err)
	require.NotEqual(t, tool.PermissionActionAllow, decision.Action)
	require.Zero(t, executor.calls)
	require.NoFileExists(t, marker)
	require.Len(t, auditor.events, 1)
}

func TestWrapOutputGuardWithholdsRealCodeExecInlineArtifact(t *testing.T) {
	executor := &canaryCodeExecutor{
		result: codeexecutor.CodeExecutionResult{
			OutputFiles: []codeexecutor.File{{
				Name: "artifact.json", Content: "api_key=top-secret-value",
				MIMEType: "application/json",
			}},
		},
	}
	inner := codeexec.NewTool(executor)
	guard, auditor := newWrapperGuard(t, nil)
	wrapper, err := WrapOutputGuard(
		guard, inner, BindCodeExec("execute_code", BackendLocal),
	)
	require.NoError(t, err)

	result, callErr := wrapper.(tool.CallableTool).Call(
		context.Background(), []byte(
			`{"code_blocks":[{"language":"bash","code":"echo ok"}]}`,
		),
	)
	require.NoError(t, callErr)
	require.Equal(t, ruleOutputSecret, result.(BlockedResult).RuleID)
	require.Equal(t, 1, executor.calls)
	require.Len(t, auditor.events, 1)
	require.Equal(t, ruleOutputSecret, auditor.events[0].RuleID)
}

func TestPostcheckBlockIsNeverRetried(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := newFakeCallable(map[string]string{
		"token": "ghp_abcdefghijklmnopqrstuvwxyz123456",
	})
	wrapper := wrapOutput(t, guard, inner)
	retryChecks := 0
	result := toolretry.Execute(context.Background(), toolretry.ExecuteInput{
		ToolName: workspaceToolName,
		Policy: &tool.RetryPolicy{
			MaxAttempts: retryMaxAttempts,
			RetryOn: func(context.Context, *tool.RetryInfo) (bool, error) {
				retryChecks++
				return true, nil
			},
		},
		Call: wrapper.Call,
		ResultError: func(result any) bool {
			marker, ok := result.(interface{ RetryResultError() bool })
			return ok && marker.RetryResultError()
		},
	})
	require.NoError(t, result.Error)
	require.IsType(t, BlockedResult{}, result.Result)
	require.Equal(t, 1, inner.calls)
	require.Zero(t, retryChecks)
}

func TestOrdinaryToolErrorRetainsRetryBehavior(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := newFakeCallable("ok")
	inner.call = func(context.Context, []byte) (any, error) {
		if inner.calls < retryMaxAttempts {
			return nil, errors.New("transient failure")
		}
		return inner.result, nil
	}
	wrapper := wrapOutput(t, guard, inner)
	retryChecks := 0
	result := toolretry.Execute(context.Background(), toolretry.ExecuteInput{
		ToolName: workspaceToolName,
		Policy: &tool.RetryPolicy{
			MaxAttempts: retryMaxAttempts, InitialInterval: retryInterval,
			RetryOn: func(context.Context, *tool.RetryInfo) (bool, error) {
				retryChecks++
				return true, nil
			},
		},
		Call: wrapper.Call,
	})
	require.NoError(t, result.Error)
	require.Equal(t, inner.result, result.Result)
	require.Equal(t, retryMaxAttempts, inner.calls)
	require.Equal(t, retryMaxAttempts-1, retryChecks)
}

func boundPermissionRequest(inner tool.Tool, arguments []byte) *tool.PermissionRequest {
	return &tool.PermissionRequest{
		Tool: inner, ToolName: inner.Declaration().Name,
		Declaration: inner.Declaration(), Arguments: append([]byte(nil), arguments...),
		Metadata: tool.MetadataOf(inner),
	}
}

func findToolByName(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, current := range tools {
		if current.Declaration() != nil && current.Declaration().Name == name {
			return current
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
