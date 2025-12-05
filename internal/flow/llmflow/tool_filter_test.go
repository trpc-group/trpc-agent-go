//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockTool implements tool.Tool for testing.
type mockTool struct {
	name string
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: m.name}
}

func (m *mockTool) Call(context.Context, []byte) (any, error) {
	return nil, nil
}

// mockAgentWithUserTools implements agent.Agent and UserToolsProvider.
type mockAgentWithUserTools struct {
	allTools       []tool.Tool
	userTools      []tool.Tool
	name           string
	toolsCallCount int
}

func (m *mockAgentWithUserTools) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (m *mockAgentWithUserTools) Tools() []tool.Tool {
	m.toolsCallCount++
	return m.allTools
}

func (m *mockAgentWithUserTools) UserTools() []tool.Tool {
	return m.userTools
}

func (m *mockAgentWithUserTools) Info() agent.Info {
	return agent.Info{Name: m.name}
}

func (m *mockAgentWithUserTools) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgentWithUserTools) FindSubAgent(string) agent.Agent {
	return nil
}

// mockAgentWithoutUserTools implements agent.Agent without UserToolsProvider.
type mockAgentWithoutUserTools struct {
	allTools []tool.Tool
	name     string
}

func (m *mockAgentWithoutUserTools) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (m *mockAgentWithoutUserTools) Tools() []tool.Tool {
	return m.allTools
}

func (m *mockAgentWithoutUserTools) Info() agent.Info {
	return agent.Info{Name: m.name}
}

func (m *mockAgentWithoutUserTools) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgentWithoutUserTools) FindSubAgent(string) agent.Agent {
	return nil
}

// TestGetFilteredTools_CachesPerInvocation verifies that the tool list is
// computed once per invocation and then reused, even if the underlying
// agent tools or filter change. This ensures tool stability within a
// single agent.Run.
func TestGetFilteredTools_CachesPerInvocation(t *testing.T) {
	f := New(nil, nil, Options{})

	userToolV1 := &mockTool{name: "user_tool_v1"}
	userToolV2 := &mockTool{name: "user_tool_v2"}

	mockAgent := &mockAgentWithUserTools{
		name:      "test-agent",
		allTools:  []tool.Tool{userToolV1},
		userTools: []tool.Tool{userToolV1},
	}

	inv := agent.NewInvocation()
	inv.Agent = mockAgent
	inv.AgentName = "test-agent"
	inv.RunOptions.ToolFilter = tool.NewIncludeToolNamesFilter(
		"user_tool_v1",
	)

	ctx := context.Background()
	first := f.getFilteredTools(ctx, inv)

	if len(first) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(first))
	}
	if first[0].Declaration().Name != "user_tool_v1" {
		t.Fatalf(
			"expected user_tool_v1 on first call, got %s",
			first[0].Declaration().Name,
		)
	}

	// Change the agent tools and filter to simulate a dynamic ToolSet.
	mockAgent.allTools = []tool.Tool{userToolV2}
	mockAgent.userTools = []tool.Tool{userToolV2}
	inv.RunOptions.ToolFilter = tool.NewIncludeToolNamesFilter(
		"user_tool_v2",
	)

	second := f.getFilteredTools(ctx, inv)

	if len(second) != 1 {
		t.Fatalf("expected cached 1 tool, got %d", len(second))
	}
	if second[0].Declaration().Name != "user_tool_v1" {
		t.Fatalf(
			"expected cached user_tool_v1 on second call, got %s",
			second[0].Declaration().Name,
		)
	}

	if mockAgent.toolsCallCount != 1 {
		t.Fatalf(
			"expected Tools() to be called once, got %d",
			mockAgent.toolsCallCount,
		)
	}
}

