//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package transfer

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockSubAgent implements agent.Agent for testing.
type mockSubAgent struct {
	name        string
	description string
}

func (m *mockSubAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (m *mockSubAgent) Tools() []tool.Tool {
	return nil
}

func (m *mockSubAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockSubAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *mockSubAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func TestTransferTool_Declaration(t *testing.T) {
	agentInfos := []agent.Info{
		{Name: "calculator", Description: "A calculator agent"},
	}

	tool := New(agentInfos)
	declaration := tool.Declaration()

	if declaration.Name != "transfer_to_agent" {
		t.Errorf("Expected name 'transfer_to_agent', got '%s'", declaration.Name)
	}

	// Check that the description includes agent details.
	agentNameParam := declaration.InputSchema.Properties["agent_name"]
	if agentNameParam == nil {
		t.Error("Expected agent_name parameter in schema")
	} else if !contains(agentNameParam.Description, "calculator") {
		t.Error("Expected agent_name description to contain agent names")
	}
	if !contains(agentNameParam.Description, "A calculator agent") {
		t.Error("Expected agent_name description to contain agent descriptions")
	}
}

func TestTransferTool_Success(t *testing.T) {
	agentInfos := []agent.Info{
		{Name: "calculator", Description: "A calculator agent"},
	}

	tool := New(agentInfos)

	request := Request{AgentName: "calculator"}
	requestBytes, _ := json.Marshal(request)

	ctx := agent.NewContextWithInvocation(context.Background(), &agent.Invocation{
		InvocationID: "test-invocation-id",
	})
	result, err := tool.Call(ctx, requestBytes)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	response, ok := result.(Response)
	if !ok {
		t.Error("Expected Response type")
	}

	if !response.Success {
		t.Error("Expected successful transfer")
	}
}

func TestTransferTool_AgentNotFound(t *testing.T) {
	agentInfos := []agent.Info{
		{Name: "calculator", Description: "A calculator agent"},
	}

	tool := New(agentInfos)

	request := Request{AgentName: "nonexistent"}
	requestBytes, _ := json.Marshal(request)

	ctx := agent.NewContextWithInvocation(context.Background(), &agent.Invocation{
		InvocationID: "test-invocation-id",
	})
	result, err := tool.Call(ctx, requestBytes)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	response, ok := result.(Response)
	if !ok {
		t.Error("Expected Response type")
	}

	if response.Success {
		t.Error("Expected transfer to fail for nonexistent agent")
	}
	if response.TransferType != "error" {
		t.Errorf("Expected TransferType 'error', got '%s'", response.TransferType)
	}
}

func TestTransferTool_NoSubAgents(t *testing.T) {
	// Test with empty agent info list.
	agentInfos := []agent.Info{}

	tool := New(agentInfos)

	request := Request{AgentName: "any-agent"}
	requestBytes, _ := json.Marshal(request)

	ctx := agent.NewContextWithInvocation(context.Background(), &agent.Invocation{
		InvocationID: "test-invocation-id",
	})
	result, err := tool.Call(ctx, requestBytes)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	response, ok := result.(Response)
	if !ok {
		t.Error("Expected Response type")
	}

	if response.Success {
		t.Error("Expected transfer to fail when no sub-agents available")
	}
	if response.TransferType != "error" {
		t.Errorf("Expected TransferType 'error', got '%s'", response.TransferType)
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr))))
}

// findSubstring is a simple substring search.
func findSubstring(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
