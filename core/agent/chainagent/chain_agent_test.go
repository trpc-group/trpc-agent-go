package chainagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// mockAgent is a test implementation of agent.Agent.
type mockAgent struct {
	name           string
	shouldError    bool
	eventCount     int
	eventContent   string
	executionOrder *[]string // Track execution order
	tools          []tool.Tool
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if m.shouldError {
		return nil, errors.New("mock agent error")
	}

	eventChan := make(chan *event.Event, 10)

	go func() {
		defer close(eventChan)

		// Record execution order if tracking is enabled.
		if m.executionOrder != nil {
			*m.executionOrder = append(*m.executionOrder, m.name)
		}

		// Generate the specified number of events.
		for i := 0; i < m.eventCount; i++ {
			evt := event.New(invocation.InvocationID, m.name)
			evt.Object = "test.completion"

			// Add some content to simulate real events.
			choice := model.Choice{
				Index: i,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: m.eventContent,
				},
			}
			evt.Choices = []model.Choice{choice}
			evt.Done = i == m.eventCount-1 // Mark last event as done.

			select {
			case eventChan <- evt:
			case <-ctx.Done():
				return
			}

			// Small delay to simulate processing time.
			time.Sleep(1 * time.Millisecond)
		}
	}()

	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

func TestChainAgent_Run_Sequential(t *testing.T) {
	// Track execution order.
	var executionOrder []string

	// Create mock sub-agents.
	subAgent1 := &mockAgent{
		name:           "agent-1",
		eventCount:     2,
		eventContent:   "Response from agent 1",
		executionOrder: &executionOrder,
	}
	subAgent2 := &mockAgent{
		name:           "agent-2",
		eventCount:     1,
		eventContent:   "Response from agent 2",
		executionOrder: &executionOrder,
	}
	subAgent3 := &mockAgent{
		name:           "agent-3",
		eventCount:     1,
		eventContent:   "Response from agent 3",
		executionOrder: &executionOrder,
	}

	// Create ChainAgent.
	chainAgent := New(Options{
		Name:              "test-chain",
		SubAgents:         []agent.Agent{subAgent1, subAgent2, subAgent3},
		ChannelBufferSize: 20,
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-chain",
		InvocationID: "test-invocation-001",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventChan, err := chainAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("ChainAgent.Run() failed: %v", err)
	}

	// Collect all events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Verify events count (2 + 1 + 1 = 4 events).
	expectedEventCount := 4
	if len(events) != expectedEventCount {
		t.Errorf("Expected %d events, got %d", expectedEventCount, len(events))
	}

	// Verify execution order (agents should run sequentially).
	expectedOrder := []string{"agent-1", "agent-2", "agent-3"}
	if len(executionOrder) != len(expectedOrder) {
		t.Errorf("Expected %d agents to execute, got %d", len(expectedOrder), len(executionOrder))
	}
	for i, expected := range expectedOrder {
		if i >= len(executionOrder) || executionOrder[i] != expected {
			t.Errorf("Expected agent %s at position %d, got %v", expected, i, executionOrder)
		}
	}

	// Verify event authors match execution order.
	agentEventCounts := map[string]int{
		"agent-1": 0,
		"agent-2": 0,
		"agent-3": 0,
	}
	for _, evt := range events {
		agentEventCounts[evt.Author]++
	}

	if agentEventCounts["agent-1"] != 2 {
		t.Errorf("Expected 2 events from agent-1, got %d", agentEventCounts["agent-1"])
	}
	if agentEventCounts["agent-2"] != 1 {
		t.Errorf("Expected 1 event from agent-2, got %d", agentEventCounts["agent-2"])
	}
	if agentEventCounts["agent-3"] != 1 {
		t.Errorf("Expected 1 event from agent-3, got %d", agentEventCounts["agent-3"])
	}
}

func TestChainAgent_Run_SubAgentError(t *testing.T) {
	// Create mock sub-agents with one that errors.
	subAgent1 := &mockAgent{
		name:         "agent-1",
		eventCount:   1,
		eventContent: "Response from agent 1",
	}
	subAgent2 := &mockAgent{
		name:        "agent-2",
		shouldError: true, // This agent will error.
	}
	subAgent3 := &mockAgent{
		name:         "agent-3",
		eventCount:   1,
		eventContent: "Response from agent 3",
	}

	// Create ChainAgent.
	chainAgent := New(Options{
		Name:      "test-chain",
		SubAgents: []agent.Agent{subAgent1, subAgent2, subAgent3},
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-chain",
		InvocationID: "test-invocation-002",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	eventChan, err := chainAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("ChainAgent.Run() failed: %v", err)
	}

	// Collect all events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have 1 event from agent-1 + 1 error event = 2 events.
	// agent-3 should not execute because agent-2 errored.
	expectedEventCount := 2
	if len(events) != expectedEventCount {
		t.Errorf("Expected %d events, got %d", expectedEventCount, len(events))
	}

	// Last event should be an error event.
	lastEvent := events[len(events)-1]
	if lastEvent.Error == nil {
		t.Error("Expected last event to be an error event")
	} else if lastEvent.Error.Type != model.ErrorTypeFlowError {
		t.Errorf("Expected error type %s, got %s", model.ErrorTypeFlowError, lastEvent.Error.Type)
	}
}

func TestChainAgent_Run_EmptySubAgents(t *testing.T) {
	// Create ChainAgent with no sub-agents.
	chainAgent := New(Options{
		Name:      "test-chain",
		SubAgents: []agent.Agent{},
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-chain",
		InvocationID: "test-invocation-003",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	eventChan, err := chainAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("ChainAgent.Run() failed: %v", err)
	}

	// Collect all events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have no events.
	if len(events) != 0 {
		t.Errorf("Expected 0 events, got %d", len(events))
	}
}

func TestChainAgent_Tools(t *testing.T) {
	// Create some mock tools.
	tools := []tool.UnaryTool{} // Empty for now since we don't have concrete tool implementations.

	chainAgent := New(Options{
		Name:  "test-chain",
		Tools: tools,
	})

	if len(chainAgent.Tools()) != len(tools) {
		t.Errorf("Expected %d tools, got %d", len(tools), len(chainAgent.Tools()))
	}
}

func TestChainAgent_ChannelBufferSize(t *testing.T) {
	// Test default buffer size.
	chainAgent1 := New(Options{
		Name: "test-chain-1",
	})
	if chainAgent1.channelBufferSize != defaultChannelBufferSize {
		t.Errorf("Expected default buffer size %d, got %d", defaultChannelBufferSize, chainAgent1.channelBufferSize)
	}

	// Test custom buffer size.
	customSize := 100
	chainAgent2 := New(Options{
		Name:              "test-chain-2",
		ChannelBufferSize: customSize,
	})
	if chainAgent2.channelBufferSize != customSize {
		t.Errorf("Expected custom buffer size %d, got %d", customSize, chainAgent2.channelBufferSize)
	}
}
