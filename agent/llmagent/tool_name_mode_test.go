//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestLLMAgent_ToolSetToolNameMode(t *testing.T) {
	agent := New(
		"name-mode-agent",
		WithToolSets([]tool.ToolSet{
			dummyToolSet{name: "github"},
		}),
		WithToolSetToolNameMode("github", tool.ToolSetToolNameModeOriginal),
	)

	names := make(map[string]bool)
	for _, tl := range agent.Tools() {
		names[tl.Declaration().Name] = true
	}
	require.True(t, names[testKnowledgeToolName])
	require.False(t, names["github_"+testKnowledgeToolName])
}

func TestLLMAgent_RefreshToolSetToolNameMode(t *testing.T) {
	agent := New(
		"name-mode-agent",
		WithToolSets([]tool.ToolSet{
			&dynamicToolSet{
				name: "github",
				tools: []tool.Tool{
					dummyTool{decl: &tool.Declaration{Name: "search"}},
				},
			},
		}),
		WithRefreshToolSetsOnRun(true),
		WithToolSetToolNameMode("github", tool.ToolSetToolNameModeOriginal),
	)

	names := make(map[string]bool)
	for _, tl := range agent.Tools() {
		names[tl.Declaration().Name] = true
	}
	require.True(t, names["search"])
	require.False(t, names["github_search"])
}

func TestWithToolSetToolNameModeValidation(t *testing.T) {
	require.PanicsWithValue(t,
		"Invalid LLMAgent configuration: tool set name for tool name mode must not be empty",
		func() {
			_ = New(
				"name-mode-agent",
				WithToolSetToolNameMode(" ", tool.ToolSetToolNameModeOriginal),
			)
		},
	)
	require.PanicsWithValue(t,
		"Invalid LLMAgent configuration: unsupported tool name mode 99 for tool set \"github\"",
		func() {
			_ = New(
				"name-mode-agent",
				WithToolSetToolNameMode("github", tool.ToolSetToolNameMode(99)),
			)
		},
	)
	require.PanicsWithValue(t,
		"Invalid LLMAgent configuration: tool set \"missing\" is not registered",
		func() {
			_ = New(
				"name-mode-agent",
				WithToolSetToolNameMode("missing", tool.ToolSetToolNameModeOriginal),
			)
		},
	)
}
