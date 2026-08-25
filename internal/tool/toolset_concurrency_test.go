//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// plainTool publishes no metadata at all, like almost every tool in a toolset.
type plainTool struct{}

func (plainTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: "plain"} }

// unsafeTool objects to running beside its siblings.
type unsafeTool struct{}

func (unsafeTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: "unsafe"} }
func (unsafeTool) IsConcurrencySafe() bool        { return false }

// Wrapping must not invent an objection the wrapped tool never raised.
//
// NamedTool wraps every tool from a toolset. Reading MetadataOf(...)
// .ConcurrencySafe to answer for them would have the file and shell toolsets all
// read as objecting, dropping every turn containing one off the parallel path. A
// measured run did exactly that: 45 turns, 45 tool calls, one call per turn.
func TestNamedToolPreservesConcurrencyDefault(t *testing.T) {
	wrapped := NewUnprefixedNamedTool(plainTool{})

	if !wrapped.IsConcurrencySafe() {
		t.Error("wrapping a tool that publishes nothing must leave it admissible")
	}
	if !tool.IsConcurrencySafe(tool.Tool(wrapped)) {
		t.Error("the wrapper must resolve as admissible through tool.IsConcurrencySafe")
	}
}

// The wrapper must still carry a real objection through.
func TestNamedToolPreservesDeclaredUnsafety(t *testing.T) {
	wrapped := NewUnprefixedNamedTool(unsafeTool{})

	if wrapped.IsConcurrencySafe() {
		t.Error("wrapping must not lose a tool's objection")
	}
	if tool.IsConcurrencySafe(tool.Tool(wrapped)) {
		t.Error("the wrapper must resolve as objecting through tool.IsConcurrencySafe")
	}
}

// A declaration overlay changes only what the model is shown, so it must not
// change how the call is scheduled.
//
// The overlay exposes none of the wrapped tool's optional interfaces, which is
// why schedulers resolve through this package rather than calling
// tool.IsConcurrencySafe on what they are handed: asked directly, the wrapper
// reports the default and a patched description restores the parallel path.
func TestApplyDeclarationsPreservesConcurrencyObjection(t *testing.T) {
	patched := ApplyDeclarations(
		[]tool.Tool{unsafeTool{}},
		[]tool.Declaration{{Name: "unsafe", Description: "patched"}},
	)[0]

	if got := patched.Declaration().Description; got != "patched" {
		t.Fatalf("declaration overlay was not applied: description = %q", got)
	}
	if IsConcurrencySafe(patched) {
		t.Error("a patched declaration must not discard the tool's objection")
	}
	// The wrapper stays opaque; that isolation is what ApplyDeclarations is for,
	// and why the resolving helper exists.
	if !tool.IsConcurrencySafe(patched) {
		t.Error("the overlay wrapper must not publish the objection itself")
	}
	if got := tool.MetadataOf(patched); got != (tool.ToolMetadata{}) {
		t.Errorf("the overlay wrapper must publish no metadata, got %+v", got)
	}
}

// The same overlay must not manufacture an objection either.
func TestApplyDeclarationsPreservesConcurrencyDefault(t *testing.T) {
	patched := ApplyDeclarations(
		[]tool.Tool{plainTool{}},
		[]tool.Declaration{{Name: "plain", Description: "patched"}},
	)[0]

	if !IsConcurrencySafe(patched) {
		t.Error("a patched declaration must leave an unobjecting tool admissible")
	}
}

// A NamedTool from a toolset is the other wrapper schedulers see, so the helper
// must resolve through it too.
func TestIsConcurrencySafeResolvesNamedTools(t *testing.T) {
	if IsConcurrencySafe(NewUnprefixedNamedTool(unsafeTool{})) {
		t.Error("a named wrapper must not hide the objection")
	}
	if !IsConcurrencySafe(NewUnprefixedNamedTool(plainTool{})) {
		t.Error("a named wrapper must not manufacture an objection")
	}
	if !IsConcurrencySafe(nil) {
		t.Error("a nil tool cannot object")
	}
}

// Wrappers nest: a toolset's tool is a NamedTool, and patching its declaration
// wraps that in an overlay — or the reverse, when the patch lands first.
//
// NamedTool.IsConcurrencySafe must therefore resolve rather than ask its
// original directly: an overlay asked directly reports the default, and
// tool.IsConcurrencySafe stops at the first ConcurrencyAware it finds, so it
// would take the wrapper's answer and never reach the resolver.
func TestNamedToolResolvesNestedDeclarationOverlays(t *testing.T) {
	patched := ApplyDeclarations(
		[]tool.Tool{unsafeTool{}},
		[]tool.Declaration{{Name: "unsafe", Description: "patched"}},
	)[0]
	wrapped := NewUnprefixedNamedTool(patched)

	if wrapped.IsConcurrencySafe() {
		t.Error("a named wrapper over an overlay must not hide the objection")
	}
	if IsConcurrencySafe(wrapped) {
		t.Error("the resolver must reach the objection through both wrappers")
	}
	if tool.IsConcurrencySafe(tool.Tool(wrapped)) {
		t.Error("the named wrapper must report the objection to plain callers too")
	}
}

// The nested case must not manufacture an objection either.
func TestNamedToolResolvesNestedOverlaysWithoutObjection(t *testing.T) {
	patched := ApplyDeclarations(
		[]tool.Tool{plainTool{}},
		[]tool.Declaration{{Name: "plain", Description: "patched"}},
	)[0]
	wrapped := NewUnprefixedNamedTool(patched)

	if !wrapped.IsConcurrencySafe() {
		t.Error("nesting wrappers must leave an unobjecting tool admissible")
	}
}