// TestGetFilteredTools_NoFilter tests that all tools are returned when no filter is set.
func TestGetFilteredTools_NoFilter(t *testing.T) {
	f := New(nil, nil, Options{})

	userTool1 := &mockTool{name: "user_tool_1"}
	frameworkTool := &mockTool{name: "framework_tool"}

	mockAgent := &mockAgentWithUserTools{
		name:      "test-agent",
		allTools:  []tool.Tool{userTool1, frameworkTool},
		userTools: []tool.Tool{userTool1},
	}

	inv := agent.NewInvocation()
	inv.Agent = mockAgent
	inv.AgentName = "test-agent"

	// No filter set
	filtered := f.getFilteredTools(context.Background(), inv)

	// Should return all tools
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools, got %d", len(filtered))
	}
}

// TestGetFilteredTools_WithToolFilter tests tool filtering using FilterFunc.
func TestGetFilteredTools_WithToolFilter(t *testing.T) {
	f := New(nil, nil, Options{})

	userTool1 := &mockTool{name: "user_tool_1"}
	userTool2 := &mockTool{name: "user_tool_2"}
	frameworkTool := &mockTool{name: "framework_tool"}

	mockAgent := &mockAgentWithUserTools{
		name:      "test-agent",
		allTools:  []tool.Tool{userTool1, userTool2, frameworkTool},
		userTools: []tool.Tool{userTool1, userTool2},
	}

	inv := agent.NewInvocation()
	inv.Agent = mockAgent
	inv.AgentName = "test-agent"
	inv.RunOptions.ToolFilter = tool.NewIncludeToolNamesFilter("user_tool_1")

	filtered := f.getFilteredTools(context.Background(), inv)

	// Should include: user_tool_1 (allowed) + framework_tool (always included)
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools (user_tool_1 + framework_tool), got %d", len(filtered))
	}

	foundUserTool1 := false
	foundFramework := false
	foundUserTool2 := false

	for _, tool := range filtered {
		switch tool.Declaration().Name {
		case "user_tool_1":
			foundUserTool1 = true
		case "framework_tool":
			foundFramework = true
		case "user_tool_2":
			foundUserTool2 = true
		}
	}

	if !foundUserTool1 {
		t.Error("expected user_tool_1 to be included")
	}
	if !foundFramework {
		t.Error("expected framework_tool to be included")
	}
	if foundUserTool2 {
		t.Error("user_tool_2 should be filtered out")
	}
}

// TestGetFilteredTools_WithExcludeFilter tests tool filtering using exclude filter.
func TestGetFilteredTools_WithExcludeFilter(t *testing.T) {
	f := New(nil, nil, Options{})

	userTool1 := &mockTool{name: "user_tool_1"}
	userTool2 := &mockTool{name: "user_tool_2"}
	frameworkTool := &mockTool{name: "framework_tool"}

	mockAgent := &mockAgentWithUserTools{
		name:      "test-agent",
		allTools:  []tool.Tool{userTool1, userTool2, frameworkTool},
		userTools: []tool.Tool{userTool1, userTool2},
	}

	inv := agent.NewInvocation()
	inv.Agent = mockAgent
	inv.AgentName = "test-agent"
	inv.RunOptions.ToolFilter = tool.NewExcludeToolNamesFilter("user_tool_2")

	filtered := f.getFilteredTools(context.Background(), inv)

	// Should include: user_tool_1 (not excluded) + framework_tool (always included)
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools (user_tool_1 + framework_tool), got %d", len(filtered))
	}

	foundUserTool1 := false
	foundUserTool2 := false
	foundFramework := false

	for _, tool := range filtered {
		switch tool.Declaration().Name {
		case "user_tool_1":
			foundUserTool1 = true
		case "user_tool_2":
			foundUserTool2 = true
		case "framework_tool":
			foundFramework = true
		}
	}

	if !foundUserTool1 {
		t.Error("expected user_tool_1 to be included")
	}
	if foundUserTool2 {
		t.Error("user_tool_2 should be filtered out")
	}
	if !foundFramework {
		t.Error("expected framework_tool to be included")
	}
}

