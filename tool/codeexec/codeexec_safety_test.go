// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package codeexec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type safetyTestExecutor struct {
	called bool
	result codeexecutor.CodeExecutionResult
}

func (e *safetyTestExecutor) ExecuteCode(context.Context, codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	e.called = true
	if e.result.Output != "" || len(e.result.OutputFiles) > 0 {
		return e.result, nil
	}
	return codeexecutor.CodeExecutionResult{Output: "executed"}, nil
}

func TestExecuteCodeTool_SafetyScannerSanitizesOutput(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.ResourceLimits.MaxOutputBytes = 32
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })
	exec := &safetyTestExecutor{result: codeexecutor.CodeExecutionResult{
		Output: "token=super-secret-value",
		OutputFiles: []codeexecutor.File{{
			Name: "result.txt", Content: strings.Repeat("x", 64), MIMEType: "text/plain",
		}},
	}}
	tl := NewTool(exec, WithSafetyScanner(scanner))
	got, err := tl.Call(context.Background(), []byte(`{
		"code_blocks":[{"language":"python","code":"print('ok')"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	result := got.(codeexecutor.CodeExecutionResult)
	visibleBytes := len(result.Output)
	for _, file := range result.OutputFiles {
		visibleBytes += len(file.Content)
	}
	if visibleBytes > 32 {
		t.Fatalf("visible output bytes = %d, want <= 32", visibleBytes)
	}
	if strings.Contains(result.Output, "super-secret-value") {
		t.Fatalf("output leaked secret: %q", result.Output)
	}
	if !result.OutputFiles[0].Truncated {
		t.Fatalf("output file was not marked truncated: %#v", result.OutputFiles[0])
	}
}

func TestExecuteCodeTool_SafetyScannerBlocksPolicyExcludedLanguage(t *testing.T) {
	scanner, err := safety.NewScanner(safety.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })
	exec := &safetyTestExecutor{}
	tl := NewTool(
		exec,
		WithLanguages("ruby"),
		WithSafetyScanner(scanner),
	)
	_, err = tl.Call(context.Background(), []byte(`{
		"code_blocks":[{"language":"ruby","code":"puts 'ok'"}]
	}`))
	if err == nil || !errors.Is(err, safety.ErrBlocked) ||
		!strings.Contains(err.Error(), safety.RuleCodeExecLanguage) {
		t.Fatalf("error = %v, want excluded language block", err)
	}
	if exec.called {
		t.Fatal("executor was called for policy-excluded language")
	}
}

func (e *safetyTestExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestExecuteCodeTool_SafetyScannerBlocksBeforeExecutor(t *testing.T) {
	scanner, err := safety.NewScanner(safety.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	exec := &safetyTestExecutor{}
	tl := NewTool(exec, WithSafetyScanner(scanner))
	_, err = tl.Call(context.Background(), []byte(`{
		"code_blocks":[{"language":"bash","code":"rm -rf /tmp/project"}]
	}`))
	if err == nil {
		t.Fatal("expected safety scanner to block code execution")
	}
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("error = %v, want ErrBlocked", err)
	}
	if !strings.Contains(err.Error(), safety.RuleDangerousDelete) {
		t.Fatalf("error = %v, want rule %s", err, safety.RuleDangerousDelete)
	}
	if exec.called {
		t.Fatal("executor was called despite blocked safety decision")
	}
}
