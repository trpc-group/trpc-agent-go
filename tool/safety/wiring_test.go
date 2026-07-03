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
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubTool is a minimal tool.Tool used to verify the wrapper behavior
// without depending on the hostexec or workspaceexec packages.
type stubTool struct {
	name      string
	desc      string
	lastArgs  []byte
	callCount int
}

func (s *stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name, Description: s.desc}
}

func (s *stubTool) Call(_ context.Context, args []byte) (any, error) {
	s.lastArgs = append([]byte(nil), args...)
	s.callCount++
	return map[string]string{"ok": "true"}, nil
}

// stubToolSet exposes a fixed list of stubTool values; it lets the
// WrapToolSet test exercise the full ToolSet interface without
// pulling in any executor package.
type stubToolSet struct {
	name  string
	tools []tool.Tool
}

func (s *stubToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s *stubToolSet) Close() error                      { return nil }
func (s *stubToolSet) Name() string                      { return s.name }

// TestWrapTool_AllowsSafeCommand verifies that a wrapped tool
// executes normally when the guard allows the command.
func TestWrapTool_AllowsSafeCommand(t *testing.T) {
	stub := &stubTool{name: "exec_command", desc: "stub"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTool(stub, guard)

	out, err := wrapped.Call(context.Background(), jsonCommandArgs("ls -la"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.callCount != 1 {
		t.Errorf("inner tool call count = %d, want 1", stub.callCount)
	}
	m, ok := out.(map[string]string)
	if !ok || m["ok"] != "true" {
		t.Errorf("expected inner output to flow through, got %v", out)
	}
}

// TestWrapTool_DeniesDangerousCommand verifies that a wrapped tool
// does NOT call the inner implementation when the guard denies, and
// that the result is a structured permission result the framework
// understands.
func TestWrapTool_DeniesDangerousCommand(t *testing.T) {
	stub := &stubTool{name: "exec_command", desc: "stub"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTool(stub, guard)

	out, err := wrapped.Call(context.Background(), jsonCommandArgs("rm -rf /"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.callCount != 0 {
		t.Errorf("inner tool must NOT be called on deny, got callCount=%d", stub.callCount)
	}
	res, ok := out.(tool.PermissionResult)
	if !ok {
		t.Fatalf("expected PermissionResult, got %T", out)
	}
	if res.Status != tool.PermissionResultStatusDenied {
		t.Errorf("status = %q, want %q", res.Status, tool.PermissionResultStatusDenied)
	}
	if res.Tool != "exec_command" {
		t.Errorf("tool = %q, want %q", res.Tool, "exec_command")
	}
	if res.Reason == "" {
		t.Error("reason should explain the deny")
	}
}

// TestWrapTool_AskResultEscalates checks that the wrapper honours
// DecisionAsk by emitting the approval-required result and skipping
// the inner tool.
func TestWrapTool_AskResultEscalates(t *testing.T) {
	stub := &stubTool{name: "exec_command", desc: "stub"}
	guard := NewGuard(WithRules(NewAskForReviewRule()))

	wrapped := WrapTool(stub, guard)

	out, err := wrapped.Call(context.Background(), jsonCommandArgs("git push origin main"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.callCount != 0 {
		t.Errorf("inner tool must NOT be called on ask, got callCount=%d", stub.callCount)
	}
	res, ok := out.(tool.PermissionResult)
	if !ok {
		t.Fatalf("expected PermissionResult, got %T", out)
	}
	if res.Status != tool.PermissionResultStatusApprovalRequired {
		t.Errorf("status = %q, want %q", res.Status, tool.PermissionResultStatusApprovalRequired)
	}
}

// TestWrapTool_NilGuardFallsThrough confirms a nil guard does not
// break callers that pass an optional guard.
func TestWrapTool_NilGuardFallsThrough(t *testing.T) {
	stub := &stubTool{name: "exec_command", desc: "stub"}

	wrapped := WrapTool(stub, nil)
	if wrapped != tool.Tool(stub) {
		t.Errorf("nil guard should return the original tool unchanged")
	}
}

// TestWrapTool_NilToolReturnsNil makes the wrapper's nil-safety
// contract explicit so a misuse is caught early.
func TestWrapTool_NilToolReturnsNil(t *testing.T) {
	if got := WrapTool(nil, NewGuard()); got != nil {
		t.Errorf("nil inner tool should return nil, got %T", got)
	}
}

// TestWrapTool_PreservesDeclaration verifies the wrapper exposes the
// inner tool's declaration so the model's tool schema is unchanged.
func TestWrapTool_PreservesDeclaration(t *testing.T) {
	stub := &stubTool{name: "exec_command", desc: "exec shell"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTool(stub, guard)
	d := wrapped.Declaration()
	if d == nil {
		t.Fatal("declaration should not be nil")
	}
	if d.Name != "exec_command" || d.Description != "exec shell" {
		t.Errorf("declaration was rewritten: %+v", d)
	}
}

// TestWrapTools_BatchGating ensures that wrapping a slice applies the
// guard to every entry and the underlying tools are still
// individually callable when allowed.
func TestWrapTools_BatchGating(t *testing.T) {
	stubs := []tool.Tool{
		&stubTool{name: "exec_command", desc: "exec"},
		&stubTool{name: "kill_session", desc: "kill"},
	}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTools(stubs, guard)
	if len(wrapped) != len(stubs) {
		t.Fatalf("length mismatch: got %d, want %d", len(wrapped), len(stubs))
	}
	for i, w := range wrapped {
		if w == nil {
			t.Errorf("wrapped[%d] should not be nil", i)
		}
	}
	// Verify one of the tools still works.
	wrappedCall, ok := wrapped[0].(tool.CallableTool)
	if !ok {
		t.Fatalf("wrapped[0] is not a CallableTool: %T", wrapped[0])
	}
	out, err := wrappedCall.Call(context.Background(), jsonCommandArgs("ls -la"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out.(map[string]string); !ok {
		t.Errorf("unexpected output shape: %T", out)
	}
}

// TestWrapToolSet_GuardsEveryTool demonstrates the documented
// integration path: a ToolSet (hostexec / workspaceexec) is wrapped
// and every tool it exposes is gated by the same guard.
func TestWrapToolSet_GuardsEveryTool(t *testing.T) {
	inner := &stubToolSet{
		name: "hostexec",
		tools: []tool.Tool{
			&stubTool{name: "exec_command", desc: "exec"},
		},
	}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	ts := WrapToolSet(inner, guard)
	if ts == nil {
		t.Fatal("wrapped tool set is nil")
	}
	if ts.Name() != "hostexec" {
		t.Errorf("name = %q, want %q", ts.Name(), "hostexec")
	}
	tools := ts.Tools(context.Background())
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	guarded, ok := tools[0].(*GuardedTool)
	if !ok {
		t.Fatalf("expected *GuardedTool, got %T", tools[0])
	}
	out, err := guarded.Call(context.Background(), jsonCommandArgs("rm -rf /"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out.(tool.PermissionResult); !ok {
		t.Errorf("expected PermissionResult on deny, got %T", out)
	}
	// The inner stub must NOT have been called.
	if guarded.inner.(*stubTool).callCount != 0 {
		t.Error("inner stub should not be called on deny")
	}
}

// TestWrapToolSet_NilInputsPassThrough checks the documented
// no-op contract for nil inputs.
func TestWrapToolSet_NilInputsPassThrough(t *testing.T) {
	if WrapToolSet(nil, NewGuard()) != nil {
		t.Error("nil tool set should return nil")
	}
	inner := &stubToolSet{name: "x", tools: nil}
	if WrapToolSet(inner, nil) != tool.ToolSet(inner) {
		t.Error("nil guard should return the original tool set")
	}
}

// TestGuardedTool_CallInnerErrorPropagates ensures errors from the
// inner tool still surface to the caller.
func TestGuardedTool_CallInnerErrorPropagates(t *testing.T) {
	stub := &errorStub{}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))
	wrapped := WrapTool(stub, guard)

	_, err := wrapped.Call(context.Background(), jsonCommandArgs("ls"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected inner error to propagate, got %v", err)
	}
}

// errorStub is a tool.Tool whose Call always returns a fixed error.
type errorStub struct{}

func (e *errorStub) Declaration() *tool.Declaration { return &tool.Declaration{Name: "x"} }
func (e *errorStub) Call(context.Context, []byte) (any, error) {
	return nil, errors.New("boom")
}
