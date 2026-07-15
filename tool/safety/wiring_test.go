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

// TestWrapTool_WithGuardedExtractor verifies that WithGuardedExtractor
// overrides the extractor for a single wrapped tool.
func TestWrapTool_WithGuardedExtractor(t *testing.T) {
	stub := &stubTool{name: "custom_tool", desc: "custom"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	extractorCalled := false
	customFn := func(args []byte) ScanInput {
		extractorCalled = true
		var raw map[string]string
		json.Unmarshal(args, &raw)
		return ScanInput{Command: raw["cmd"], ExecutorType: "local"}
	}

	wrapped := WrapTool(stub, guard, WithGuardedExtractor(customFn))

	// Send args with "cmd" field (not "command") — safe command so inner tool runs.
	args, _ := json.Marshal(map[string]string{"cmd": "ls"})
	out, err := wrapped.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !extractorCalled {
		t.Error("custom extractor was not invoked")
	}
	// Inner tool should have been called (safe command).
	if stub.callCount != 1 {
		t.Errorf("inner tool call count = %d, want 1", stub.callCount)
	}
	_ = out
}

// TestGuardedToolSet_Close verifies that Close forwards to the inner ToolSet.
func TestGuardedToolSet_Close(t *testing.T) {
	inner := &stubToolSet{name: "test_ts", tools: nil}
	guard := NewGuard()
	ts := WrapToolSet(inner, guard)
	if err := ts.Close(); err != nil {
		t.Errorf("Close should succeed, got %v", err)
	}
}

// TestGuardedTool_Declaration_Nil verifies that a nil GuardedTool
// returns nil from Declaration().
func TestGuardedTool_Declaration_Nil(t *testing.T) {
	var gt *GuardedTool
	if d := gt.Declaration(); d != nil {
		t.Errorf("nil GuardedTool should return nil declaration, got %+v", d)
	}
}

// declOnlyTool is a minimal tool.Tool that does NOT implement CallableTool.
type declOnlyTool struct {
	name string
	desc string
}

func (d *declOnlyTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: d.name, Description: d.desc}
}

// TestWrapTools_NonCallableTool passes a non-CallableTool tool through unchanged.
func TestWrapTools_NonCallableTool(t *testing.T) {
	dt := &declOnlyTool{name: "decl_only", desc: "decl"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))
	wrapped := WrapTools([]tool.Tool{dt}, guard)
	if len(wrapped) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(wrapped))
	}
	// Non-callable tools should pass through unchanged.
	if wrapped[0] != tool.Tool(dt) {
		t.Error("non-callable tool should pass through unchanged")
	}
}

// TestWrapTools_NilGuardPassesThrough confirms nil guard returns original slice.
func TestWrapTools_NilGuardPassesThrough(t *testing.T) {
	stubs := []tool.Tool{&stubTool{name: "t1", desc: "d1"}}
	got := WrapTools(stubs, nil)
	if len(got) != len(stubs) || got[0] != stubs[0] {
		t.Error("nil guard should return original slice")
	}
}

// TestWrapTools_EmptySlicePassesThrough confirms empty/nil slice returns unchanged.
func TestWrapTools_EmptySlicePassesThrough(t *testing.T) {
	guard := NewGuard()
	got := WrapTools(nil, guard)
	if got != nil {
		t.Errorf("nil slice should return nil, got %v", got)
	}
}

