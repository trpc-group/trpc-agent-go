//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	testToolNameA             = "a"
	testDummyToolSetName      = "dummy"
	testUserToolNameOne       = "user_tool_1"
	testUserToolNameTwo       = "user_tool_2"
	testTransferToolName      = "transfer_to_agent"
	testKnowledgeToolName     = "knowledge_search"
	testDynamicToolSetName    = "dynamic"
	testFirstToolSetName      = "set_one"
	testSecondToolSetName     = "set_two"
	testPrefixedKnowledgeTail = "_knowledge_search"
	testFilterAgentName       = "filter-agent"
	testSubAgentName          = "sub-agent"
)

// minimalKnowledge implements knowledge.Knowledge with no-op behaviors for unit tests.
type minimalKnowledge struct{}

func (m *minimalKnowledge) Search(_ context.Context, _ *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
	return nil, nil
}

// dummyToolSet returns a fixed tool for coverage.
type dummyToolSet struct {
	name string
}

func (d dummyToolSet) Tools(ctx context.Context) []tool.Tool {
	// Wrap the tool to a CallableTool by asserting to the known concrete type.
	kt := knowledgetool.NewKnowledgeSearchTool(&minimalKnowledge{})
	type callable interface{ tool.CallableTool }
	if c, ok := any(kt).(callable); ok {
		return []tool.Tool{c}
	}
	return nil
}
func (d dummyToolSet) Close() error { return nil }
func (d dummyToolSet) Name() string { return d.name }

// dummyTool implements tool.Tool.
type dummyTool struct{ decl *tool.Declaration }

func (d dummyTool) Call(_ context.Context, _ []byte) (any, error) { return nil, nil }
func (d dummyTool) Declaration() *tool.Declaration                { return d.decl }

func TestRegisterTools_Combinations(t *testing.T) {
	base := []tool.Tool{
		dummyTool{decl: &tool.Declaration{Name: testToolNameA}},
	}
	sets := []tool.ToolSet{dummyToolSet{name: testDummyToolSetName}}
	kb := &minimalKnowledge{}

	// with tools, toolset and knowledge and nil memory.
	tools, userToolNames := registerTools(&Options{Tools: base, ToolSets: sets, Knowledge: kb})
	if len(tools) < 2 {
		t.Fatalf("expected aggregated tools from base and toolset")
	}

	// Verify user tool tracking
	if len(userToolNames) == 0 {
		t.Fatalf("expected user tool names to be tracked")
	}

	// User tool from WithTools should be tracked
	if !userToolNames[testToolNameA] {
		t.Errorf("expected tool 'a' to be tracked as user tool")
	}

	// Knowledge search tool should NOT be tracked as user tool
	if userToolNames[testKnowledgeToolName] {
		t.Errorf("knowledge_search should not be tracked as user tool")
	}
}

func TestLLMAgent_Tools_IncludesTransferWhenSubAgents(t *testing.T) {
	sub1 := New("sub-1")
	agt := New("main", WithSubAgents([]agent.Agent{sub1}))

	ts := agt.Tools()
	foundTransfer := false
	for _, tl := range ts {
		if tl.Declaration().Name == testTransferToolName {
			foundTransfer = true
			break
		}
	}
	if !foundTransfer {
		t.Fatalf("expected transfer_to_agent tool when sub agents exist")
	}
}

func TestLLMAgent_UserTools(t *testing.T) {
	// Create agent with user tools, toolsets, knowledge, and subagents
	userTool1 := dummyTool{
		decl: &tool.Declaration{Name: testUserToolNameOne},
	}
	userTool2 := dummyTool{
		decl: &tool.Declaration{Name: testUserToolNameTwo},
	}
	toolSet := dummyToolSet{name: "test_toolset"}
	kb := &minimalKnowledge{}
	subAgent := New("sub-agent")

	agent := New("test-agent",
		WithTools([]tool.Tool{userTool1, userTool2}),
		WithToolSets([]tool.ToolSet{toolSet}),
		WithKnowledge(kb),
		WithSubAgents([]agent.Agent{subAgent}),
	)

	// Get all tools
	allTools := agent.Tools()

	// Get user tools
	userTools := agent.UserTools()

	// All tools should include user tools + framework tools
	// User tools should only include tools from WithTools and WithToolSets

	// Verify user tools count (should be user_tool_1, user_tool_2, + toolset tools)
	if len(userTools) < 2 {
		t.Errorf("expected at least 2 user tools, got %d", len(userTools))
	}

	// Verify all tools count (should include knowledge_search and transfer_to_agent)
	if len(allTools) <= len(userTools) {
		t.Errorf("all tools (%d) should be more than user tools (%d)", len(allTools), len(userTools))
	}

	// Verify that user tools include the explicitly registered tools
	foundUserTool1 := false
	foundUserTool2 := false
	for _, tool := range userTools {
		switch tool.Declaration().Name {
		case testUserToolNameOne:
			foundUserTool1 = true
		case testUserToolNameTwo:
			foundUserTool2 = true
		}
	}

	if !foundUserTool1 || !foundUserTool2 {
		t.Errorf("user tools should include user_tool_1 and user_tool_2")
	}

	// Verify that framework tools are NOT in user tools
	for _, tool := range userTools {
		name := tool.Declaration().Name
		if name == testKnowledgeToolName || name == testTransferToolName {
			t.Errorf("framework tool %s should not be in user tools", name)
		}
	}
}

