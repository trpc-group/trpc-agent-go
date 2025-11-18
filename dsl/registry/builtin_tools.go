//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package registry

import (
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
)

// DefaultToolRegistry is the global default tool registry.
// Built-in tools are automatically registered here when you import:
//
//	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin"
var DefaultToolRegistry = NewToolRegistry()

// registerBuiltinTools registers all built-in tools to the given registry.
func registerBuiltinTools(registry *ToolRegistry) {
	// Register DuckDuckGo search tool
	ddgTool := duckduckgo.NewTool()
	registry.MustRegister("duckduckgo_search", ddgTool)
}

// RegisterBuiltinTools registers all built-in tools to a custom registry.
// This is useful when you want to create a custom tool registry with built-in tools.
func RegisterBuiltinTools(registry *ToolRegistry) {
	registerBuiltinTools(registry)
}

// NewToolRegistryWithBuiltins creates a new ToolRegistry with built-in tools pre-registered.
func NewToolRegistryWithBuiltins() *ToolRegistry {
	reg := NewToolRegistry()
	registerBuiltinTools(reg)
	return reg
}

// GetBuiltinTools returns a list of all built-in tool names.
func GetBuiltinTools() []string {
	return []string{
		"duckduckgo_search",
	}
}

// IsBuiltinTool checks if a tool name is a built-in tool.
func IsBuiltinTool(name string) bool {
	builtins := GetBuiltinTools()
	for _, builtin := range builtins {
		if builtin == name {
			return true
		}
	}
	return false
}

// GetBuiltinTool creates a new instance of a built-in tool by name.
// Returns nil if the tool name is not a built-in tool.
func GetBuiltinTool(name string) tool.Tool {
	switch name {
	case "duckduckgo_search":
		return duckduckgo.NewTool()
	default:
		return nil
	}
}