// TestGuardedTool_Call_WithExtractor_Deny verifies that a custom extractor
// path correctly denies dangerous commands.
func TestGuardedTool_Call_WithExtractor_Deny(t *testing.T) {
	stub := &stubTool{name: "exec_command", desc: "stub"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	customFn := func(args []byte) ScanInput {
		var raw map[string]string
		json.Unmarshal(args, &raw)
		return ScanInput{Command: raw["script"], ExecutorType: "local"}
	}

	wrapped := WrapTool(stub, guard, WithGuardedExtractor(customFn))
	args, _ := json.Marshal(map[string]string{"script": "rm -rf /"})
	out, err := wrapped.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.callCount != 0 {
		t.Error("inner tool should not be called on deny")
	}
	pr, ok := out.(tool.PermissionResult)
	if !ok {
		t.Fatalf("expected PermissionResult, got %T", out)
	}
	if pr.Status != tool.PermissionResultStatusDenied {
		t.Errorf("expected denied, got %s", pr.Status)
	}
}

// TestGuardedTool_Call_WithExtractor_Ask verifies the ask path through custom extractor.
func TestGuardedTool_Call_WithExtractor_Ask(t *testing.T) {
	stub := &stubTool{name: "exec_command", desc: "stub"}
	guard := NewGuard(WithRules(NewAskForReviewRule()))

	customFn := func(args []byte) ScanInput {
		var raw map[string]string
		json.Unmarshal(args, &raw)
		return ScanInput{Command: raw["op"], ExecutorType: "local"}
	}

	wrapped := WrapTool(stub, guard, WithGuardedExtractor(customFn))
	args, _ := json.Marshal(map[string]string{"op": "rm -r ./build"})
	out, err := wrapped.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr, ok := out.(tool.PermissionResult)
	if !ok {
		t.Fatalf("expected PermissionResult, got %T", out)
	}
	if pr.Status != tool.PermissionResultStatusApprovalRequired {
		t.Errorf("expected approval-required, got %s", pr.Status)
	}
}

// TestGuardedTool_Call_NilDeclaration exercises the path where
// the inner tool's Declaration returns nil.
func TestGuardedTool_Call_NilDeclaration(t *testing.T) {
	// stubTool returns a non-nil Declaration, so use a custom stub here.
	stub := &nilDeclTool{}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTool(stub, guard)
	out, err := wrapped.Call(context.Background(), jsonCommandArgs("ls"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out.(map[string]string); !ok {
		t.Errorf("expected inner output, got %T", out)
	}
}

// nilDeclTool returns nil from Declaration().
type nilDeclTool struct{}

func (n *nilDeclTool) Declaration() *tool.Declaration { return nil }
func (n *nilDeclTool) Call(_ context.Context, _ []byte) (any, error) {
	return map[string]string{"ok": "nil_decl"}, nil
}

// streamableStub is a streamable-only tool (no Call method).
type streamableStub struct {
	name        string
	streamCount int
	lastArgs    []byte
}

func (s *streamableStub) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}

func (s *streamableStub) StreamableCall(_ context.Context, args []byte) (*tool.StreamReader, error) {
	s.lastArgs = append([]byte(nil), args...)
	s.streamCount++
	return tool.NewStream(1).Reader, nil
}

// combinedStub implements both CallableTool and StreamableTool.
type combinedStub struct {
	name        string
	callCount   int
	streamCount int
	lastArgs    []byte
}

func (s *combinedStub) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}

func (s *combinedStub) Call(_ context.Context, args []byte) (any, error) {
	s.lastArgs = append([]byte(nil), args...)
	s.callCount++
	return map[string]string{"ok": "call"}, nil
}

func (s *combinedStub) StreamableCall(_ context.Context, args []byte) (*tool.StreamReader, error) {
	s.lastArgs = append([]byte(nil), args...)
	s.streamCount++
	return tool.NewStream(1).Reader, nil
}

// permissionCheckerStub implements tool.PermissionChecker and denies everything.
type permissionCheckerStub struct {
	stubTool
	checked bool
}

func (s *permissionCheckerStub) CheckPermission(_ context.Context, _ *tool.PermissionRequest) (tool.PermissionDecision, error) {
	s.checked = true
	return tool.DenyPermission("inner permission checker denied"), nil
}

// metadataStub publishes ToolMetadata.
type metadataStub struct {
	stubTool
	meta tool.ToolMetadata
}

func (s *metadataStub) ToolMetadata() tool.ToolMetadata { return s.meta }

func TestWrapTools_StreamableOnlyToolIsGuarded(t *testing.T) {
	inner := &streamableStub{name: "stream_only"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTools([]tool.Tool{inner}, guard)
	if len(wrapped) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(wrapped))
	}
	st, ok := wrapped[0].(tool.StreamableTool)
	if !ok {
		t.Fatalf("expected StreamableTool, got %T", wrapped[0])
	}
	_, err := st.StreamableCall(context.Background(), jsonCommandArgs("rm -rf /"))
	if err == nil {
		t.Fatal("expected deny error for dangerous streamable command")
	}
	if inner.streamCount != 0 {
		t.Errorf("inner streamable tool must NOT be called on deny, got %d", inner.streamCount)
	}
}

