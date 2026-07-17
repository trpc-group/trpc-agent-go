//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package resultcodec

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type mockCallable struct {
	decl        *tool.Declaration
	longRunning bool
}

func (m *mockCallable) Declaration() *tool.Declaration { return m.decl }
func (m *mockCallable) Call(_ context.Context, _ []byte) (any, error) {
	return "called", nil
}
func (m *mockCallable) LongRunning() bool { return m.longRunning }

type mockStreamable struct {
	decl        *tool.Declaration
	streamInner bool
}

func (m *mockStreamable) Declaration() *tool.Declaration { return m.decl }
func (m *mockStreamable) Call(_ context.Context, _ []byte) (any, error) {
	return "called", nil
}
func (m *mockStreamable) StreamableCall(
	_ context.Context,
	_ []byte,
) (*tool.StreamReader, error) {
	return nil, nil
}
func (m *mockStreamable) StreamInner() bool { return m.streamInner }

func TestWrap_NilTool(t *testing.T) {
	if got := Wrap(nil, JSON()); got != nil {
		t.Fatalf("Wrap(nil) should return nil, got %v", got)
	}
}

func TestWrap_CallableOnly(t *testing.T) {
	base := &mockCallable{decl: &tool.Declaration{Name: "c"}}
	wrapped := Wrap(base, XML())

	if _, ok := wrapped.(tool.CallableTool); !ok {
		t.Fatal("wrapped tool should be callable")
	}
	if _, ok := wrapped.(tool.StreamableTool); ok {
		t.Fatal("callable-only tool must not become streamable")
	}
	if wrapped.Declaration().Name != "c" {
		t.Fatalf("declaration should delegate, got %q", wrapped.Declaration().Name)
	}
}

func TestWrap_ExposesCodecAndUnwrap(t *testing.T) {
	base := &mockCallable{decl: &tool.Declaration{Name: "c"}}
	codec := XML()
	wrapped := Wrap(base, codec)

	provider, ok := wrapped.(interface{ ResultCodec() Codec })
	if !ok {
		t.Fatal("wrapped tool should expose ResultCodec()")
	}
	if provider.ResultCodec() != codec {
		t.Fatal("ResultCodec() should return the bound codec")
	}
	unwrapper, ok := wrapped.(interface{ Unwrap() tool.Tool })
	if !ok {
		t.Fatal("wrapped tool should expose Unwrap()")
	}
	if unwrapper.Unwrap() != base {
		t.Fatal("Unwrap() should return the base tool")
	}
}

func TestWrap_StreamablePreserved(t *testing.T) {
	base := &mockStreamable{decl: &tool.Declaration{Name: "s"}, streamInner: true}
	wrapped := Wrap(base, JSON())
	if _, ok := wrapped.(tool.StreamableTool); !ok {
		t.Fatal("streamable capability should be preserved")
	}
}

func TestWrap_StreamInnerOptOutDropsStreamable(t *testing.T) {
	// A tool that implements StreamableTool but opts out via StreamInner()==false
	// must not advertise streamable after wrapping, matching framework detection.
	base := &mockStreamable{decl: &tool.Declaration{Name: "s"}, streamInner: false}
	wrapped := Wrap(base, JSON())
	if _, ok := wrapped.(tool.StreamableTool); ok {
		t.Fatal("StreamInner opt-out should drop the streamable wrapper")
	}
	if _, ok := wrapped.(tool.CallableTool); !ok {
		t.Fatal("callable capability should remain")
	}
}

type mockMetaTool struct {
	decl      *tool.Declaration
	meta      tool.ToolMetadata
	deferLoad bool
	skip      bool
}

func (m *mockMetaTool) Declaration() *tool.Declaration            { return m.decl }
func (m *mockMetaTool) Call(context.Context, []byte) (any, error) { return "called", nil }
func (m *mockMetaTool) ToolMetadata() tool.ToolMetadata           { return m.meta }
func (m *mockMetaTool) ShouldDefer(context.Context) bool          { return m.deferLoad }
func (m *mockMetaTool) SkipSummarization() bool                   { return m.skip }

func TestWrap_PreservesMetadata(t *testing.T) {
	base := &mockMetaTool{
		decl: &tool.Declaration{Name: "m"},
		meta: tool.ToolMetadata{ReadOnly: true, ConcurrencySafe: true, MaxResultSize: 123},
	}
	wrapped := Wrap(base, JSON())

	got := tool.MetadataOf(wrapped)
	if got != base.meta {
		t.Fatalf("MetadataOf(wrapped) = %+v, want %+v", got, base.meta)
	}
	aware, ok := wrapped.(tool.ConcurrencyAware)
	if !ok || !aware.IsConcurrencySafe() {
		t.Fatal("wrapped tool should report concurrency safety of the base")
	}
}

