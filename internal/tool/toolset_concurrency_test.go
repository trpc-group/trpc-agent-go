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

// unsafeTool declares itself unfit for the parallel path.
type unsafeTool struct{}

func (unsafeTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: "unsafe"} }
func (unsafeTool) IsConcurrencySafe() bool        { return false }

// Wrapping must not invent a claim the wrapped tool never made.
//
// NamedTool wraps every tool that comes from a toolset and publishes metadata on
// its behalf, so if it republished MetadataOf's zero value the file and shell
// toolsets would all read as concurrency-unsafe — and every turn containing one
// would drop off the parallel path. A measured run did exactly that: 45 turns,
// 45 tool calls, one call per turn.
func TestNamedToolPreservesConcurrencyDefault(t *testing.T) {
	wrapped := NewUnprefixedNamedTool(plainTool{})

	if !wrapped.IsConcurrencySafe() {
		t.Error("wrapping a tool that publishes nothing must leave it concurrency-safe")
	}
	if !wrapped.ToolMetadata().ConcurrencySafe {
		t.Error("republished metadata must carry the same default")
	}
	if !tool.IsConcurrencySafe(tool.Tool(wrapped)) {
		t.Error("the wrapper must resolve as safe through tool.IsConcurrencySafe")
	}
}

// The wrapper must still carry a real declaration through.
func TestNamedToolPreservesDeclaredUnsafety(t *testing.T) {
	wrapped := NewUnprefixedNamedTool(unsafeTool{})

	if wrapped.IsConcurrencySafe() {
		t.Error("wrapping must not lose a tool's declaration that it is unsafe")
	}
	if wrapped.ToolMetadata().ConcurrencySafe {
		t.Error("republished metadata must carry the declaration too")
	}
	if tool.IsConcurrencySafe(tool.Tool(wrapped)) {
		t.Error("the wrapper must resolve as unsafe through tool.IsConcurrencySafe")
	}
}
