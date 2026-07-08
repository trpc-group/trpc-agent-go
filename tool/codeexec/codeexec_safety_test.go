// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package codeexec

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type safetyTestExecutor struct {
	called bool
}

func (e *safetyTestExecutor) ExecuteCode(context.Context, codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	e.called = true
	return codeexecutor.CodeExecutionResult{Output: "executed"}, nil
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
	if !strings.Contains(err.Error(), safety.RuleDangerousDelete) {
		t.Fatalf("error = %v, want rule %s", err, safety.RuleDangerousDelete)
	}
	if exec.called {
		t.Fatal("executor was called despite blocked safety decision")
	}
}
