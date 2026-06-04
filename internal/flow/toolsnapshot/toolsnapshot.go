//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolsnapshot owns the invocation-scoped LLM tool snapshot keys.
package toolsnapshot

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// ToolsSnapshotKey is the invocation state key used to cache the final tool list.
	ToolsSnapshotKey = "llmflow:tools_snapshot"
	// HasFilteredUserToolsKey caches whether the filtered snapshot has user tools.
	HasFilteredUserToolsKey = "llmflow:has_filtered_user_tools"
)

// Get returns the cached tool snapshot for this invocation.
func Get(inv *agent.Invocation) ([]tool.Tool, bool) {
	tools, ok := agent.GetStateValue[[]tool.Tool](inv, ToolsSnapshotKey)
	if !ok {
		return nil, false
	}
	return copyTools(tools), true
}

// Set stores the cached tool snapshot for this invocation.
func Set(inv *agent.Invocation, tools []tool.Tool, hasFilteredUserTools bool) {
	if inv == nil {
		return
	}
	inv.SetState(ToolsSnapshotKey, copyTools(tools))
	inv.SetState(HasFilteredUserToolsKey, hasFilteredUserTools)
}

// HasFilteredUserTools reports whether the cached snapshot has user tools.
func HasFilteredUserTools(inv *agent.Invocation) (bool, bool) {
	return agent.GetStateValue[bool](inv, HasFilteredUserToolsKey)
}

// Invalidate clears the cached tool snapshot for this invocation.
func Invalidate(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(ToolsSnapshotKey)
	inv.DeleteState(HasFilteredUserToolsKey)
}

func copyTools(tools []tool.Tool) []tool.Tool {
	return append([]tool.Tool(nil), tools...)
}