func TestLLMAgent_UserTools_EmptyCase(t *testing.T) {
	// Agent with no user tools, only framework tools
	kb := &minimalKnowledge{}
	subAgent := New("sub-agent")

	agent := New("test-agent",
		WithKnowledge(kb),
		WithSubAgents([]agent.Agent{subAgent}),
	)

	// Get user tools - should be empty
	userTools := agent.UserTools()

	if len(userTools) != 0 {
		t.Errorf("expected no user tools, got %d", len(userTools))
	}

	// Get all tools - should have framework tools
	allTools := agent.Tools()

	foundKnowledge := false
	foundTransfer := false
	for _, tool := range allTools {
		switch tool.Declaration().Name {
		case testKnowledgeToolName:
			foundKnowledge = true
		case testTransferToolName:
			foundTransfer = true
		}
	}

	if !foundKnowledge || !foundTransfer {
		t.Errorf("framework tools should be in all tools even when no user tools")
	}
}

type dynamicToolSet struct {
	name  string
	tools []tool.Tool
}

func (d *dynamicToolSet) Tools(_ context.Context) []tool.Tool {
	return d.tools
}

func (d *dynamicToolSet) Close() error {
	return nil
}

func (d *dynamicToolSet) Name() string {
	return d.name
}

func TestLLMAgent_RefreshToolSetsOnRun(t *testing.T) {
	staticTool := dummyTool{
		decl: &tool.Declaration{Name: "static_tool"},
	}

	dyn := &dynamicToolSet{
		name: "dyn",
		tools: []tool.Tool{
			dummyTool{
				decl: &tool.Declaration{Name: "dynamic_tool_v1"},
			},
		},
	}

	agent := New(
		"dynamic-agent",
		WithTools([]tool.Tool{staticTool}),
		WithToolSets([]tool.ToolSet{dyn}),
		WithRefreshToolSetsOnRun(true),
	)

	toolsV1 := agent.Tools()

	foundStatic := false
	foundDynV1 := false

	for _, tl := range toolsV1 {
		switch tl.Declaration().Name {
		case "static_tool":
			foundStatic = true
		case "dyn_dynamic_tool_v1":
			foundDynV1 = true
		}
	}

	if !foundStatic || !foundDynV1 {
		t.Fatalf(
			"expected static_tool and dyn_dynamic_tool_v1, got %+v",
			toolsV1,
		)
	}

	dyn.tools = []tool.Tool{
		dummyTool{
			decl: &tool.Declaration{Name: "dynamic_tool_v2"},
		},
	}

	toolsV2 := agent.Tools()

	foundDynV2 := false
	for _, tl := range toolsV2 {
		switch tl.Declaration().Name {
		case "static_tool":
			foundStatic = true
		case "dyn_dynamic_tool_v2":
			foundDynV2 = true
		}
	}

	if !foundStatic || !foundDynV2 {
		t.Fatalf(
			"expected static_tool and dyn_dynamic_tool_v2 after refresh, got %+v",
			toolsV2,
		)
	}

	userTools := agent.UserTools()
	for _, tl := range userTools {
		name := tl.Declaration().Name
		if name == "knowledge_search" || name == "transfer_to_agent" {
			t.Fatalf(
				"framework tool %s should not appear in user tools",
				name,
			)
		}
	}
}

func TestLLMAgent_AddToolSet_DynamicTools(t *testing.T) {
	baseTool := dummyTool{
		decl: &tool.Declaration{Name: testUserToolNameOne},
	}
	agent := New("dynamic-agent",
		WithTools([]tool.Tool{baseTool}),
	)

	initialTools := agent.Tools()
	if len(initialTools) != 1 {
		t.Fatalf("expected 1 tool before add, got %d",
			len(initialTools))
	}

	toolSet := dummyToolSet{name: testDynamicToolSetName}
	agent.AddToolSet(toolSet)

	tools := agent.Tools()
	if len(tools) <= len(initialTools) {
		t.Fatalf("expected more tools after add, got %d",
			len(tools))
	}

	prefixedName := testDynamicToolSetName + testPrefixedKnowledgeTail
	foundPrefixed := false
	for _, tl := range tools {
		if tl.Declaration().Name == prefixedName {
			foundPrefixed = true
			break
		}
	}
	if !foundPrefixed {
		t.Fatalf("expected tool %q after adding toolset",
			prefixedName)
	}

	userTools := agent.UserTools()
	foundUserBase := false
	foundUserFromSet := false
	for _, tl := range userTools {
		switch tl.Declaration().Name {
		case testUserToolNameOne:
			foundUserBase = true
		case prefixedName:
			foundUserFromSet = true
		}
	}
	if !foundUserBase || !foundUserFromSet {
		t.Fatalf("expected base and toolset tools in userTools")
	}
}

