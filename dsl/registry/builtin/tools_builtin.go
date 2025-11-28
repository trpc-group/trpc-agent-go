//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package builtin

import (
	"log"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
)

func init() {
	// Auto-register built-in tools and toolsets at package init time
	// This happens when importing _ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin"
	registerBuiltinTools()
	registerBuiltinToolSets()
}

// registerBuiltinTools registers all built-in tools to the DefaultToolRegistry.
func registerBuiltinTools() {
	// Register DuckDuckGo search tool
	ddgTool := duckduckgo.NewTool()
	registry.DefaultToolRegistry.MustRegister("duckduckgo_search", ddgTool)
}

// registerBuiltinToolSets registers all built-in ToolSets to the DefaultToolSetRegistry.
func registerBuiltinToolSets() {
	if err := registry.RegisterBuiltinToolSets(registry.DefaultToolSetRegistry); err != nil {
		log.Printf("Warning: failed to register built-in toolsets: %v", err)
	}
}
