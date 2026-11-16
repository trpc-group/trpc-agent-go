//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package deferred marks registered tools for on-demand loading through the
// existing tool.DeferredTool capability. Loading depends on the consumer; the
// Responses adapter uses these markers when native tool search is enabled.
// Wrapping alone does not hide tools from other models.
package deferred

import (
	"context"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Tool returns a wrapper whose ShouldDefer decision is given by filter. A nil
// filter always defers the tool; a non-nil filter replaces the tool's own
// preference, with false keeping it eager. The filter receives the original
// tool and the context passed to ShouldDefer, and may be called concurrently.
//
// The wrapper preserves the declaration, callable and streaming capabilities,
// and framework execution semantics. It does not modify or copy the underlying
// tool. A nil tool returns nil.
func Tool(t tool.Tool, filter tool.FilterFunc) tool.Tool {
	return itool.WithDeferral(t, &itool.Deferral{Filter: filter})
}

// ToolSet returns a wrapper that applies filter to each member's ShouldDefer
// decision, as described by Tool. A nil filter defers every member. Filters see
// original member declarations, before the Agent qualifies their names.
//
// Each Tools call fetches the current members from the original ToolSet and
// wraps them without changing its slice or members. This also covers members
// obtained by an Agent's refresh or activation. The wrapper preserves the
// ToolSet's name and optional description. Close delegates to the original
// ToolSet; the wrapper creates no resources or independent lifetime. A nil
// ToolSet returns nil.
func ToolSet(ts tool.ToolSet, filter tool.FilterFunc) tool.ToolSet {
	if ts == nil {
		return nil
	}
	return &deferredToolSet{original: ts, filter: filter}
}

type deferredToolSet struct {
	original tool.ToolSet
	filter   tool.FilterFunc
}

func (s *deferredToolSet) Tools(ctx context.Context) []tool.Tool {
	members := s.original.Tools(ctx)
	if members == nil {
		return nil
	}
	wrapped := make([]tool.Tool, len(members))
	for i, member := range members {
		wrapped[i] = Tool(member, s.filter)
	}
	return wrapped
}

func (s *deferredToolSet) Name() string { return s.original.Name() }

func (s *deferredToolSet) Description() string {
	if describer, ok := s.original.(tool.ToolSetDescriber); ok {
		return describer.Description()
	}
	return ""
}

func (s *deferredToolSet) Close() error { return s.original.Close() }