func TestLLMAgent_RemoveToolSet_ByName(t *testing.T) {
	toolSetOne := dummyToolSet{name: testFirstToolSetName}
	toolSetTwo := dummyToolSet{name: testSecondToolSetName}

	agent := New("remove-agent",
		WithToolSets([]tool.ToolSet{toolSetOne, toolSetTwo}),
	)

	toolsBefore := agent.Tools()

	prefixOne := testFirstToolSetName + testPrefixedKnowledgeTail
	prefixTwo := testSecondToolSetName + testPrefixedKnowledgeTail

	hasOne := false
	hasTwo := false
	for _, tl := range toolsBefore {
		switch tl.Declaration().Name {
		case prefixOne:
			hasOne = true
		case prefixTwo:
			hasTwo = true
		}
	}
	if !hasOne || !hasTwo {
		t.Fatalf("expected tools %q and %q before remove",
			prefixOne, prefixTwo)
	}

	removed := agent.RemoveToolSet(testFirstToolSetName)
	if !removed {
		t.Fatalf("expected RemoveToolSet to remove %q",
			testFirstToolSetName)
	}

	toolsAfter := agent.Tools()
	hasOne = false
	hasTwo = false
	for _, tl := range toolsAfter {
		switch tl.Declaration().Name {
		case prefixOne:
			hasOne = true
		case prefixTwo:
			hasTwo = true
		}
	}
	if hasOne {
		t.Fatalf("expected tool %q to be removed", prefixOne)
	}
	if !hasTwo {
		t.Fatalf("expected tool %q to remain", prefixTwo)
	}

	removedAgain := agent.RemoveToolSet(testFirstToolSetName)
	if removedAgain {
		t.Fatalf("expected second RemoveToolSet(%q) to be false",
			testFirstToolSetName)
	}
}

func TestLLMAgent_SetToolSets_ReplacesAll(t *testing.T) {
	toolSetOne := dummyToolSet{name: testFirstToolSetName}
	toolSetTwo := dummyToolSet{name: testSecondToolSetName}

	agent := New("set-agent",
		WithToolSets([]tool.ToolSet{toolSetOne}),
	)

	toolsBefore := agent.Tools()
	prefixOne := testFirstToolSetName + testPrefixedKnowledgeTail
	prefixTwo := testSecondToolSetName + testPrefixedKnowledgeTail

	hasOne := false
	hasTwo := false
	for _, tl := range toolsBefore {
		switch tl.Declaration().Name {
		case prefixOne:
			hasOne = true
		case prefixTwo:
			hasTwo = true
		}
	}
	if !hasOne {
		t.Fatalf("expected tool %q before SetToolSets", prefixOne)
	}
	if hasTwo {
		t.Fatalf("did not expect tool %q before SetToolSets", prefixTwo)
	}

	agent.SetToolSets([]tool.ToolSet{toolSetTwo})

	toolsAfter := agent.Tools()
	hasOne = false
	hasTwo = false
	for _, tl := range toolsAfter {
		switch tl.Declaration().Name {
		case prefixOne:
			hasOne = true
		case prefixTwo:
			hasTwo = true
		}
	}
	if hasOne {
		t.Fatalf("expected tool %q to be removed after SetToolSets",
			prefixOne)
	}
	if !hasTwo {
		t.Fatalf("expected tool %q after SetToolSets", prefixTwo)
	}
}

func TestLLMAgent_FilterTools_NoFilterReturnsAll(t *testing.T) {
	ctx := context.Background()

	userTool1 := dummyTool{
		decl: &tool.Declaration{Name: testUserToolNameOne},
	}
	userTool2 := dummyTool{
		decl: &tool.Declaration{Name: testUserToolNameTwo},
	}

	agent := New(
		testFilterAgentName,
		WithTools([]tool.Tool{userTool1, userTool2}),
	)

	allTools := agent.Tools()
	if len(allTools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(allTools))
	}

	filtered := agent.FilterTools(ctx)
	if len(filtered) != len(allTools) {
		t.Fatalf("expected %d filtered tools, got %d",
			len(allTools), len(filtered))
	}

	seen := map[string]bool{}
	for _, tl := range filtered {
		seen[tl.Declaration().Name] = true
	}
	if !seen[testUserToolNameOne] || !seen[testUserToolNameTwo] {
		t.Fatalf("expected both user tools in filtered set, got %v",
			seen)
	}
}

