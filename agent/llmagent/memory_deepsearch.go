//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// WithMemoryDeepSearch enables progressive DeepSearch tools for this agent.
// It preserves all tools already configured by the caller. The memory service
// attached to the invocation must separately implement deepsearch.Service.
func WithMemoryDeepSearch() Option {
	return func(options *Options) {
		options.memoryDeepSearch = true
	}
}

func applyMemoryDeepSearch(options *Options) {
	if options == nil || !options.memoryDeepSearch {
		return
	}
	options.Tools = appendToolIfMissing(
		options.Tools,
		memorytool.NewDeepSearchTool(),
	)
	if !hasToolSet(options.activatableToolSets, memorytool.DeepSearchToolSetName) {
		options.activatableToolSets = append(
			options.activatableToolSets,
			memorytool.NewDeepSearchToolSet(),
		)
	}
	options.toolActivationRules = append(
		options.toolActivationRules,
		toolActivationRule{
			trigger: toolActivationTrigger{
				kind:     toolActivationTriggerToolResult,
				toolName: memory.DeepSearchToolName,
			},
			toolSetNames: []string{memorytool.DeepSearchToolSetName},
			mode:         ToolActivationModeInclude,
			lifetime:     ToolActivationLifetimeInvocation,
		},
	)
}

func appendToolIfMissing(tools []tool.Tool, candidate tool.Tool) []tool.Tool {
	name := candidate.Declaration().Name
	for _, existing := range tools {
		if existing != nil && existing.Declaration().Name == name {
			return tools
		}
	}
	return append(tools, candidate)
}

func hasToolSet(toolSets []tool.ToolSet, name string) bool {
	for _, toolSet := range toolSets {
		if toolSet != nil && toolSet.Name() == name {
			return true
		}
	}
	return false
}
