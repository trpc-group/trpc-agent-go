//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestWrapCallableToolBlocksBeforeExecutionAndAudits(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = []string{"go", "npm", "rm"}
	policy.Commands.Denied = []string{"rm"}
	policy.Commands.Review = []string{"npm"}
	policy.Environment.DeniedVariables = append(
		policy.Environment.DeniedVariables,
		"OPENAI_API_KEY",
	)

	var audit bytes.Buffer
	guard, err := New(policy, WithAuditSink(NewJSONLAuditSink(&audit)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fake := &recordingExecutionTool{}
	wrapped, err := WrapCallableTool(fake, guard)
	if err != nil {
		t.Fatalf("WrapCallableTool() error = %v", err)
	}

	result, err := wrapped.Call(context.Background(), []byte(
		`{"command":"go test ./tool/safety","timeout_sec":10}`,
	))
	if err != nil {
		t.Fatalf("safe Call() error = %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("safe call count = %d, want 1", fake.calls)
	}
	if result != "executed" {
		t.Fatalf("safe result = %#v, want executed", result)
	}

	result, err = wrapped.Call(context.Background(), []byte(
		`{"command":"rm -rf /","timeout_sec":10}`,
	))
	if err != nil {
		t.Fatalf("denied Call() error = %v", err)
	}
	assertPermissionResult(t, result, tool.PermissionResultStatusDenied)
	if fake.calls != 1 {
		t.Fatalf("denied request executed tool: call count = %d", fake.calls)
	}

	result, err = wrapped.Call(context.Background(), []byte(
		`{"command":"npm install left-pad","timeout_sec":10}`,
	))
	if err != nil {
		t.Fatalf("review Call() error = %v", err)
	}
	assertPermissionResult(t, result, tool.PermissionResultStatusApprovalRequired)
	if fake.calls != 1 {
		t.Fatalf("ask request executed tool: call count = %d", fake.calls)
	}

	const secret = "sk-wrapper-secret-must-not-leak"
	result, err = wrapped.Call(context.Background(), []byte(
		`{"command":"go test ./tool/safety","env":{"OPENAI_API_KEY":"`+secret+`"}}`,
	))
	if err != nil {
		t.Fatalf("secret Call() error = %v", err)
	}
	assertPermissionResult(t, result, tool.PermissionResultStatusDenied)
	if fake.calls != 1 {
		t.Fatalf("secret request executed tool: call count = %d", fake.calls)
	}

	if strings.Contains(audit.String(), secret) {
		t.Fatal("audit contains a plaintext secret")
	}
	if lines := strings.Count(strings.TrimSpace(audit.String()), "\n") + 1; lines != 4 {
		t.Fatalf("audit lines = %d, want 4", lines)
	}
}

func TestWrapCallableToolRejectsInvalidInputs(t *testing.T) {
	guard, err := New(DefaultPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := WrapCallableTool(nil, guard); err == nil {
		t.Fatal("WrapCallableTool(nil, guard) succeeded")
	}
	if _, err := WrapCallableTool(&recordingExecutionTool{}, nil); err == nil {
		t.Fatal("WrapCallableTool(tool, nil) succeeded")
	}
}

func assertPermissionResult(t *testing.T, value any, wantStatus string) {
	t.Helper()
	result, ok := value.(tool.PermissionResult)
	if !ok {
		t.Fatalf("permission result type = %T, want tool.PermissionResult", value)
	}
	if result.Status != wantStatus {
		t.Fatalf("permission status = %q, want %q", result.Status, wantStatus)
	}
}

type recordingExecutionTool struct {
	calls int
}

func (t *recordingExecutionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (t *recordingExecutionTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{
		OpenWorld:     true,
		MaxResultSize: 4096,
	}
}

func (t *recordingExecutionTool) Call(context.Context, []byte) (any, error) {
	t.calls++
	return "executed", nil
}
