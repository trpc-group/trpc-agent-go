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

// Option configures deferred loading for Tool or ToolSet. Nil options are ignored.
type Option func(*options)

type options struct {
	filter tool.FilterFunc
}

// WithFilter selects which tools to defer, replacing their own preferences.
// Returning true defers the tool; returning false keeps it immediately available.
// The filter does not remove tools. A nil filter defers every tool. If specified
// more than once, the last filter wins.
//
// The filter receives the original tool and the context passed to ShouldDefer,
// and may be called concurrently.
func WithFilter(filter tool.FilterFunc) Option {
	return func(o *options) { o.filter = filter }
}

func newDeferral(opts []Option) *itool.Deferral {
	var o options
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return &itool.Deferral{Filter: o.filter}
}

// Tool returns a wrapper that marks t as deferred by default. Use WithFilter
// to change the decision.
//
// The wrapper preserves the declaration, callable and streaming capabilities,
// and framework execution semantics. It does not modify or copy the underlying
// tool. A nil tool returns nil.
func Tool(t tool.Tool, opts ...Option) tool.Tool {
	return itool.WithDeferral(t, newDeferral(opts))
}

// ToolSet returns a wrapper that marks all members as deferred by default.
// Use WithFilter to select deferred members by their original declarations,
// before the Agent qualifies their names.
//
// Each Tools call fetches the current members from the original ToolSet and
// wraps them without changing its slice or members. This also covers members
// obtained by an Agent's refresh or activation. The wrapper preserves the
// ToolSet's name and optional description. Close delegates to the original
// ToolSet; the wrapper creates no resources or independent lifetime. A nil
// ToolSet returns nil.
func ToolSet(ts tool.ToolSet, opts ...Option) tool.ToolSet {
	if ts == nil {
		return nil
	}
	return &deferredToolSet{original: ts, deferral: newDeferral(opts)}
}

type deferredToolSet struct {
	original tool.ToolSet
	deferral *itool.Deferral
}

func (s *deferredToolSet) Tools(ctx context.Context) []tool.Tool {
	members := s.original.Tools(ctx)
	if members == nil {
		return nil
	}
	wrapped := make([]tool.Tool, len(members))
	for i, member := range members {
		wrapped[i] = itool.WithDeferral(member, s.deferral)
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
