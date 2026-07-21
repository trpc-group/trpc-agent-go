//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type recordingCodeExecutor struct {
	calls    int
	inputs   []codeexecutor.CodeExecutionInput
	clean    bool
	limits   codeexecutor.ExecutionLimits
	limited  bool
	deadline bool
	result   codeexecutor.CodeExecutionResult
	err      error
}

func TestExecuteCodeToolSafetyGuardRedactsExecutorError(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	require.NoError(t, err)
	rawErr := errors.New("executor failed: password=supersecret")
	exec := &recordingCodeExecutor{err: rawErr}
	ct := NewTool(exec, WithSafetyGuard(guard))
	args := []byte(`{"code_blocks":[{"language":"python","code":"print('safe')"}]}`)

	result, err := ct.Call(context.Background(), args)
	require.Equal(t, codeexecutor.CodeExecutionResult{}, result)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "supersecret")
	require.Contains(t, err.Error(), "[REDACTED]")
}

func TestExecuteCodeToolSafetyGuardRedactsValidationResult(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	require.NoError(t, err)
	exec := &recordingCodeExecutor{}
	ct := NewTool(exec, WithSafetyGuard(guard))
	args := []byte(`{"code_blocks":[{"language":"password=supersecret","code":"print(1)"}]}`)

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	require.Zero(t, exec.calls)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "supersecret")
	require.Contains(t, string(encoded), "[REDACTED]")
}

func (e *recordingCodeExecutor) ExecuteCode(
	ctx context.Context,
	input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	e.calls++
	e.clean = codeexecutor.CleanExecutionEnv(ctx)
	e.limits, e.limited = codeexecutor.ExecutionLimitsFromContext(ctx)
	_, e.deadline = ctx.Deadline()
	e.inputs = append(e.inputs, input)
	return e.result, e.err
}

func TestExecuteCodeToolUsesMostRestrictiveRuntimeLimits(t *testing.T) {
	directPolicy := safety.DefaultPolicy()
	directPolicy.Profiles = map[string]safety.ToolProfile{"execute_code": {
		MaxTimeout: safety.Duration(2 * time.Minute), MaxOutputBytes: 1 << 20,
	}}
	direct, err := safety.NewGuard(directPolicy)
	require.NoError(t, err)
	invocationPolicy := safety.DefaultPolicy()
	invocationPolicy.Profiles = map[string]safety.ToolProfile{"execute_code": {
		MaxTimeout: safety.Duration(30 * time.Millisecond), MaxOutputBytes: 5,
	}}
	invocation, err := safety.NewGuard(invocationPolicy)
	require.NoError(t, err)
	exec := &recordingCodeExecutor{result: codeexecutor.CodeExecutionResult{
		Output:      "abcdefgh",
		OutputFiles: []codeexecutor.File{{Content: "ijkl", MIMEType: "text/plain"}},
	}}
	ct := NewTool(exec, WithSafetyGuard(direct))
	ctx := tool.WithPermissionPolicyContext(context.Background(), invocation)
	result, err := ct.Call(ctx, []byte(`{"code_blocks":[{"language":"python","code":"print('safe')"}]}`))
	require.NoError(t, err)
	require.True(t, exec.clean)
	require.True(t, exec.limited)
	require.True(t, exec.deadline)
	require.Equal(t, 30*time.Millisecond, exec.limits.MaxTimeout)
	require.EqualValues(t, 5, exec.limits.MaxOutputBytes)
	safe := result.(codeexecutor.CodeExecutionResult)
	require.Equal(t, "abcde", safe.Output)
	require.Empty(t, safe.OutputFiles[0].Content)
	require.True(t, safe.OutputFiles[0].Truncated)
}

func TestExecuteCodeToolSafetyGuardRequestsCleanEnvironment(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	require.NoError(t, err)
	args := []byte(`{"code_blocks":[{"language":"python","code":"print('safe')"}]}`)
	for _, tc := range []struct {
		name string
		tool *executeCodeTool
		ctx  context.Context
	}{
		{
			name: "direct guard",
			tool: NewTool(&recordingCodeExecutor{}, WithSafetyGuard(guard)).(*executeCodeTool),
			ctx:  context.Background(),
		},
		{
			name: "framework guard",
			tool: NewTool(&recordingCodeExecutor{}).(*executeCodeTool),
			ctx:  tool.WithPermissionPolicyContext(context.Background(), guard),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := tc.tool.executor.(*recordingCodeExecutor)
			_, err := tc.tool.Call(tc.ctx, args)
			require.NoError(t, err)
			require.True(t, exec.clean)
		})
	}
}

