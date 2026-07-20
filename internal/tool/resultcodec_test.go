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

// rcPermChecker denies permission and exposes TransparentUnwrap, standing in for
// a transparent third-party permission wrapper.
type rcPermChecker struct {
	name  string
	inner tool.Tool
}

func (p *rcPermChecker) Declaration() *tool.Declaration { return &tool.Declaration{Name: p.name} }
func (p *rcPermChecker) TransparentUnwrap() tool.Tool   { return p.inner }
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

// rcUnwrapOnly is a transparent wrapper exposing only TransparentUnwrap, used to
// build deep and cyclic chains.
type rcUnwrapOnly struct {
	name  string
	inner tool.Tool
}

func (w *rcUnwrapOnly) Declaration() *tool.Declaration { return &tool.Declaration{Name: w.name} }
func (w *rcUnwrapOnly) TransparentUnwrap() tool.Tool   { return w.inner }

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

// rcSelfUnwrap returns itself from TransparentUnwrap, forming a cycle.
type rcSelfUnwrap struct {
	name string
}

func (s *rcSelfUnwrap) Declaration() *tool.Declaration { return &tool.Declaration{Name: s.name} }
func (s *rcSelfUnwrap) TransparentUnwrap() tool.Tool   { return s }

func TestResolvers_CyclicUnwrapTerminate(t *testing.T) {
	// A cyclic transparent chain must not cause unbounded recursion; the
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

// rcCapProvider is an innermost tool that publishes metadata, deferred loading,
// and skip-summarization preferences.
type rcCapProvider struct {
	name string
}

func (p *rcCapProvider) Declaration() *tool.Declaration            { return &tool.Declaration{Name: p.name} }
func (p *rcCapProvider) Call(context.Context, []byte) (any, error) { return "ok", nil }
func (p *rcCapProvider) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{Destructive: true, OpenWorld: true, MaxResultSize: 7}
}
func (p *rcCapProvider) ShouldDefer(context.Context) bool { return true }
func (p *rcCapProvider) SkipSummarization() bool          { return true }

// TestNamedTool_ResolvesCapabilitiesThroughTransparentWrapper covers the reported
// chain resultcodec.Wrap -> NamedTool -> transparent wrapper -> provider, and
// asserts NamedTool no longer hides a deeper MetadataProvider / DeferredTool /
// SkipSummarization behind an intermediate transparent wrapper (the sibling of
// the permission-chain fix). The transparent wrapper deliberately does not
// implement any of those capabilities itself.
func TestNamedTool_ResolvesCapabilitiesThroughTransparentWrapper(t *testing.T) {
	ctx := context.Background()
	provider := &rcCapProvider{name: "inner"}
	wrapper := &rcUnwrapOnly{name: "inner", inner: provider}
	named := NewUnprefixedNamedTool(wrapper)

	// Directly on the NamedTool (the reported shallow methods).
	md := tool.MetadataOf(named)
	if !md.Destructive || !md.OpenWorld || md.MaxResultSize != 7 {
		t.Fatalf("NamedTool must resolve metadata through the wrapper, got %+v", md)
	}
	if !tool.ShouldDefer(ctx, named) {
		t.Fatal("NamedTool must resolve ShouldDefer through the wrapper")
	}
	if s, ok := tool.Tool(named).(interface{ SkipSummarization() bool }); !ok || !s.SkipSummarization() {
		t.Fatal("NamedTool must resolve SkipSummarization through the wrapper")
	}

	// Full reported chain, inspected directly (no manual ResolveSemantic).
	wrapped := resultcodec.Wrap(named, resultcodec.JSON())
	wmd := tool.MetadataOf(wrapped)
	if !wmd.Destructive || !wmd.OpenWorld || wmd.MaxResultSize != 7 {
		t.Fatalf("wrapped chain must resolve metadata, got %+v", wmd)
	}
	if !tool.ShouldDefer(ctx, wrapped) {
		t.Fatal("wrapped chain must resolve ShouldDefer")
	}
	if s, ok := wrapped.(interface{ SkipSummarization() bool }); !ok || !s.SkipSummarization() {
		t.Fatal("wrapped chain must resolve SkipSummarization")
	}
}

// rcSafeProvider is concurrency-safe and non-destructive.
type rcSafeProvider struct {
	name string
}

func (p *rcSafeProvider) Declaration() *tool.Declaration            { return &tool.Declaration{Name: p.name} }
func (p *rcSafeProvider) Call(context.Context, []byte) (any, error) { return "ok", nil }
func (p *rcSafeProvider) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{ConcurrencySafe: true}
}

// rcConcurrencyOverrideWrapper is a transparent wrapper that overrides only the
// concurrency-safety capability (marks the composite unsafe), delegating the rest.
type rcConcurrencyOverrideWrapper struct {
	inner tool.Tool
}

