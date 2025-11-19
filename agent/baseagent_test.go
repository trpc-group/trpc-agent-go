//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgentForBase is a simple mock implementation for testing.
type mockAgentForBase struct {
	name string
}

func (m *mockAgentForBase) Run(ctx context.Context, inv *Invocation) (<-chan *event.Event, error) {
	return nil, nil
}

func (m *mockAgentForBase) Info() Info {
	return Info{Name: m.name}
}

func (m *mockAgentForBase) SubAgents() []Agent {
	return nil
}

func (m *mockAgentForBase) FindSubAgent(name string) Agent {
	return nil
}

func (m *mockAgentForBase) Tools() []tool.Tool {
	return nil
}

func TestBaseSubAgentHolder_SubAgents(t *testing.T) {
	agent1 := &mockAgentForBase{name: "agent1"}
	agent2 := &mockAgentForBase{name: "agent2"}
	subAgents := []Agent{agent1, agent2}

	holder := NewBaseSubAgentHolder(subAgents)
	result := holder.SubAgents()

	if len(result) != 2 {
		t.Errorf("expected 2 sub-agents, got %d", len(result))
	}
	if result[0].Info().Name != agent1.name {
		t.Error("first sub-agent doesn't match")
	}
	if result[1].Info().Name != agent2.name {
		t.Error("second sub-agent doesn't match")
	}
}

func TestBaseSubAgentHolder_FindSubAgent(t *testing.T) {
	agent1 := &mockAgentForBase{name: "agent1"}
	agent2 := &mockAgentForBase{name: "agent2"}
	subAgents := []Agent{agent1, agent2}

	holder := NewBaseSubAgentHolder(subAgents)

	// Test finding existing agent
	found := holder.FindSubAgent("agent2")
	if found == nil || found.Info().Name != "agent2" {
		t.Error("failed to find agent2")
	}

	// Test finding non-existent agent
	notFound := holder.FindSubAgent("agent3")
	if notFound != nil {
		t.Error("expected nil for non-existent agent")
	}
}

func TestBaseSubAgentHolder_Tools(t *testing.T) {
	holder := NewBaseSubAgentHolder(nil)
	tools := holder.Tools()

	if tools == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}
