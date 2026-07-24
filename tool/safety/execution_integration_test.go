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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
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
		_ = os.WriteFile(executor.marker, []byte("called"), 0o600)
	}
	return executor.result, nil
}

func (*canaryCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestWrapExecutionBlocksRealHostExecBeforeCanary(t *testing.T) {
	baseDir := t.TempDir()
	marker := filepath.Join(baseDir, "safety-canary.txt")
	toolSet, err := hostexec.NewToolSet(hostexec.WithBaseDir(baseDir))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, toolSet.Close()) })
	inner := findToolByName(t, toolSet.Tools(context.Background()), "exec_command")
	control := filepath.Join(baseDir, "control-canary.txt")
	_, err = inner.(tool.CallableTool).Call(
		context.Background(),
		[]byte(`{"command":"echo control > control-canary.txt"}`),
	)
	require.NoError(t, err)
	require.FileExists(t, control)
	guard, auditor := newWrapperGuard(t, nil)
	wrapper, err := WrapExecution(guard, inner, BindHostExec("exec_command", baseDir))
	require.NoError(t, err)
	callable := wrapper.(tool.CallableTool)

	result, callErr := callable.Call(
		context.Background(),
		[]byte(`{"command":"echo blocked > safety-canary.txt"}`),
	)
	require.Nil(t, result)
	require.Error(t, callErr)
	require.NoFileExists(t, marker)
	require.Len(t, auditor.events, 1)
	require.True(t, auditor.events[0].Blocked)
}

func TestWrapExecutionBlocksRealCodeExecBeforeExecutor(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "codeexec-canary.txt")
	executor := &canaryCodeExecutor{marker: marker}
	inner := codeexec.NewTool(executor)
	guard, auditor := newWrapperGuard(t, nil)
	wrapper, err := WrapExecution(
		guard, inner, BindCodeExec("execute_code", BackendLocal),
	)
	require.NoError(t, err)
	callable := wrapper.(tool.CallableTool)

	result, callErr := callable.Call(context.Background(), []byte(
		`{"code_blocks":[{"language":"bash","code":"echo blocked > canary"}]}`,
	))
	require.Nil(t, result)
	require.Error(t, callErr)
	require.Zero(t, executor.calls)
	require.NoFileExists(t, marker)
	require.Len(t, auditor.events, 1)
	require.True(t, auditor.events[0].Blocked)
}

func TestWrapExecutionWithholdsRealCodeExecInlineArtifact(t *testing.T) {
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
	wrapper, err := WrapExecution(
		guard, inner, BindCodeExec("execute_code", BackendLocal),
	)
	require.NoError(t, err)
	callable := wrapper.(tool.CallableTool)

	result, callErr := callable.Call(context.Background(), []byte(
		`{"code_blocks":[{"language":"bash","code":"echo ok"}]}`,
	))
	require.Nil(t, result)
	require.Error(t, callErr)
	require.NotContains(t, callErr.Error(), "top-secret-value")
	require.Equal(t, 1, executor.calls)
	require.Len(t, auditor.events, 2)
	require.Equal(t, "SECRET_IN_TOOL_OUTPUT", auditor.events[1].RuleID)
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