func (w *rcConcurrencyOverrideWrapper) Declaration() *tool.Declaration {
	return w.inner.Declaration()
}
func (w *rcConcurrencyOverrideWrapper) Call(ctx context.Context, args []byte) (any, error) {
	return w.inner.(tool.CallableTool).Call(ctx, args)
}
func (w *rcConcurrencyOverrideWrapper) TransparentUnwrap() tool.Tool { return w.inner }
func (w *rcConcurrencyOverrideWrapper) IsConcurrencySafe() bool      { return false }

// rcDestructiveOverrideWrapper is a transparent wrapper that declares the
// composite destructive via its own MetadataProvider, delegating unwrap.
type rcDestructiveOverrideWrapper struct {
	inner tool.Tool
}

func (w *rcDestructiveOverrideWrapper) Declaration() *tool.Declaration { return w.inner.Declaration() }
func (w *rcDestructiveOverrideWrapper) Call(ctx context.Context, args []byte) (any, error) {
	return w.inner.(tool.CallableTool).Call(ctx, args)
}
func (w *rcDestructiveOverrideWrapper) TransparentUnwrap() tool.Tool { return w.inner }
func (w *rcDestructiveOverrideWrapper) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{Destructive: true}
}

func TestResolveMetadata_OuterWrapperOverridesConcurrency(t *testing.T) {
	// An intermediate transparent wrapper that marks the composite as not
	// concurrency-safe must win over the inner tool's ConcurrencySafe=true, rather
	// than being skipped by fully unwrapping to the innermost tool.
	inner := &rcSafeProvider{name: "inner"}
	wrapper := &rcConcurrencyOverrideWrapper{inner: inner}

	if !ResolveMetadata(inner).ConcurrencySafe {
		t.Fatal("precondition: inner tool must be concurrency-safe")
	}
	if ResolveMetadata(wrapper).ConcurrencySafe {
		t.Fatal("outer ConcurrencySafe=false must win over inner true")
	}

	named := NewUnprefixedNamedTool(wrapper)
	if named.IsConcurrencySafe() {
		t.Fatal("NamedTool must honor the outer ConcurrencySafe=false")
	}
	if tool.MetadataOf(named).ConcurrencySafe {
		t.Fatal("MetadataOf(NamedTool) must honor the outer override")
	}
	if tool.MetadataOf(resultcodec.Wrap(named, resultcodec.JSON())).ConcurrencySafe {
		t.Fatal("codecTool chain must honor the outer ConcurrencySafe=false")
	}
}

func TestResolveMetadata_OuterWrapperOverridesDestructive(t *testing.T) {
	// An intermediate transparent wrapper that declares the composite destructive
	// must win over a non-destructive inner tool.
	inner := &rcSafeProvider{name: "inner"} // not destructive
	wrapper := &rcDestructiveOverrideWrapper{inner: inner}

	if ResolveMetadata(inner).Destructive {
		t.Fatal("precondition: inner tool must not be destructive")
	}
	if !ResolveMetadata(wrapper).Destructive {
		t.Fatal("outer Destructive=true must win over inner false")
	}
	if !tool.MetadataOf(NewUnprefixedNamedTool(wrapper)).Destructive {
		t.Fatal("NamedTool must honor the outer Destructive=true")
	}
}

// nilUnwrapTool is a transparent wrapper that unwraps to nil (a clean end).
type nilUnwrapTool struct {
	decl *tool.Declaration
}

func (n *nilUnwrapTool) Declaration() *tool.Declaration            { return n.decl }
func (n *nilUnwrapTool) Call(context.Context, []byte) (any, error) { return nil, nil }
func (n *nilUnwrapTool) TransparentUnwrap() tool.Tool              { return nil }

func TestResolveMetadata_NilTerminationReturnsZero(t *testing.T) {
	// A nil tool, or a transparent wrapper that unwraps to nil, is a clean end,
	// not depth exhaustion: metadata must be the benign zero value, not the
	// conservative fail-closed flags.
	if md := ResolveMetadata(nil); md != (tool.ToolMetadata{}) {
		t.Fatalf("ResolveMetadata(nil) must be zero, got %+v", md)
	}
	w := &nilUnwrapTool{decl: &tool.Declaration{Name: "n"}}
	if md := ResolveMetadata(w); md != (tool.ToolMetadata{}) {
		t.Fatalf("ResolveMetadata(nil-unwrap) must be zero, got %+v", md)
	}
}

func TestResolveMetadata_CyclicFailsClosed(t *testing.T) {
	// A cyclic chain (depth exhaustion) must still fail closed with conservative
	// flags, distinct from the clean nil termination above.
	s := &rcSelfUnwrap{name: "cyclic"}
	md := ResolveMetadata(s)
	if !md.Destructive || !md.OpenWorld {
		t.Fatalf("cyclic chain must fail closed, got %+v", md)
	}
}
