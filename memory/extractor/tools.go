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

// writeToolDeclarations returns tool declarations for memory write operations.
// These are used to build model request tools for auto memory extraction.
// Only write tools (add/update/delete) are included since read tools are
// exposed to the agent directly.
func writeToolDeclarations() []*tool.Declaration {
	writeTools := []tool.CallableTool{
		memorytool.NewAddTool(),
		memorytool.NewUpdateTool(),
		memorytool.NewDeleteTool(),
	}
	declarations := make([]*tool.Declaration, 0, len(writeTools))
	for _, t := range writeTools {
		declarations = append(declarations, t.Declaration())
	}
	return declarations
}

// buildToolsMap builds a map of tool name to tool for model request.
// The tools are declaration-only and not callable.
func buildToolsMap() map[string]tool.Tool {
	declarations := writeToolDeclarations()
	tools := make(map[string]tool.Tool, len(declarations))
	for _, decl := range declarations {
		tools[decl.Name] = &declarationOnlyTool{decl: decl}
	}
	return tools
}

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