// TestGetFilteredTools_CustomFilter tests custom filter function.
func TestGetFilteredTools_CustomFilter(t *testing.T) {
	f := New(nil, nil, Options{})

	userTool1 := &mockTool{name: "user_tool_1"}
	userTool2 := &mockTool{name: "user_tool_2"}
	frameworkTool := &mockTool{name: "framework_tool"}

	mockAgent := &mockAgentWithUserTools{
		name:      "test-agent",
		allTools:  []tool.Tool{userTool1, userTool2, frameworkTool},
		userTools: []tool.Tool{userTool1, userTool2},
	}

	inv := agent.NewInvocation()
	inv.Agent = mockAgent
	inv.AgentName = "test-agent"
	// Custom filter: only allow tools with "1" in the name
	inv.RunOptions.ToolFilter = func(ctx context.Context, t tool.Tool) bool {
		decl := t.Declaration()
		if decl == nil {
			return false
		}
		return decl.Name == "user_tool_1"
	}

	filtered := f.getFilteredTools(context.Background(), inv)

	// Should include: user_tool_1 (matches custom logic) + framework_tool (always included)
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools, got %d", len(filtered))
	}

	foundUserTool1 := false
	foundUserTool2 := false

	for _, tool := range filtered {
		switch tool.Declaration().Name {
		case "user_tool_1":
			foundUserTool1 = true
		case "user_tool_2":
			foundUserTool2 = true
		}
	}

	if !foundUserTool1 {
		t.Error("user_tool_1 should be included (matches custom filter)")
	}
	if foundUserTool2 {
		t.Error("user_tool_2 should be filtered out")
	}
}

// TestGetFilteredTools_AgentWithoutUserToolsProvider tests backward compatibility.
func TestGetFilteredTools_AgentWithoutUserToolsProvider(t *testing.T) {
	f := New(nil, nil, Options{})

	tool1 := &mockTool{name: "tool_1"}
	tool2 := &mockTool{name: "tool_2"}

	// Agent without UserToolsProvider (backward compatibility)
	mockAgent := &mockAgentWithoutUserTools{
		name:     "old-agent",
		allTools: []tool.Tool{tool1, tool2},
	}

	inv := agent.NewInvocation()
	inv.Agent = mockAgent
	inv.AgentName = "old-agent"
	inv.RunOptions.ToolFilter = tool.NewIncludeToolNamesFilter("tool_1")

	filtered := f.getFilteredTools(context.Background(), inv)

	// Should filter all tools (no distinction between user and framework)
	if len(filtered) != 1 {
		t.Errorf("expected 1 tool, got %d", len(filtered))
	}

	if filtered[0].Declaration().Name != "tool_1" {
		t.Error("expected only tool_1 to pass filter")
	}
}

// TestGetFilteredTools_FrameworkToolsNeverFiltered tests that framework tools are never filtered.
func TestGetFilteredTools_FrameworkToolsNeverFiltered(t *testing.T) {
	f := New(nil, nil, Options{})

	userTool := &mockTool{name: "user_tool"}
	knowledgeTool := &mockTool{name: "knowledge_search"}
	transferTool := &mockTool{name: "transfer_to_agent"}

	mockAgent := &mockAgentWithUserTools{
		name:      "test-agent",
		allTools:  []tool.Tool{userTool, knowledgeTool, transferTool},
		userTools: []tool.Tool{userTool}, // Only user_tool is a user tool
	}

	inv := agent.NewInvocation()
	inv.Agent = mockAgent
	inv.AgentName = "test-agent"
	// Allow only user_tool, but framework tools should still be included
	inv.RunOptions.ToolFilter = tool.NewIncludeToolNamesFilter("user_tool")

	filtered := f.getFilteredTools(context.Background(), inv)

	// Should include: user_tool + knowledge_search + transfer_to_agent
	if len(filtered) != 3 {
		t.Errorf("expected 3 tools, got %d", len(filtered))
	}

	foundKnowledge := false
	foundTransfer := false

	for _, tool := range filtered {
		switch tool.Declaration().Name {
		case "knowledge_search":
			foundKnowledge = true
		case "transfer_to_agent":
			foundTransfer = true
		}
	}

	if !foundKnowledge {
		t.Error("knowledge_search (framework tool) should never be filtered")
	}
	if !foundTransfer {
		t.Error("transfer_to_agent (framework tool) should never be filtered")
	}
}
