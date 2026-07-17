//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultcodec"
)

// runWithinTimeout runs fn and fails the test if it does not return within d, so
// a regression in cycle protection fails fast instead of hanging go test until
// its global timeout.
func runWithinTimeout(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("traversal did not terminate; cycle protection may have regressed")
	}
}

type rcFakeCallable struct {
	name        string
	longRunning bool
}

func (f *rcFakeCallable) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name}
}
func (f *rcFakeCallable) Call(context.Context, []byte) (any, error) { return nil, nil }
func (f *rcFakeCallable) LongRunning() bool                         { return f.longRunning }

func TestResolveResultCodec_FromWrap(t *testing.T) {
	codec := resultcodec.JSON()
	wrapped := resultcodec.Wrap(&rcFakeCallable{name: "b"}, codec)
	if got := ResolveResultCodec(wrapped); got != codec {
		t.Fatalf("expected codec from Wrap, got %v", got)
	}
}

func TestResolveResultCodec_NoCodec(t *testing.T) {
	if got := ResolveResultCodec(&rcFakeCallable{name: "b"}); got != nil {
		t.Fatalf("expected nil codec, got %v", got)
	}
}

func TestResolveResultCodec_Nil(t *testing.T) {
	if got := ResolveResultCodec(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestResolveResultCodec_ThroughNamedTool(t *testing.T) {
	codec := resultcodec.XML()
	wrapped := resultcodec.Wrap(&rcFakeCallable{name: "b"}, codec)
	named := NewUnprefixedNamedTool(wrapped)
	if got := ResolveResultCodec(named); got != codec {
		t.Fatalf("expected codec through NamedTool, got %v", got)
	}
}

func TestResolveSemantic_SeesThroughResultCodecWrap(t *testing.T) {
	wrapped := resultcodec.Wrap(
		&rcFakeCallable{name: "b", longRunning: true},
		resultcodec.JSON(),
	)
	lr, ok := ResolveSemantic(wrapped).(interface{ LongRunning() bool })
	if !ok {
		t.Fatal("ResolveSemantic should see through Wrap to LongRunner")
	}
	if !lr.LongRunning() {
		t.Fatal("LongRunning should be true through the wrapper")
	}
}

func TestResolveDeclaration_SeesThroughResultCodecWrap(t *testing.T) {
	wrapped := resultcodec.Wrap(
		&rcFakeCallable{name: "b", longRunning: true},
		resultcodec.JSON(),
	)
	lr, ok := ResolveDeclaration(wrapped).(interface{ LongRunning() bool })
	if !ok {
		t.Fatal("ResolveDeclaration should see through Wrap to LongRunner")
	}
	if !lr.LongRunning() {
		t.Fatal("LongRunning should be true through the wrapper")
	}
}

// rcPermChecker denies permission and exposes Unwrap, standing in for a
// transparent third-party permission wrapper.
type rcPermChecker struct {
	name  string
	inner tool.Tool
}

func (p *rcPermChecker) Declaration() *tool.Declaration { return &tool.Declaration{Name: p.name} }
func (p *rcPermChecker) Unwrap() tool.Tool              { return p.inner }
func (p *rcPermChecker) CheckPermission(
	context.Context,
	*tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return tool.DenyPermission("no"), nil
}

func TestResolvePermissionChecker_NotSkippedByUnwrap(t *testing.T) {
	// resultcodec.Wrap wraps a permission-denying wrapper over a plain tool.
	// Resolving the permission checker must find the wrapper's deny, not unwrap
	// past it to the inner tool that has no checker.
	inner := &rcFakeCallable{name: "inner"}
	wrapped := resultcodec.Wrap(&rcPermChecker{name: "pw", inner: inner}, resultcodec.JSON())
	checker, err := ResolvePermissionChecker(wrapped)
	if err != nil {
		t.Fatalf("ResolvePermissionChecker error: %v", err)
	}
	if checker == nil {
		t.Fatal("expected a permission checker in the wrapper chain")
	}
	decision, err := checker.CheckPermission(context.Background(), &tool.PermissionRequest{})
	if err != nil {
		t.Fatalf("CheckPermission error: %v", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("expected deny, got %q", decision.Action)
	}
}

func TestResolvePermissionChecker_NoneReturnsNil(t *testing.T) {
	// A plain tool with no checker anywhere in the chain returns (nil, nil).
	checker, err := ResolvePermissionChecker(&rcFakeCallable{name: "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checker != nil {
		t.Fatal("expected no permission checker for a plain tool")
	}
}

// rcUnwrapOnly is a transparent wrapper exposing only Unwrap, used to build deep
// and cyclic chains.
type rcUnwrapOnly struct {
	name  string
	inner tool.Tool
}

func (w *rcUnwrapOnly) Declaration() *tool.Declaration { return &tool.Declaration{Name: w.name} }
func (w *rcUnwrapOnly) Unwrap() tool.Tool              { return w.inner }

func TestResolvePermissionChecker_ExhaustedChainFailsClosed(t *testing.T) {
	// A checker hidden past the depth bound must not be reported as "no checker".
	var t0 tool.Tool = &rcPermChecker{name: "deny", inner: &rcFakeCallable{name: "base"}}
	for i := 0; i < 200; i++ {
		t0 = &rcUnwrapOnly{name: "w", inner: t0}
	}
	checker, err := ResolvePermissionChecker(t0)
	if err == nil {
		t.Fatal("expected an exhaustion error for an overly deep chain")
	}
	if checker != nil {
		t.Fatal("expected no checker returned on exhaustion")
	}
}

func TestNamedTool_CheckPermissionResolvesDeepDeny(t *testing.T) {
	// NamedTool -> transparent wrapper -> deny checker. NamedTool must resolve
	// the deeper deny, not only its direct original.
	deny := &rcPermChecker{name: "deny", inner: &rcFakeCallable{name: "base"}}
	named := NewUnprefixedNamedTool(&rcUnwrapOnly{name: "t", inner: deny})
	decision, err := named.CheckPermission(context.Background(), &tool.PermissionRequest{})
	if err != nil {
		t.Fatalf("CheckPermission error: %v", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("NamedTool must honor a deny behind a transparent wrapper, got %q", decision.Action)
	}
}

func TestResolvePermissionChecker_CyclicFailsClosed(t *testing.T) {
	// A cyclic chain must terminate and fail closed rather than report "none".
	c := &rcSelfUnwrap{name: "cyclic"}
	var (
		checker tool.PermissionChecker
		err     error
	)
	runWithinTimeout(t, 5*time.Second, func() {
		checker, err = ResolvePermissionChecker(c)
	})
	if err == nil {
		t.Fatal("expected an exhaustion error for a cyclic chain")
	}
	if checker != nil {
		t.Fatal("expected no checker returned on a cyclic chain")
	}
}

// rcSelfUnwrap returns itself from Unwrap, forming a cycle.
type rcSelfUnwrap struct {
	name string
}

func (s *rcSelfUnwrap) Declaration() *tool.Declaration { return &tool.Declaration{Name: s.name} }
func (s *rcSelfUnwrap) Unwrap() tool.Tool              { return s }

func TestResolvers_CyclicUnwrapTerminate(t *testing.T) {
	// A cyclic Unwrap() chain must not cause unbounded recursion; the
	// depth-bounded traversals return instead of hanging or overflowing.
	s := &rcSelfUnwrap{name: "cyclic"}
	var semantic, declaration tool.Tool
	var codec resultcodec.Codec
	runWithinTimeout(t, 5*time.Second, func() {
		semantic = ResolveSemantic(s)
		declaration = ResolveDeclaration(s)
		codec = ResolveResultCodec(s)
	})
	if semantic == nil {
		t.Fatal("ResolveSemantic should return a tool, not nil")
	}
	if declaration == nil {
		t.Fatal("ResolveDeclaration should return a tool, not nil")
	}
	if codec != nil {
		t.Fatal("ResolveResultCodec should return nil when no codec is present")
	}
}
