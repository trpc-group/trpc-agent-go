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
// NamedTool wraps every tool that comes from a toolset. Reading
// MetadataOf(...).ConcurrencySafe to answer for them would have the file and
// shell toolsets all read as objecting — and every turn containing one would drop
// off the parallel path. A measured run did exactly that: 45 turns, 45 tool
// calls, one call per turn.
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
// change how the call is scheduled. The overlay wrapper implements none of the
// wrapped tool's optional interfaces, so without an explicit delegation a host
// that patches an objecting tool's description would silently put it back on the
// parallel path — the one place the objection was meant to be honored.
func TestApplyDeclarationsPreservesConcurrencyObjection(t *testing.T) {
	patched := ApplyDeclarations(
		[]tool.Tool{unsafeTool{}},
		[]tool.Declaration{{Name: "unsafe", Description: "patched"}},
	)[0]

	if got := patched.Declaration().Description; got != "patched" {
		t.Fatalf("declaration overlay was not applied: description = %q", got)
	}
	if tool.IsConcurrencySafe(patched) {
		t.Error("a patched declaration must not discard the tool's objection")
	}
}

// The same overlay must not manufacture an objection either.
func TestApplyDeclarationsPreservesConcurrencyDefault(t *testing.T) {
	patched := ApplyDeclarations(
		[]tool.Tool{plainTool{}},
		[]tool.Declaration{{Name: "plain", Description: "patched"}},
	)[0]

	if !tool.IsConcurrencySafe(patched) {
		t.Error("a patched declaration must leave an unobjecting tool admissible")
	}
}