func TestLLMAgent_FilterTools_RespectsUserFilterOnly(t *testing.T) {
	ctx := context.Background()

	userTool1 := dummyTool{
		decl: &tool.Declaration{Name: testUserToolNameOne},
	}
	userTool2 := dummyTool{
		decl: &tool.Declaration{Name: testUserToolNameTwo},
	}
	kb := &minimalKnowledge{}
	subAgent := New(testSubAgentName)

	filterFunc := func(ctx context.Context,
		tl tool.Tool) bool {
		return tl.Declaration().Name == testUserToolNameTwo
	}

	agent := New(
		testFilterAgentName,
		WithTools([]tool.Tool{userTool1, userTool2}),
		WithKnowledge(kb),
		WithSubAgents([]agent.Agent{subAgent}),
		WithToolFilter(filterFunc),
	)

	filtered := agent.FilterTools(ctx)

	seenUser := map[string]bool{}
	seenKnowledge := false
	seenTransfer := false
	for _, tl := range filtered {
		name := tl.Declaration().Name
		switch name {
		case testUserToolNameOne, testUserToolNameTwo:
			seenUser[name] = true
		case testKnowledgeToolName:
			seenKnowledge = true
		case testTransferToolName:
			seenTransfer = true
		}
	}

	if seenUser[testUserToolNameOne] {
		t.Fatalf("user tool %q should have been filtered out",
			testUserToolNameOne)
	}
	if !seenUser[testUserToolNameTwo] {
		t.Fatalf("user tool %q should have been kept",
			testUserToolNameTwo)
	}
	if !seenKnowledge {
		t.Fatalf("framework knowledge tool %q must always pass filter",
			testKnowledgeToolName)
	}
	if !seenTransfer {
		t.Fatalf("framework transfer tool %q must always pass filter",
			testTransferToolName)
	}
}

func TestLLMAgent_AddToolSet_NilDoesNothing(t *testing.T) {
	baseTool := dummyTool{
		decl: &tool.Declaration{Name: testUserToolNameOne},
	}
	llmAgent := New(
		"add-nil",
		WithTools([]tool.Tool{baseTool}),
	)

	beforeTools := llmAgent.Tools()
	if len(llmAgent.option.ToolSets) != 0 {
		t.Fatalf("expected no toolsets before AddToolSet")
	}

	llmAgent.AddToolSet(nil)

	afterTools := llmAgent.Tools()
	if len(afterTools) != len(beforeTools) {
		t.Fatalf("expected tools unchanged, before=%d after=%d",
			len(beforeTools), len(afterTools))
	}
	if len(llmAgent.option.ToolSets) != 0 {
		t.Fatalf("expected no toolsets after AddToolSet(nil)")
	}
}

func TestLLMAgent_AddToolSet_ReplacesByName(t *testing.T) {
	firstToolSet := &dummyToolSet{name: testDynamicToolSetName}
	llmAgent := New(
		"add-replace",
		WithToolSets([]tool.ToolSet{firstToolSet}),
	)

	if len(llmAgent.option.ToolSets) != 1 {
		t.Fatalf("expected 1 toolset, got %d",
			len(llmAgent.option.ToolSets))
	}
	original := llmAgent.option.ToolSets[0]

	replacement := &dummyToolSet{name: testDynamicToolSetName}
	llmAgent.AddToolSet(replacement)

	if len(llmAgent.option.ToolSets) != 1 {
		t.Fatalf("expected 1 toolset after replace, got %d",
			len(llmAgent.option.ToolSets))
	}
	if llmAgent.option.ToolSets[0] != replacement {
		t.Fatalf("expected toolset to be replaced with new value")
	}
	if llmAgent.option.ToolSets[0] == original {
		t.Fatalf("expected original toolset to be replaced")
	}
}

func TestLLMAgent_SetToolSets_ClearsWhenEmpty(t *testing.T) {
	toolSet := &dummyToolSet{name: testDynamicToolSetName}
	llmAgent := New(
		"set-clear",
		WithToolSets([]tool.ToolSet{toolSet}),
	)

	if len(llmAgent.option.ToolSets) == 0 {
		t.Fatalf("expected toolsets before clear")
	}

	beforeCount := len(llmAgent.Tools())
	llmAgent.SetToolSets(nil)

	if llmAgent.option.ToolSets != nil {
		t.Fatalf("expected option.ToolSets to be nil after clear")
	}
	afterCount := len(llmAgent.Tools())
	if afterCount >= beforeCount {
		t.Fatalf("expected fewer tools after clearing toolsets, "+
			"before=%d after=%d", beforeCount, afterCount)
	}
}
