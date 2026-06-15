//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mragent

import (
	"context"

	sessionrecall "trpc.group/trpc-go/trpc-agent-go/internal/session/tool/recall"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// SessionSearchToolName is the on-demand session search tool name.
	SessionSearchToolName = sessionrecall.SearchToolName
	// SessionLoadToolName is the on-demand session load tool name.
	SessionLoadToolName = sessionrecall.LoadToolName
)

// ToolSetOption configures the MRAgent tool set.
type ToolSetOption func(*ToolSet)

// ToolSet exposes MRAgent reconstruction tools as an unprefixed tool set.
type ToolSet struct {
	includeSessionSearch bool
	includeSessionLoad   bool
	extraTools           []tool.Tool
}

// NewToolSet creates the tool set used by the MRAgent graph.
func NewToolSet(opts ...ToolSetOption) *ToolSet {
	ts := &ToolSet{
		includeSessionLoad: true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(ts)
		}
	}
	return ts
}

// Name returns an empty name so graph/llmagent ToolSet wrapping does not prefix
// public tool names such as memory_cue_search.
func (ts *ToolSet) Name() string { return "" }

// Tools returns the active reconstruction tools.
func (ts *ToolSet) Tools(context.Context) []tool.Tool {
	tools := []tool.Tool{
		memorytool.NewCueSearchTool(),
		memorytool.NewTagExpandTool(),
		memorytool.NewContentLoadTool(),
	}
	if ts.includeSessionSearch {
		tools = append(tools, sessionrecall.NewSearchTool())
	}
	if ts.includeSessionLoad {
		tools = append(tools, sessionrecall.NewLoadTool())
	}
	tools = append(tools, ts.extraTools...)
	return tools
}

// SessionToolMap returns unprefixed on-demand session tools for graph
// baselines or custom MRAgent variants.
func SessionToolMap(includeSearch, includeLoad bool) map[string]tool.Tool {
	tools := make(map[string]tool.Tool)
	if includeSearch {
		tools[SessionSearchToolName] = sessionrecall.NewSearchTool()
	}
	if includeLoad {
		tools[SessionLoadToolName] = sessionrecall.NewLoadTool()
	}
	return tools
}

// Close implements tool.ToolSet.
func (ts *ToolSet) Close() error { return nil }

// WithToolSetSessionSearchTool controls whether session_search is exposed.
func WithToolSetSessionSearchTool(enable bool) ToolSetOption {
	return func(ts *ToolSet) {
		ts.includeSessionSearch = enable
	}
}

// WithToolSetSessionLoadTool controls whether session_load is exposed.
func WithToolSetSessionLoadTool(enable bool) ToolSetOption {
	return func(ts *ToolSet) {
		ts.includeSessionLoad = enable
	}
}

// WithToolSetExtraTools appends additional tools to the MRAgent tool set.
func WithToolSetExtraTools(tools ...tool.Tool) ToolSetOption {
	return func(ts *ToolSet) {
		ts.extraTools = append(ts.extraTools, tools...)
	}
}
