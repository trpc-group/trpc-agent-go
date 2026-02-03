//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package history provides tools for agents to retrieve and search session history
// on demand, helping reduce token usage while preserving access to full context.
package history

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolSet bundles history-related tools.
//
// Currently it includes:
//   - search_history: find relevant events with snippets
//   - get_history_events: fetch full (but bounded) content by event id
type ToolSet struct {
	name  string
	tools []tool.Tool
}

// Name implements tool.ToolSet.
func (t *ToolSet) Name() string { return t.name }

// Tools implements tool.ToolSet.
func (t *ToolSet) Tools(ctx context.Context) []tool.Tool { return t.tools }

// Close implements tool.ToolSet.
func (t *ToolSet) Close() error { return nil }

// NewToolSet creates a history ToolSet with default budget limits.
func NewToolSet() *ToolSet {
	return &ToolSet{
		name: "history",
		tools: []tool.Tool{
			NewSearchTool(),
			NewGetEventsTool(),
		},
	}
}
