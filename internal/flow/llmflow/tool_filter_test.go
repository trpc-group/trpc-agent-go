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
	allTools  []tool.Tool
	userTools []tool.Tool
	name      string
}

func (m *mockAgentWithUserTools) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (m *mockAgentWithUserTools) Tools() []tool.Tool {
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
	filtered := f.getFilteredTools(inv)

	// Should return all tools
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools, got %d", len(filtered))
	}
}

// TestGetFilteredTools_WithAllowedTools tests global tool filtering.
func TestGetFilteredTools_WithAllowedTools(t *testing.T) {
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
	inv.RunOptions.AllowedTools = []string{"user_tool_1"}

	filtered := f.getFilteredTools(inv)

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

// TestGetFilteredTools_WithAllowedAgentTools tests agent-specific tool filtering.
func TestGetFilteredTools_WithAllowedAgentTools(t *testing.T) {
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
	inv.RunOptions.AllowedAgentTools = map[string][]string{
		"test-agent": {"user_tool_2"},
	}

	filtered := f.getFilteredTools(inv)

	// Should include: user_tool_2 (allowed for this agent) + framework_tool (always included)
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools (user_tool_2 + framework_tool), got %d", len(filtered))
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

	if foundUserTool1 {
		t.Error("user_tool_1 should be filtered out")
	}
	if !foundUserTool2 {
		t.Error("expected user_tool_2 to be included")
	}
	if !foundFramework {
		t.Error("expected framework_tool to be included")
	}
}

// TestGetFilteredTools_Priority tests that AllowedAgentTools takes priority over AllowedTools.
func TestGetFilteredTools_Priority(t *testing.T) {
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
	// Set both global and agent-specific filters
	inv.RunOptions.AllowedTools = []string{"user_tool_1"}
	inv.RunOptions.AllowedAgentTools = map[string][]string{
		"test-agent": {"user_tool_2"}, // This should take priority
	}

	filtered := f.getFilteredTools(inv)

	// Should use agent-specific filter: user_tool_2 + framework_tool
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

	if foundUserTool1 {
		t.Error("user_tool_1 should be filtered (agent-specific filter takes priority)")
	}
	if !foundUserTool2 {
		t.Error("user_tool_2 should be included (from agent-specific filter)")
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
	inv.RunOptions.AllowedTools = []string{"tool_1"}

	filtered := f.getFilteredTools(inv)

	// Should use old behavior: filter all tools (no distinction between user and framework)
	if len(filtered) != 1 {
		t.Errorf("expected 1 tool (old behavior), got %d", len(filtered))
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
	inv.RunOptions.AllowedTools = []string{"user_tool"}

	filtered := f.getFilteredTools(inv)

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

// TestGetFilteredTools_EmptyAllowedList tests that empty allowed list returns all tools.
func TestGetFilteredTools_EmptyAllowedList(t *testing.T) {
	f := New(nil, nil, Options{})

	tool1 := &mockTool{name: "tool_1"}
	tool2 := &mockTool{name: "tool_2"}

	mockAgent := &mockAgentWithUserTools{
		name:      "test-agent",
		allTools:  []tool.Tool{tool1, tool2},
		userTools: []tool.Tool{tool1, tool2},
	}

	inv := agent.NewInvocation()
	inv.Agent = mockAgent
	inv.AgentName = "test-agent"
	inv.RunOptions.AllowedTools = []string{} // Empty list

	filtered := f.getFilteredTools(inv)

	// Empty list should be treated as "no filter"
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools (empty filter = no filter), got %d", len(filtered))
	}
}
