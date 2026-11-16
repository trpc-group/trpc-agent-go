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
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

type selfDeferredDummyTool struct{ dummyTool }

func (selfDeferredDummyTool) ShouldDefer(context.Context) bool { return true }

type metadataDeferralDummyTool struct{ dummyTool }

func (metadataDeferralDummyTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}
}

func TestLLMAgent_DeferredToolsKeepMetadataForFilters(t *testing.T) {
	original := metadataDeferralDummyTool{dummyTool{decl: &tool.Declaration{Name: "lookup"}}}
	agt := New("deferral-agent", WithTools([]tool.Tool{deferred.Tool(original)}),
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

func TestLLMAgent_DeferredToolsKeepDefaultsWithoutWrapper(t *testing.T) {
	weather := deferralDummyTool("weather")
	self := selfDeferredDummyTool{dummyTool{decl: &tool.Declaration{Name: "self"}}}
	agt := New("deferral-agent",
		WithTools([]tool.Tool{weather, self}),
		WithToolSets([]tool.ToolSet{&dynamicToolSet{name: "crm", tools: []tool.Tool{deferralDummyTool("lookup")}}}),
	)
	tools := agt.Tools()
	require.Contains(t, tools, weather)
	require.Equal(t, map[string]bool{"weather": false, "self": true, "crm_lookup": false},
		deferredByName(tools), "unwrapped tools keep their own preference")
}

func TestLLMAgent_DeferredToolsAndToolSets(t *testing.T) {
	var seen []string
	agt := New("deferral-agent",
		WithTools([]tool.Tool{
			deferralDummyTool("weather"),
			deferred.Tool(deferralDummyTool("eta")),
			deferred.Tool(selfDeferredDummyTool{dummyTool{decl: &tool.Declaration{Name: "self"}}},
				deferred.WithFilter(tool.NewIncludeToolNamesFilter("other"))),
		}),
		WithToolSets([]tool.ToolSet{
			deferred.ToolSet(&dynamicToolSet{name: "crm", tools: []tool.Tool{
				deferralDummyTool("lookup"), deferralDummyTool("orders"),
			}}, deferred.WithFilter(func(_ context.Context, tl tool.Tool) bool {
				seen = append(seen, tl.Declaration().Name)
				return tl.Declaration().Name == "orders"
			})),
			&dynamicToolSet{name: "billing", tools: []tool.Tool{deferralDummyTool("lookup")}},
			deferred.ToolSet(&dynamicToolSet{name: "docs", tools: []tool.Tool{deferralDummyTool("read")}}),
		}),
		WithToolSetToolNameMode("docs", tool.ToolSetToolNameModeOriginal),
	)
	tools := agt.Tools()
	require.Equal(t, map[string]bool{
		"weather": false, "eta": true, "self": false,
		"crm_lookup": false, "crm_orders": true, "billing_lookup": false, "read": true,
	}, deferredByName(tools))
	require.ElementsMatch(t, []string{"lookup", "orders"}, seen, "ToolSet filters see original member names")

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

func TestLLMAgent_SharedToolsHaveAgentSpecificDeferral(t *testing.T) {
	sharedTool := &dummyTool{decl: &tool.Declaration{Name: "weather"}}
	sharedSet := &dynamicToolSet{name: "crm", tools: []tool.Tool{deferralDummyTool("lookup")}}
	eager := New("eager",
		WithTools([]tool.Tool{sharedTool}),
		WithToolSets([]tool.ToolSet{sharedSet}),
		WithRefreshToolSetsOnRun(true),
	)
	lazy := New("lazy",
		WithTools([]tool.Tool{deferred.Tool(sharedTool)}),
		WithToolSets([]tool.ToolSet{deferred.ToolSet(sharedSet)}),
		WithRefreshToolSetsOnRun(true),
	)
	require.Equal(t, map[string]bool{"weather": true, "crm_lookup": true}, deferredByName(lazy.Tools()))
	require.Equal(t, map[string]bool{"weather": false, "crm_lookup": false}, deferredByName(eager.Tools()))

	sharedSet.tools = append(sharedSet.tools, deferralDummyTool("orders"))
	require.Equal(t, map[string]bool{"weather": true, "crm_lookup": true, "crm_orders": true}, deferredByName(lazy.Tools()))
	require.Equal(t, map[string]bool{"weather": false, "crm_lookup": false, "crm_orders": false}, deferredByName(eager.Tools()))
}

func TestLLMAgent_DeferredToolSetFollowsRefreshedMembers(t *testing.T) {
	set := &dynamicToolSet{name: "mcp", tools: []tool.Tool{deferralDummyTool("list")}}
	mockModel := &activationSequenceModel{responses: []*model.Response{
		activationFinalResponse("first"), activationFinalResponse("second"),
	}}
	agt := New("deferral-agent",
		WithModel(mockModel),
		WithToolSets([]tool.ToolSet{deferred.ToolSet(set, deferred.WithFilter(tool.NewExcludeToolNamesFilter("list")))}),
		WithRefreshToolSetsOnRun(true),
	)
	require.Equal(t, map[string]bool{"mcp_list": false}, deferredByName(agt.Tools()))
	run := func(sessionID string) {
		inv := &agent.Invocation{
			InvocationID: sessionID,
			Session:      session.NewSession("app", "user", sessionID),
			Message:      model.NewUserMessage("use mcp"),
		}
		events, err := agt.Run(context.Background(), inv)
		require.NoError(t, err)
		for range events {
		}
	}
	run("first")
	set.tools = append(set.tools, deferralDummyTool("export"))
	require.Equal(t, map[string]bool{"mcp_list": false, "mcp_export": true}, deferredByName(agt.Tools()),
		"members added by a refresh are covered by the wrapper")
	run("second")
	requests := mockModel.Requests()
	require.Len(t, requests, 2)
	require.Contains(t, requests[0].Tools, "mcp_list")
	require.NotContains(t, requests[0].Tools, "mcp_export")
	require.Contains(t, requests[1].Tools, "mcp_export")
	require.False(t, tool.ShouldDefer(context.Background(), requests[1].Tools["mcp_list"]))
	require.True(t, tool.ShouldDefer(context.Background(), requests[1].Tools["mcp_export"]))

	filtered := New("deferral-agent",
		WithToolSets([]tool.ToolSet{deferred.ToolSet(set)}),
		WithRefreshToolSetsOnRun(true),
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
	set := &dynamicToolSet{name: "github", tools: []tool.Tool{activationTool{name: "search"}}}
	agt := New("agent",
		WithModel(mockModel),
		WithSkills(repo),
		WithActivatableToolSets([]tool.ToolSet{
			deferred.ToolSet(set, deferred.WithFilter(tool.NewIncludeToolNamesFilter("issues"))),
		}),
		WithToolActivationOnSkillLoad("research", []string{"github"}),
	)
	set.tools = append(set.tools, activationTool{name: "issues"})
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
	require.Contains(t, requests[1].Tools, "github_search")
	require.Contains(t, requests[1].Tools, "github_issues")
	require.False(t, tool.ShouldDefer(ctx, requests[1].Tools["github_search"]))
	require.True(t, tool.ShouldDefer(ctx, requests[1].Tools["github_issues"]),
		"activated members follow the ToolSet wrapper")
}

func TestLLMAgent_DeferredAdditionalTool(t *testing.T) {
	mockModel := &activationSequenceModel{responses: []*model.Response{activationFinalResponse("done")}}
	agt := New("agent", WithModel(mockModel), WithTools([]tool.Tool{deferralDummyTool("static")}))
	inv := &agent.Invocation{
		InvocationID: "inv",
		Session:      session.NewSession("app", "user", "session"),
		Message:      model.NewUserMessage("use extra tool"),
		RunOptions: agent.RunOptions{
			AdditionalTools: []tool.Tool{deferred.Tool(deferralDummyTool("runtime"))},
		},
	}
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}
	requests := mockModel.Requests()
	require.Len(t, requests, 1)
	require.Contains(t, requests[0].Tools, "static")
	require.Contains(t, requests[0].Tools, "runtime")
	require.False(t, tool.ShouldDefer(context.Background(), requests[0].Tools["static"]))
	require.True(t, tool.ShouldDefer(context.Background(), requests[0].Tools["runtime"]))
}

func TestLLMAgent_AddDeferredToolSet(t *testing.T) {
	agt := New("agent")
	set := &dynamicToolSet{name: "runtime", tools: []tool.Tool{deferralDummyTool("lookup")}}
	agt.AddToolSet(deferred.ToolSet(set))
	require.Equal(t, map[string]bool{"runtime_lookup": true}, deferredByName(agt.Tools()))
}
