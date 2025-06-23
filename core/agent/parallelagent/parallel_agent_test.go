package parallelagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// mockAgent is a test implementation of agent.Agent.
type mockAgent struct {
	name         string
	shouldError  bool
	eventCount   int
	eventContent string
	delay        time.Duration
	tools        []tool.Tool
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if m.shouldError {
		return nil, errors.New("mock agent error")
	}

	eventChan := make(chan *event.Event, 10)

	go func() {
		defer close(eventChan)

		// Add delay to simulate processing time.
		if m.delay > 0 {
			select {
			case <-time.After(m.delay):
			case <-ctx.Done():
				return
			}
		}

		// Generate events.
		for i := 0; i < m.eventCount; i++ {
			evt := event.New(invocation.InvocationID, m.name)
			evt.Object = "test.completion"
			evt.Done = i == m.eventCount-1

			select {
			case eventChan <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

func TestParallelAgent_Run_Basic(t *testing.T) {
	// Create mock sub-agents.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 2, delay: 10 * time.Millisecond}
	subAgent2 := &mockAgent{name: "agent-2", eventCount: 1, delay: 5 * time.Millisecond}

	// Create ParallelAgent.
	parallelAgent := New(Options{
		Name:      "test-parallel",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-parallel",
		InvocationID: "test-001",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	eventChan, err := parallelAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("ParallelAgent.Run() failed: %v", err)
	}

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Verify events count (2 + 1 = 3 events).
	if len(events) != 3 {
		t.Errorf("Expected 3 events, got %d", len(events))
	}

	// Verify both agents contributed.
	agentCounts := make(map[string]int)
	for _, evt := range events {
		agentCounts[evt.Author]++
	}

	if agentCounts["agent-1"] != 2 {
		t.Errorf("Expected 2 events from agent-1, got %d", agentCounts["agent-1"])
	}
	if agentCounts["agent-2"] != 1 {
		t.Errorf("Expected 1 event from agent-2, got %d", agentCounts["agent-2"])
	}
}

func TestParallelAgent_Run_WithError(t *testing.T) {
	// Create agents, one with error.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1}
	subAgent2 := &mockAgent{name: "agent-2", shouldError: true}

	parallelAgent := New(Options{
		Name:      "test-parallel",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	invocation := &agent.Invocation{
		AgentName:    "test-parallel",
		InvocationID: "test-002",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	eventChan, err := parallelAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("ParallelAgent.Run() failed: %v", err)
	}

	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have events from successful agent and error event.
	if len(events) < 1 {
		t.Errorf("Expected at least 1 event, got %d", len(events))
	}

	// Check for error event.
	hasError := false
	for _, evt := range events {
		if evt.Error != nil {
			hasError = true
		}
	}
	if !hasError {
		t.Error("Expected error event")
	}
}

func TestParallelAgent_BranchInvocations(t *testing.T) {
	subAgent := &mockAgent{name: "agent-1", eventCount: 1}
	parallelAgent := New(Options{Name: "test-parallel"})

	baseInvocation := &agent.Invocation{
		AgentName:    "test-parallel",
		InvocationID: "base-001",
	}

	branchInvocation := parallelAgent.createBranchInvocationForSubAgent(subAgent, baseInvocation)

	// Verify branch has different ID.
	if branchInvocation.InvocationID == baseInvocation.InvocationID {
		t.Error("Branch should have different InvocationID")
	}

	// Verify branch contains base ID.
	if !strings.Contains(branchInvocation.InvocationID, baseInvocation.InvocationID) {
		t.Error("Branch ID should contain base ID")
	}

	// Verify agent is set.
	if branchInvocation.Agent != subAgent {
		t.Error("Branch should have correct agent")
	}
}

func TestParallelAgent_ChannelBufferSize(t *testing.T) {
	// Test default.
	agent1 := New(Options{Name: "test1"})
	if agent1.channelBufferSize != defaultChannelBufferSize {
		t.Errorf("Expected default size %d, got %d", defaultChannelBufferSize, agent1.channelBufferSize)
	}

	// Test custom.
	agent2 := New(Options{Name: "test2", ChannelBufferSize: 100})
	if agent2.channelBufferSize != 100 {
		t.Errorf("Expected size 100, got %d", agent2.channelBufferSize)
	}
}
