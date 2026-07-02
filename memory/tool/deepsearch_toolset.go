//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"fmt"

	coretool "trpc.group/trpc-go/trpc-agent-go/tool"
)

// DeepSearchToolSetName is the activatable DeepSearch tool set name.
const DeepSearchToolSetName = "memory_deepsearch"

type deepSearchToolSet struct{}

// NewDeepSearchToolSet creates the complete activatable DeepSearch tool set.
func NewDeepSearchToolSet() coretool.ToolSet {
	return deepSearchToolSet{}
}

func (deepSearchToolSet) Tools(context.Context) []coretool.Tool {
	tools := []coretool.Tool{
		NewCueSearchTool(),
		NewTagExpandTool(),
		NewContentLoadTool(),
		NewEdgesByTagTool(),
		NewQueryConversationTimeTool(),
		NewQueryEventKeywordsTool(),
		NewQueryEventContextTool(),
		NewQueryPersonalInformationTool(),
		NewQueryPersonalAspectTool(),
		NewQueryTopicEventsTool(),
	}
	result := make([]coretool.Tool, 0, len(tools))
	for _, tl := range tools {
		result = append(result, relativeDeepSearchTool{tool: tl})
	}
	return result
}

func (deepSearchToolSet) Close() error {
	return nil
}

func (deepSearchToolSet) Name() string {
	return DeepSearchToolSetName
}

// relativeDeepSearchTool lets the runtime apply the tool-set prefix exactly
// once while preserving the public memory_deepsearch_* declarations.
type relativeDeepSearchTool struct {
	tool coretool.Tool
}

func (t relativeDeepSearchTool) Declaration() *coretool.Declaration {
	declaration := *t.tool.Declaration()
	const prefix = DeepSearchToolSetName + "_"
	if len(declaration.Name) >= len(prefix) && declaration.Name[:len(prefix)] == prefix {
		declaration.Name = declaration.Name[len(prefix):]
	}
	return &declaration
}

func (t relativeDeepSearchTool) Call(ctx context.Context, args []byte) (any, error) {
	callable, ok := t.tool.(coretool.CallableTool)
	if !ok {
		return nil, fmt.Errorf("deepsearch tool %q is not callable", t.tool.Declaration().Name)
	}
	return callable.Call(ctx, args)
}
