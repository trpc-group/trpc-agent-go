//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type selfDeferredDummyTool struct{ dummyTool }

func (selfDeferredDummyTool) ShouldDefer(context.Context) bool { return true }

type metadataDeferralDummyTool struct{ dummyTool }

func (metadataDeferralDummyTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}
}

func TestLLMAgent_DeferredToolsKeepMetadataForFilters(t *testing.T) {
	original := metadataDeferralDummyTool{dummyTool{decl: &tool.Declaration{Name: "lookup"}}}
	agt := New("deferral-agent", WithTools([]tool.Tool{original}), WithDeferredTools(nil),
		WithToolFilter(func(_ context.Context, tl tool.Tool) bool {
			return tool.MetadataOf(tl).ReadOnly
		}))
	tools := agt.FilterTools(context.Background())
	require.Len(t, tools, 1)
	require.Equal(t, tool.MetadataOf(original), tool.MetadataOf(tools[0]))
	require.True(t, tool.ShouldDefer(context.Background(), tools[0]))
}

func deferralDummyTool(name string) tool.Tool {
	return dummyTool{decl: &tool.Declaration{Name: name}}
}

func deferredByName(tools []tool.Tool) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, tl := range tools {
		out[tl.Declaration().Name] = tool.ShouldDefer(context.Background(), tl)
	}
	return out
}

func TestLLMAgent_DeferredToolsKeepDefaultsWithoutPolicy(t *testing.T) {
	weather := deferralDummyTool("weather")
	self := selfDeferredDummyTool{dummyTool{decl: &tool.Declaration{Name: "self"}}}
	agt := New("deferral-agent",
		WithTools([]tool.Tool{weather, self}),
		WithToolSets([]tool.ToolSet{&dynamicToolSet{name: "crm", tools: []tool.Tool{deferralDummyTool("lookup")}}}),
	)
	tools := agt.Tools()
	require.Contains(t, tools, weather, "tools are not wrapped without a deferral policy")
	require.Equal(t, map[string]bool{"weather": false, "self": true, "crm_lookup": false},
		deferredByName(tools), "tools keep their own preference")
}

func TestLLMAgent_DeferredToolsAndToolSets(t *testing.T) {
	var seen []string
	agt := New("deferral-agent",
		WithTools([]tool.Tool{
			deferralDummyTool("weather"),
			deferralDummyTool("eta"),
			selfDeferredDummyTool{dummyTool{decl: &tool.Declaration{Name: "self"}}},
		}),
		WithToolSets([]tool.ToolSet{
			&dynamicToolSet{name: "crm", tools: []tool.Tool{deferralDummyTool("lookup"), deferralDummyTool("orders")}},
			&dynamicToolSet{name: "billing", tools: []tool.Tool{deferralDummyTool("lookup")}},
			&dynamicToolSet{name: "docs", tools: []tool.Tool{deferralDummyTool("read")}},
		}),
		WithDeferredTools(tool.NewIncludeToolNamesFilter("eta")),
		WithDeferredToolSet(" crm ", func(_ context.Context, tl tool.Tool) bool {
			seen = append(seen, tl.Declaration().Name)
			return tl.Declaration().Name == "orders"
		}),
		WithDeferredToolSet("docs", nil),
		WithToolSetToolNameMode("docs", tool.ToolSetToolNameModeOriginal),
	)
	tools := agt.Tools()
	require.Equal(t, map[string]bool{
		"weather": false, "eta": true, "self": false,
		"crm_lookup": false, "crm_orders": true, "billing_lookup": false, "read": true,
	}, deferredByName(tools))
	require.Subset(t, seen, []string{"lookup", "orders"}, "ToolSet filters see original member names")

	for _, tl := range tools {
		if tl.Declaration().Name != "crm_orders" {
			continue
		}
		toolSetName, memberName, _, ok := itool.ToolSetMember(tl)
		require.True(t, ok)
		require.Equal(t, "crm", toolSetName)
		require.Equal(t, "orders", memberName)
	}
}

func TestLLMAgent_DeferredToolSetFollowsRefreshedMembers(t *testing.T) {
	set := &dynamicToolSet{name: "mcp", tools: []tool.Tool{deferralDummyTool("list")}}
	agt := New("deferral-agent",
		WithToolSets([]tool.ToolSet{set}),
		WithRefreshToolSetsOnRun(true),
		WithDeferredToolSet("mcp", tool.NewExcludeToolNamesFilter("list")),
	)
	require.Equal(t, map[string]bool{"mcp_list": false}, deferredByName(agt.Tools()))
	set.tools = append(set.tools, deferralDummyTool("export"))
	require.Equal(t, map[string]bool{"mcp_list": false, "mcp_export": true}, deferredByName(agt.Tools()),
		"members added by a refresh are covered by the policy")

	filtered := New("deferral-agent",
		WithToolSets([]tool.ToolSet{set}),
		WithRefreshToolSetsOnRun(true),
		WithDeferredToolSet("mcp", nil),
		WithToolFilter(tool.NewExcludeToolNamesFilter("mcp_export")),
	)
	require.Equal(t, map[string]bool{"mcp_list": true}, deferredByName(filtered.FilterTools(context.Background())),
		"agent filters apply to deferred tools")
}

func TestLLMAgent_DeferredActivatableToolSet(t *testing.T) {
	repo, err := skill.NewFSRepository(createNamedTestSkill(t, "research", "research skill"))
	require.NoError(t, err)
	mockModel := &activationSequenceModel{responses: []*model.Response{
		activationToolCallResponse(t, "call-1", "research"),
		activationFinalResponse("done"),
	}}
	agt := New("agent",
		WithModel(mockModel),
		WithSkills(repo),
		WithActivatableToolSets([]tool.ToolSet{activationToolSet{
			name:  "github",
			tools: []tool.Tool{activationTool{name: "search"}, activationTool{name: "issues"}},
		}}),
		WithDeferredToolSet("github", tool.NewIncludeToolNamesFilter("issues")),
		WithToolActivationOnSkillLoad("research", []string{"github"}),
	)
	inv := &agent.Invocation{
		InvocationID: "inv",
		Session:      session.NewSession("app", "user", "session"),
		Message:      model.NewUserMessage("load research"),
	}
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}
	requests := mockModel.Requests()
	require.Len(t, requests, 2)
	ctx := context.Background()
	require.False(t, tool.ShouldDefer(ctx, requests[1].Tools["github_search"]))
	require.True(t, tool.ShouldDefer(ctx, requests[1].Tools["github_issues"]),
		"activated members follow the ToolSet policy")
}

func TestWithDeferredToolSetValidation(t *testing.T) {
	require.PanicsWithValue(t,
		"Invalid LLMAgent configuration: tool set name for deferred tools must not be empty",
		func() { _ = New("deferral-agent", WithDeferredToolSet(" ", nil)) },
	)
	require.PanicsWithValue(t,
		"Invalid LLMAgent configuration: deferred tool set \"missing\" is not registered",
		func() { _ = New("deferral-agent", WithDeferredToolSet("missing", nil)) },
	)
}
