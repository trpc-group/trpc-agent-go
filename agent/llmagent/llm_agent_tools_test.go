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
	base := []tool.Tool{dummyTool{decl: &tool.Declaration{Name: "a"}}}
	sets := []tool.ToolSet{dummyToolSet{name: "dummy"}}
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
	if !userToolNames["a"] {
		t.Errorf("expected tool 'a' to be tracked as user tool")
	}

	// Knowledge search tool should NOT be tracked as user tool
	if userToolNames["knowledge_search"] {
		t.Errorf("knowledge_search should not be tracked as user tool")
	}
}

func TestLLMAgent_Tools_IncludesTransferWhenSubAgents(t *testing.T) {
	sub1 := New("sub-1")
	agt := New("main", WithSubAgents([]agent.Agent{sub1}))

	ts := agt.Tools()
	foundTransfer := false
	for _, tl := range ts {
		if tl.Declaration().Name == "transfer_to_agent" {
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
	userTool1 := dummyTool{decl: &tool.Declaration{Name: "user_tool_1"}}
	userTool2 := dummyTool{decl: &tool.Declaration{Name: "user_tool_2"}}
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
		case "user_tool_1":
			foundUserTool1 = true
		case "user_tool_2":
			foundUserTool2 = true
		}
	}

	if !foundUserTool1 || !foundUserTool2 {
		t.Errorf("user tools should include user_tool_1 and user_tool_2")
	}

	// Verify that framework tools are NOT in user tools
	for _, tool := range userTools {
		name := tool.Declaration().Name
		if name == "knowledge_search" || name == "transfer_to_agent" {
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
		case "knowledge_search":
			foundKnowledge = true
		case "transfer_to_agent":
			foundTransfer = true
		}
	}

	if !foundKnowledge || !foundTransfer {
		t.Errorf("framework tools should be in all tools even when no user tools")
	}
}
