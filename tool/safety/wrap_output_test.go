//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestWrappedCallableRedactsStructuredToolOutput(t *testing.T) {
	guard, err := New(testPolicy(), withTrustedWorkspaceOutputLimit(4096))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	wrapped, err := WrapCallableTool(secretOutputTool{}, guard)
	if err != nil {
		t.Fatalf("WrapCallableTool() error = %v", err)
	}
	result, err := wrapped.Call(context.Background(), []byte(
		`{"command":"go test ./tool/safety","timeout_sec":10}`,
	))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, secret := range []string{"database-output-secret", "nested-output-token"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("wrapped tool output leaked %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), RedactedValue) {
		t.Fatalf("wrapped tool output has no redaction marker: %s", encoded)
	}
}

type secretOutputTool struct{}

func (secretOutputTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (secretOutputTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{OpenWorld: true, MaxResultSize: 4096}
}

func (secretOutputTool) Call(context.Context, []byte) (any, error) {
	return map[string]any{
		"DB_PASSWORD": "database-output-secret",
		"nested": map[string]any{
			"access_token": "nested-output-token",
		},
		"safe": "visible",
	}, nil
}
func TestWrappedCallableRedactsConcreteExecutionResult(t *testing.T) {
	guard, err := New(testPolicy(), withTrustedWorkspaceOutputLimit(4096))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	wrapped, err := WrapCallableTool(concreteSecretOutputTool{}, guard)
	if err != nil {
		t.Fatalf("WrapCallableTool() error = %v", err)
	}

	result, err := wrapped.Call(context.Background(), []byte(
		`{"command":"go test ./tool/safety","timeout_sec":10}`,
	))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	got, ok := result.(codeexecutor.CodeExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want codeexecutor.CodeExecutionResult", result)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, secret := range []string{"concrete-output-secret", "concrete-file-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("concrete tool output leaked %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), RedactedValue) {
		t.Fatalf("concrete tool output has no redaction marker: %s", encoded)
	}
}

type concreteSecretOutputTool struct{}

func (concreteSecretOutputTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (concreteSecretOutputTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{OpenWorld: true, MaxResultSize: 4096}
}

func (concreteSecretOutputTool) Call(context.Context, []byte) (any, error) {
	return codeexecutor.CodeExecutionResult{
		Output: "token=concrete-output-secret",
		OutputFiles: []codeexecutor.File{{
			Name: "result.txt", Content: "password=concrete-file-secret",
		}},
	}, nil
}

type nilCallableTool struct{}

func (*nilCallableTool) Declaration() *tool.Declaration {
	panic("typed-nil callable must not be invoked")
}

func (*nilCallableTool) Call(context.Context, []byte) (any, error) {
	panic("typed-nil callable must not be invoked")
}

func TestWrapCallableToolRejectsTypedNil(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var callable *nilCallableTool

	wrapped, err := WrapCallableTool(callable, guard)

	if err == nil || !strings.Contains(err.Error(), "callable tool is nil") {
		t.Fatalf("WrapCallableTool() error = %v, want typed-nil rejection", err)
	}
	if wrapped != nil {
		t.Fatalf("WrapCallableTool() result = %T, want nil", wrapped)
	}
}