func TestWrap_PreservesShouldDefer(t *testing.T) {
	base := &mockMetaTool{decl: &tool.Declaration{Name: "m"}, deferLoad: true}
	wrapped := Wrap(base, JSON())
	if !tool.ShouldDefer(context.Background(), wrapped) {
		t.Fatal("ShouldDefer(wrapped) should delegate to the base tool")
	}
}

func TestWrap_PreservesSkipSummarization(t *testing.T) {
	base := &mockMetaTool{decl: &tool.Declaration{Name: "m"}, skip: true}
	wrapped := Wrap(base, JSON())
	s, ok := wrapped.(interface{ SkipSummarization() bool })
	if !ok || !s.SkipSummarization() {
		t.Fatal("wrapped tool should delegate SkipSummarization to the base")
	}
}

// unwrapOnlyTool exposes only Unwrap(), hiding the inner tool's capabilities
// unless the wrapper resolves them through the full chain.
type unwrapOnlyTool struct {
	inner tool.Tool
}

func (u *unwrapOnlyTool) Declaration() *tool.Declaration { return u.inner.Declaration() }
func (u *unwrapOnlyTool) Call(ctx context.Context, args []byte) (any, error) {
	return u.inner.(tool.CallableTool).Call(ctx, args)
}
func (u *unwrapOnlyTool) Unwrap() tool.Tool { return u.inner }

// denyingTool implements PermissionChecker with a deny decision.
type denyingTool struct {
	decl *tool.Declaration
}

func (d *denyingTool) Declaration() *tool.Declaration            { return d.decl }
func (d *denyingTool) Call(context.Context, []byte) (any, error) { return "called", nil }
func (d *denyingTool) CheckPermission(
	context.Context,
	*tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return tool.DenyPermission("nope"), nil
}

func TestWrap_ResolvesPermissionThroughChain(t *testing.T) {
	// codecTool -> unwrapOnlyTool (no permission) -> denyingTool (deny).
	base := &denyingTool{decl: &tool.Declaration{Name: "danger"}}
	mid := &unwrapOnlyTool{inner: base}
	wrapped := Wrap(mid, JSON())

	checker, ok := wrapped.(tool.PermissionChecker)
	if !ok {
		t.Fatal("wrapped tool should expose PermissionChecker")
	}
	decision, err := checker.CheckPermission(context.Background(), &tool.PermissionRequest{})
	if err != nil {
		t.Fatalf("CheckPermission error: %v", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("permission must resolve through the chain to deny, got %q", decision.Action)
	}
}

// selfUnwrapTool returns itself from Unwrap, forming a cycle.
type selfUnwrapTool struct {
	decl *tool.Declaration
}

func (s *selfUnwrapTool) Declaration() *tool.Declaration            { return s.decl }
func (s *selfUnwrapTool) Call(context.Context, []byte) (any, error) { return nil, nil }
func (s *selfUnwrapTool) Unwrap() tool.Tool                         { return s }

func TestWrap_CyclicUnwrapTerminates(t *testing.T) {
	// A cyclic Unwrap() chain must not hang, and permission must fail closed:
	// because the chain can't be fully traversed, a hidden deny cannot be ruled
	// out, so the decision is deny rather than allow.
	s := &selfUnwrapTool{decl: &tool.Declaration{Name: "cyclic"}}
	wrapped := Wrap(s, JSON())
	checker, ok := wrapped.(tool.PermissionChecker)
	if !ok {
		t.Fatal("wrapped tool should expose PermissionChecker")
	}
	// Reaching this call returning (instead of hanging) proves termination.
	decision, err := checker.CheckPermission(context.Background(), &tool.PermissionRequest{})
	if err != nil {
		t.Fatalf("CheckPermission error: %v", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("cyclic chain must fail closed (deny), got %q", decision.Action)
	}
}

func TestWrap_CallDelegates(t *testing.T) {
	base := &mockCallable{decl: &tool.Declaration{Name: "c"}}
	wrapped := Wrap(base, JSON())
	callable, ok := wrapped.(tool.CallableTool)
	if !ok {
		t.Fatal("expected callable")
	}
	got, err := callable.Call(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "called" {
		t.Fatalf("Call should delegate, got %v", got)
	}
}