func (*recordingCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestExecuteCodeToolSafetyGuardBlocksDecodedCode(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	require.NoError(t, err)

	tests := []struct {
		name       string
		code       string
		wantStatus string
	}{
		{
			name:       "deny dangerous code",
			code:       "rm -rf /",
			wantStatus: tool.PermissionResultStatusDenied,
		},
		{
			name:       "ask before network code",
			code:       "curl https://example.com",
			wantStatus: tool.PermissionResultStatusApprovalRequired,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exec := &recordingCodeExecutor{}
			ct := NewTool(exec, WithSafetyGuard(guard))
			inner, err := json.Marshal([]codeexecutor.CodeBlock{{
				Language: "bash",
				Code:     tc.code,
			}})
			require.NoError(t, err)
			args, err := json.Marshal(map[string]any{"code_blocks": string(inner)})
			require.NoError(t, err)

			result, err := ct.Call(context.Background(), args)
			require.NoError(t, err)
			require.Equal(t, 0, exec.calls)
			permissionResult, ok := result.(tool.PermissionResult)
			require.True(t, ok, "result type = %T", result)
			require.Equal(t, tc.wantStatus, permissionResult.Status)
		})
	}
}

func TestExecuteCodeToolSafetyGuardExecutesDecodedSafeCode(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	require.NoError(t, err)
	exec := &recordingCodeExecutor{result: codeexecutor.CodeExecutionResult{Output: "ok"}}
	ct := NewTool(exec, WithSafetyGuard(guard))
	args := []byte(`{"code_blocks":"[{\"language\":\"python\",\"code\":\"print(1)\"}]","execution_id":"job-1"}`)

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, 1, exec.calls)
	require.Equal(t, "job-1", exec.inputs[0].ExecutionID)
	require.Equal(t, "print(1)", exec.inputs[0].CodeBlocks[0].Code)
	require.Equal(t, codeexecutor.CodeExecutionResult{Output: "ok"}, result)
}

func TestExecuteCodeToolSafetyGuardScansOnlyEffectiveInput(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	require.NoError(t, err)
	exec := &recordingCodeExecutor{result: codeexecutor.CodeExecutionResult{Output: "ok"}}
	ct := NewTool(exec, WithSafetyGuard(guard))
	args := []byte(`{
		"code_blocks":{"language":"python","code":"print('safe')"},
		"ignored_by_executor":"rm -rf /"
	}`)

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, 1, exec.calls)
	require.Equal(t, "print('safe')", exec.inputs[0].CodeBlocks[0].Code)
	require.Equal(t, codeexecutor.CodeExecutionResult{Output: "ok"}, result)
}

func TestExecuteCodeToolSafetyGuardRedactsAllResultContent(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	require.NoError(t, err)
	exec := &recordingCodeExecutor{result: codeexecutor.CodeExecutionResult{
		Output: "api_key=supersecret",
		OutputFiles: []codeexecutor.File{{
			Name:     "result.txt",
			Content:  "ghp_1234567890123456",
			MIMEType: "text/plain",
		}},
	}}
	ct := NewTool(exec, WithSafetyGuard(guard))
	args := []byte(`{"code_blocks":[{"language":"python","code":"print('safe')"}]}`)

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, 1, exec.calls)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	text := string(encoded)
	require.NotContains(t, text, "supersecret")
	require.NotContains(t, text, "ghp_1234567890123456")
	require.GreaterOrEqual(t, strings.Count(text, "[REDACTED]"), 2)
}

func TestExecuteCodeToolWithoutSafetyGuardKeepsExistingBehavior(t *testing.T) {
	exec := &recordingCodeExecutor{result: codeexecutor.CodeExecutionResult{
		Output: "api_key=supersecret",
	}}
	ct := NewTool(exec)
	args := []byte(`{"code_blocks":[{"language":"python","code":"print('safe')"}]}`)

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, 1, exec.calls)
	require.Equal(t, exec.result, result)
}

type failingAuditSink struct{}

func (failingAuditSink) WriteAudit(context.Context, safety.AuditEvent) error {
	return context.Canceled
}

func TestExecuteCodeToolSafetyGuardAuditFailureFailsClosed(t *testing.T) {
	guard, err := safety.NewDefaultGuard(safety.WithAuditSink(failingAuditSink{}))
	require.NoError(t, err)
	exec := &recordingCodeExecutor{}
	ct := NewTool(exec, WithSafetyGuard(guard))
	args := []byte(`{"code_blocks":[{"language":"python","code":"print('safe')"}]}`)

	result, err := ct.Call(context.Background(), args)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 0, exec.calls)
}

type failAfterFirstAuditSink struct{ calls int }

func (s *failAfterFirstAuditSink) WriteAudit(context.Context, safety.AuditEvent) error {
	s.calls++
	if s.calls > 1 {
		return context.Canceled
	}
	return nil
}

func TestExecuteCodeToolSafetyGuardRedactionAuditFailureSuppressesResult(t *testing.T) {
	sink := &failAfterFirstAuditSink{}
	guard, err := safety.NewDefaultGuard(safety.WithAuditSink(sink))
	require.NoError(t, err)
	exec := &recordingCodeExecutor{result: codeexecutor.CodeExecutionResult{
		Output: "api_key=supersecret",
	}}
	ct := NewTool(exec, WithSafetyGuard(guard))
	args := []byte(`{"code_blocks":[{"language":"python","code":"print('safe')"}]}`)

	result, err := ct.Call(context.Background(), args)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 1, exec.calls)
	require.Equal(t, 2, sink.calls)
}
