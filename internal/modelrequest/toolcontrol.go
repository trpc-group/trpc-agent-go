//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package modelrequest coordinates internal model request controls.
package modelrequest

import "context"

type toolsDisabledKey struct{}

// WithToolsDisabled marks provider-level tool configuration as disabled.
func WithToolsDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, toolsDisabledKey{}, true)
}

// ToolsDisabled reports whether provider-level tool configuration is disabled.
func ToolsDisabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	disabled, _ := ctx.Value(toolsDisabledKey{}).(bool)
	return disabled
}

// DeleteToolControlFields removes provider-specific fields that can declare or
// force tool use.
func DeleteToolControlFields(fields map[string]any) {
	for key := range fields {
		if IsToolControlField(key) {
			delete(fields, key)
		}
	}
}

// FilterToolControlFields returns fields without provider-specific tool
// controls when filtering is enabled. It returns the original map otherwise.
func FilterToolControlFields(
	fields map[string]any,
	enabled bool,
) map[string]any {
	if !enabled {
		return fields
	}
	filtered := make(map[string]any, len(fields))
	for key, value := range fields {
		if !IsToolControlField(key) {
			filtered[key] = value
		}
	}
	return filtered
}

// IsToolControlField reports whether a provider-specific field can declare or
// force tool use.
func IsToolControlField(key string) bool {
	switch key {
	case "tool_choice",
		"parallel_tool_calls",
		"tools",
		"function_call",
		"functions",
		"ToolChoice",
		"ParallelToolCalls",
		"Tools":
		return true
	default:
		return false
	}
}
