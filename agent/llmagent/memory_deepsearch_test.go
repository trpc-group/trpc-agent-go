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
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestWithMemoryDeepSearchPreservesExistingTools(t *testing.T) {
	base := activationTool{name: memory.SearchToolName}
	agent := New(
		"assistant",
		WithTools([]tool.Tool{base}),
		WithMemoryDeepSearch(),
	)

	require.Len(t, agent.option.Tools, 2)
	require.Equal(t, memory.SearchToolName, agent.option.Tools[0].Declaration().Name)
	require.Equal(t, memory.DeepSearchToolName, agent.option.Tools[1].Declaration().Name)
	require.Len(t, agent.option.activatableToolSets, 1)
	require.Equal(t, memorytool.DeepSearchToolSetName, agent.option.activatableToolSets[0].Name())
}

func TestWithMemoryDeepSearchToolNamesHaveSinglePrefix(t *testing.T) {
	toolSet := memorytool.NewDeepSearchToolSet()
	tools := itool.NewNamedToolSet(toolSet).Tools(context.Background())
	require.NotEmpty(t, tools)

	seen := make(map[string]bool, len(tools))
	for _, deepSearchTool := range tools {
		name := deepSearchTool.Declaration().Name
		require.True(t, strings.HasPrefix(name, memory.DeepSearchToolName+"_"), name)
		require.False(t, strings.Contains(name, memory.DeepSearchToolName+"_memory_"), name)
		require.False(t, seen[name], name)
		seen[name] = true
	}
}

func TestWithMemoryDeepSearchIsOrderIndependent(t *testing.T) {
	agent := New(
		"assistant",
		WithMemoryDeepSearch(),
		WithTools([]tool.Tool{activationTool{name: memory.LoadToolName}}),
	)

	require.Len(t, agent.option.Tools, 2)
	require.Equal(t, memory.LoadToolName, agent.option.Tools[0].Declaration().Name)
	require.Equal(t, memory.DeepSearchToolName, agent.option.Tools[1].Declaration().Name)
}
