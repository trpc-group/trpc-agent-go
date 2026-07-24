//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// WrapTool wraps a single tool with safety scanning.
// The returned tool delegates to the original but checks safety before each Call.
// If the Guard denies the call, a denial result is returned instead of
// executing the tool.
//
// WrapTool forwards optional interfaces implemented by the inner tool:
// tool.MetadataProvider, tool.ConcurrencyAware, tool.DeferredTool,
// and tool.PermissionChecker.
func WrapTool(t tool.Tool, g *Guard) tool.Tool {
	return &safeTool{
		Tool:  t,
		guard: g,
	}
}

// safeTool wraps a tool.Tool with safety scanning.
type safeTool struct {
	tool.Tool
	guard *Guard
}

// Declaration returns the original tool's declaration.
func (st *safeTool) Declaration() *tool.Declaration {
	return st.Tool.Declaration()
}

// Call checks safety before delegating to the original tool.
// If the guard denies the call, it returns a PermissionResult as the
// result value without calling the inner tool.
func (st *safeTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	decision, err := st.guard.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:  st.Declaration().Name,
		Arguments: jsonArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("safety check failed: %w", err)
	}
	if decision.Action != tool.PermissionActionAllow {
		return tool.PermissionResultFor(st.Declaration().Name, decision), nil
	}

	// Delegate to the original callable tool.
	if ct, ok := st.Tool.(tool.CallableTool); ok {
		return ct.Call(ctx, jsonArgs)
	}
	return nil, fmt.Errorf("tool %s is not callable", st.Declaration().Name)
}

// ToolMetadata forwards to the inner tool if it implements MetadataProvider.
func (st *safeTool) ToolMetadata() tool.ToolMetadata {
	if mp, ok := st.Tool.(tool.MetadataProvider); ok {
		return mp.ToolMetadata()
	}
	return tool.ToolMetadata{}
}

// IsConcurrencySafe forwards to the inner tool if it implements ConcurrencyAware.
func (st *safeTool) IsConcurrencySafe() bool {
	if ca, ok := st.Tool.(tool.ConcurrencyAware); ok {
		return ca.IsConcurrencySafe()
	}
	return false
}

// ShouldDefer forwards to the inner tool if it implements DeferredTool.
func (st *safeTool) ShouldDefer(ctx context.Context) bool {
	if dt, ok := st.Tool.(tool.DeferredTool); ok {
		return dt.ShouldDefer(ctx)
	}
	return false
}

// CheckPermission forwards to the inner tool if it implements PermissionChecker.
func (st *safeTool) CheckPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	if pc, ok := st.Tool.(tool.PermissionChecker); ok {
		return pc.CheckPermission(ctx, req)
	}
	return tool.AllowPermission(), nil
}

// WrapToolSet wraps all tools in a ToolSet with safety scanning.
// The returned ToolSet delegates to the original but applies safety
// scanning on each tool call.
func WrapToolSet(ts tool.ToolSet, g *Guard) tool.ToolSet {
	return &safeToolSet{
		original: ts,
		guard:    g,
	}
}

// safeToolSet wraps a tool.ToolSet with safety scanning.
type safeToolSet struct {
	original tool.ToolSet
	guard    *Guard
}

// Tools returns the tools from the original ToolSet, each wrapped with safety scanning.
func (sts *safeToolSet) Tools(ctx context.Context) []tool.Tool {
	original := sts.original.Tools(ctx)
	wrapped := make([]tool.Tool, len(original))
	for i, t := range original {
		wrapped[i] = WrapTool(t, sts.guard)
	}
	return wrapped
}

// Close delegates to the original ToolSet's Close.
func (sts *safeToolSet) Close() error {
	return sts.original.Close()
}

// Name returns the original ToolSet's name.
func (sts *safeToolSet) Name() string {
	return sts.original.Name()
}