func TestWrapTools_CombinedToolPreservesBothPaths(t *testing.T) {
	inner := &combinedStub{name: "combined"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTools([]tool.Tool{inner}, guard)
	if len(wrapped) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(wrapped))
	}
	ct, callable := wrapped[0].(tool.CallableTool)
	st, streamable := wrapped[0].(tool.StreamableTool)
	if !callable || !streamable {
		t.Fatalf("expected combined wrapper to implement both CallableTool and StreamableTool, got callable=%v streamable=%v", callable, streamable)
	}

	// Deny path on Call.
	out, err := ct.Call(context.Background(), jsonCommandArgs("rm -rf /"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out.(tool.PermissionResult); !ok {
		t.Errorf("expected PermissionResult on deny, got %T", out)
	}
	if inner.callCount != 0 {
		t.Errorf("inner Call must not run on deny, got %d", inner.callCount)
	}

	// Deny path on StreamableCall.
	_, err = st.StreamableCall(context.Background(), jsonCommandArgs("rm -rf /"))
	if err == nil {
		t.Fatal("expected deny error for dangerous streamable command")
	}
	if inner.streamCount != 0 {
		t.Errorf("inner StreamableCall must not run on deny, got %d", inner.streamCount)
	}

	// Allow path on Call.
	_, _ = ct.Call(context.Background(), jsonCommandArgs("ls"))
	if inner.callCount != 1 {
		t.Errorf("inner Call should run on allow, got %d", inner.callCount)
	}
}

func TestWrapTool_CombinedToolReturnsCombinedWrapper(t *testing.T) {
	inner := &combinedStub{name: "combined"}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTool(inner, guard)
	if _, ok := wrapped.(*GuardedCombinedTool); !ok {
		t.Fatalf("expected *GuardedCombinedTool, got %T", wrapped)
	}
}

func TestGuardedTool_PreservesPermissionChecker(t *testing.T) {
	inner := &permissionCheckerStub{stubTool: stubTool{name: "permissioned"}}
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	wrapped := WrapTool(inner, guard).(*GuardedTool)
	checker, ok := tool.Tool(wrapped).(tool.PermissionChecker)
	if !ok {
		t.Fatal("wrapped tool should implement PermissionChecker")
	}
	dec, err := checker.CheckPermission(context.Background(), &tool.PermissionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("expected inner checker denial, got %s", dec.Action)
	}
	if !inner.checked {
		t.Error("inner CheckPermission was not called")
	}
}

func TestGuardedTool_PreservesToolMetadata(t *testing.T) {
	inner := &metadataStub{
		stubTool: stubTool{name: "meta_tool"},
		meta:     tool.ToolMetadata{ReadOnly: true, Destructive: true},
	}
	guard := NewGuard()

	wrapped := WrapTool(inner, guard).(*GuardedTool)
	provider, ok := tool.Tool(wrapped).(tool.MetadataProvider)
	if !ok {
		t.Fatal("wrapped tool should implement MetadataProvider")
	}
	got := provider.ToolMetadata()
	if got != inner.meta {
		t.Errorf("metadata not preserved: got %+v, want %+v", got, inner.meta)
	}
}

func TestGuardedTool_PreservesOptionalInterfaces(t *testing.T) {
	inner := &optionalStub{
		name:              "optional",
		longRunning:       true,
		skipSummarization: true,
	}
	guard := NewGuard()
	wrapped := WrapTool(inner, guard).(*GuardedTool)

	if !wrapped.LongRunning() {
		t.Error("LongRunning should be preserved")
	}
	if !wrapped.SkipSummarization() {
		t.Error("SkipSummarization should be preserved")
	}
}

func TestGuardedStreamableTool_PreservesOptionalInterfaces(t *testing.T) {
	inner := &optionalStreamableStub{
		name:              "optional_streamable",
		longRunning:       true,
		skipSummarization: true,
	}
	guard := NewGuard()
	wrapped := WrapTools([]tool.Tool{inner}, guard)
	if len(wrapped) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(wrapped))
	}
	st, ok := wrapped[0].(*GuardedStreamableTool)
	if !ok {
		t.Fatalf("expected *GuardedStreamableTool, got %T", wrapped[0])
	}
	if !st.LongRunning() {
		t.Error("LongRunning should be preserved")
	}
	if !st.SkipSummarization() {
		t.Error("SkipSummarization should be preserved")
	}
}

