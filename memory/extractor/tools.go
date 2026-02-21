//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// backgroundToolCreators maps tool names to their creator functions.
// These are the tools that can be used by the extractor in background.
var backgroundToolCreators = map[string]func() tool.CallableTool{
	memory.AddToolName:    memorytool.NewAddTool,
	memory.UpdateToolName: memorytool.NewUpdateTool,
	memory.DeleteToolName: memorytool.NewDeleteTool,
	memory.ClearToolName:  memorytool.NewClearTool,
}

// filterTools returns a new tool map containing only tools that are
// enabled by the given set. A nil or empty set means all tools are
// enabled.
func filterTools(
	all map[string]tool.Tool,
	enabled map[string]struct{},
) map[string]tool.Tool {
	if enabled == nil {
		return all
	}
	filtered := make(map[string]tool.Tool, len(all))
	for name, t := range all {
		if _, ok := enabled[name]; ok {
			filtered[name] = t
		}
	}
	return filtered
}

// backgroundTools is the pre-built map of background tools for model request.
// These tools are declaration-only and not callable.
var backgroundTools = func() map[string]tool.Tool {
	tools := make(map[string]tool.Tool, len(backgroundToolCreators))
	for name, creator := range backgroundToolCreators {
		t := creator()
		tools[name] = &declarationOnlyTool{decl: t.Declaration()}
	}
	return tools
}()

// declarationOnlyTool is a tool that only provides declaration, not callable.
type declarationOnlyTool struct {
	decl *tool.Declaration
}

// Declaration returns the tool declaration.
func (t *declarationOnlyTool) Declaration() *tool.Declaration {
	return t.decl
}

// Argument keys for tool calls.
const (
	argKeyMemory   = "memory"
	argKeyMemoryID = "memory_id"
	argKeyTopics   = "topics"
)

// parseToolCallArgs parses tool call arguments and returns a memory operation.
func parseToolCallArgs(toolName string, args map[string]any) *Operation {
	switch toolName {
	case memory.AddToolName:
		mem, _ := args[argKeyMemory].(string)
		if mem == "" {
			return nil
		}
		return &Operation{
			Type:   OperationAdd,
			Memory: mem,
			Topics: toStringSlice(args[argKeyTopics]),
		}

	case memory.UpdateToolName:
		id, _ := args[argKeyMemoryID].(string)
		mem, _ := args[argKeyMemory].(string)
		if id == "" || mem == "" {
			return nil
		}
		return &Operation{
			Type:     OperationUpdate,
			MemoryID: id,
			Memory:   mem,
			Topics:   toStringSlice(args[argKeyTopics]),
		}

	case memory.DeleteToolName:
		id, _ := args[argKeyMemoryID].(string)
		if id == "" {
			return nil
		}
		return &Operation{
			Type:     OperationDelete,
			MemoryID: id,
		}

	case memory.ClearToolName:
		return &Operation{
			Type: OperationClear,
		}

	default:
		return nil
	}
}

// toStringSlice converts an any value to []string.
// Always returns an empty slice instead of nil for consistent downstream handling.
func toStringSlice(v any) []string {
	if v == nil {
		return []string{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
