//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func shouldRefreshToolSetOnStep(
	refreshToolSetsOnRun bool,
	toolSet tool.ToolSet,
) bool {
	return refreshToolSetsOnRun || itool.IsStepDynamicToolSet(toolSet)
}

func filterToolSetsForStep(
	refreshToolSetsOnRun bool,
	toolSets []tool.ToolSet,
) []tool.ToolSet {
	if len(toolSets) == 0 {
		return nil
	}
	out := make([]tool.ToolSet, 0, len(toolSets))
	for _, toolSet := range toolSets {
		if shouldRefreshToolSetOnStep(refreshToolSetsOnRun, toolSet) {
			out = append(out, toolSet)
		}
	}
	return out
}

func filterStaticToolSets(
	refreshToolSetsOnRun bool,
	toolSets []tool.ToolSet,
) []tool.ToolSet {
	if refreshToolSetsOnRun || len(toolSets) == 0 {
		return nil
	}
	out := make([]tool.ToolSet, 0, len(toolSets))
	for _, toolSet := range toolSets {
		if shouldRefreshToolSetOnStep(false, toolSet) {
			continue
		}
		out = append(out, toolSet)
	}
	return out
}