func TestGuardedCombinedTool_PreservesOptionalInterfaces(t *testing.T) {
	inner := &optionalCombinedStub{
		name:              "optional_combined",
		longRunning:       true,
		skipSummarization: true,
	}
	guard := NewGuard()
	wrapped := WrapTool(inner, guard).(*GuardedCombinedTool)

	if !wrapped.LongRunning() {
		t.Error("LongRunning should be preserved")
	}
	if !wrapped.SkipSummarization() {
		t.Error("SkipSummarization should be preserved")
	}
}

// optionalStub is a callable tool that also implements LongRunning and SkipSummarization.
type optionalStub struct {
	name              string
	callCount         int
	longRunning       bool
	skipSummarization bool
}

func (s *optionalStub) Declaration() *tool.Declaration { return &tool.Declaration{Name: s.name} }
func (s *optionalStub) Call(_ context.Context, _ []byte) (any, error) {
	s.callCount++
	return map[string]string{"ok": "true"}, nil
}
func (s *optionalStub) LongRunning() bool       { return s.longRunning }
func (s *optionalStub) SkipSummarization() bool { return s.skipSummarization }

// optionalStreamableStub is a streamable tool that also implements LongRunning and SkipSummarization.
type optionalStreamableStub struct {
	name              string
	streamCount       int
	longRunning       bool
	skipSummarization bool
}

func (s *optionalStreamableStub) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}
func (s *optionalStreamableStub) StreamableCall(_ context.Context, _ []byte) (*tool.StreamReader, error) {
	s.streamCount++
	return tool.NewStream(1).Reader, nil
}
func (s *optionalStreamableStub) LongRunning() bool       { return s.longRunning }
func (s *optionalStreamableStub) SkipSummarization() bool { return s.skipSummarization }

// optionalCombinedStub implements both CallableTool and StreamableTool plus the optional interfaces.
type optionalCombinedStub struct {
	name              string
	callCount         int
	streamCount       int
	longRunning       bool
	skipSummarization bool
}

func (s *optionalCombinedStub) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}
func (s *optionalCombinedStub) Call(_ context.Context, _ []byte) (any, error) {
	s.callCount++
	return map[string]string{"ok": "true"}, nil
}
func (s *optionalCombinedStub) StreamableCall(_ context.Context, _ []byte) (*tool.StreamReader, error) {
	s.streamCount++
	return tool.NewStream(1).Reader, nil
}
func (s *optionalCombinedStub) LongRunning() bool       { return s.longRunning }
func (s *optionalCombinedStub) SkipSummarization() bool { return s.skipSummarization }

func TestGuardedTool_UnknownDecisionDenies(t *testing.T) {
	inner := &stubTool{name: "exec_command", desc: "stub"}
	guard := NewGuard(WithRules(unknownDecisionRule{}))

	wrapped := WrapTool(inner, guard)
	out, err := wrapped.Call(context.Background(), jsonCommandArgs("ls"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inner.callCount != 0 {
		t.Error("inner tool must not be called on unknown decision")
	}
	res, ok := out.(tool.PermissionResult)
	if !ok {
		t.Fatalf("expected PermissionResult, got %T", out)
	}
	if res.Status != tool.PermissionResultStatusDenied {
		t.Errorf("expected denied, got %s", res.Status)
	}
}
