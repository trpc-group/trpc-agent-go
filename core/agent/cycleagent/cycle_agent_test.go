package cycleagent

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
	name               string
	shouldError        bool
	eventCount         int
	eventContent       string
	shouldTriggerError bool // Generate error event to trigger escalation
	executionCount     *int // Track how many times this agent runs
	tools              []tool.Tool
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if m.shouldError {
		return nil, errors.New("mock agent error")
	}

	eventChan := make(chan *event.Event, 10)

	go func() {
		defer close(eventChan)

		// Track execution count.
		if m.executionCount != nil {
			*m.executionCount++
		}

		// Generate events.
		for i := 0; i < m.eventCount; i++ {
			var evt *event.Event

			// Generate error event if requested.
			if m.shouldTriggerError && i == m.eventCount-1 {
				evt = event.NewErrorEvent(invocation.InvocationID, m.name, model.ErrorTypeFlowError, "mock escalation")
			} else {
				evt = event.New(invocation.InvocationID, m.name)
				evt.Object = "test.completion"
				evt.Done = i == m.eventCount-1
			}

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

func TestCycleAgent_Run_WithMaxIterations(t *testing.T) {
	// Track execution counts.
	agent1Count := 0
	agent2Count := 0

	// Create mock sub-agents.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1, executionCount: &agent1Count}
	subAgent2 := &mockAgent{name: "agent-2", eventCount: 1, executionCount: &agent2Count}

	// Create CycleAgent with max iterations.
	maxIter := 2
	cycleAgent := New(Options{
		Name:          "test-cycle",
		SubAgents:     []agent.Agent{subAgent1, subAgent2},
		MaxIterations: &maxIter,
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "test-001",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	eventChan, err := cycleAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("CycleAgent.Run() failed: %v", err)
	}

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should run 2 iterations * 2 agents = 4 events.
	if len(events) != 4 {
		t.Errorf("Expected 4 events, got %d", len(events))
	}

	// Each agent should run exactly maxIter times.
	if agent1Count != maxIter {
		t.Errorf("Expected agent-1 to run %d times, got %d", maxIter, agent1Count)
	}
	if agent2Count != maxIter {
		t.Errorf("Expected agent-2 to run %d times, got %d", maxIter, agent2Count)
	}
}

func TestCycleAgent_Run_WithEscalation(t *testing.T) {
	// Track execution counts.
	agent1Count := 0
	agent2Count := 0

	// Create mock sub-agents - agent2 will trigger escalation.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1, executionCount: &agent1Count}
	subAgent2 := &mockAgent{name: "agent-2", eventCount: 1, shouldTriggerError: true, executionCount: &agent2Count}

	// Create CycleAgent with high max iterations (should stop due to escalation).
	maxIter := 10
	cycleAgent := New(Options{
		Name:          "test-cycle",
		SubAgents:     []agent.Agent{subAgent1, subAgent2},
		MaxIterations: &maxIter,
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "test-002",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	eventChan, err := cycleAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("CycleAgent.Run() failed: %v", err)
	}

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have events from both agents in first iteration (escalation stops loop).
	if len(events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(events))
	}

	// Should run only 1 iteration due to escalation.
	if agent1Count != 1 {
		t.Errorf("Expected agent-1 to run 1 time, got %d", agent1Count)
	}
	if agent2Count != 1 {
		t.Errorf("Expected agent-2 to run 1 time, got %d", agent2Count)
	}

	// Last event should be error event.
	lastEvent := events[len(events)-1]
	if lastEvent.Error == nil {
		t.Error("Expected last event to be error event")
	}
}

func TestCycleAgent_Run_NoMaxIterations(t *testing.T) {
	// Track execution counts.
	agent1Count := 0
	agent2Count := 0

	// Create mock sub-agents - agent2 will trigger escalation after 3 iterations.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1, executionCount: &agent1Count}

	// Custom mock that escalates after 3 executions.
	subAgent2 := &conditionalMockAgent{
		name:           "agent-2",
		eventCount:     1,
		executionCount: &agent2Count,
		triggerAfter:   3,
		trackCount:     &agent1Count, // Track agent1 count to decide when to escalate.
	}

	// Create CycleAgent without max iterations.
	cycleAgent := New(Options{
		Name:      "test-cycle",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "test-003",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	eventChan, err := cycleAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("CycleAgent.Run() failed: %v", err)
	}

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should run until escalation (3 complete iterations).
	// Each iteration has 2 events (agent1 + agent2).
	// On 3rd iteration, agent2 will escalate instead of normal event.
	triggerAfter := 3
	expectedEvents := triggerAfter * 2 // 3 iterations * 2 agents = 6 events total.
	if len(events) != expectedEvents {
		t.Errorf("Expected %d events, got %d", expectedEvents, len(events))
	}

	// agent1 should run triggerAfter times.
	if agent1Count != triggerAfter {
		t.Errorf("Expected agent-1 to run %d times, got %d", triggerAfter, agent1Count)
	}
}

// conditionalMockAgent escalates when a condition is met.
type conditionalMockAgent struct {
	name           string
	eventCount     int
	executionCount *int
	triggerAfter   int
	trackCount     *int // External counter to track
}

func (m *conditionalMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 10)

	go func() {
		defer close(eventChan)

		// Track execution count.
		if m.executionCount != nil {
			*m.executionCount++
		}

		// Check if we should escalate.
		shouldEscalate := m.trackCount != nil && *m.trackCount >= m.triggerAfter

		for i := 0; i < m.eventCount; i++ {
			var evt *event.Event

			// Generate error event if we should escalate.
			if shouldEscalate && i == m.eventCount-1 {
				evt = event.NewErrorEvent(invocation.InvocationID, m.name, model.ErrorTypeFlowError, "escalation")
			} else {
				evt = event.New(invocation.InvocationID, m.name)
				evt.Object = "test.completion"
				evt.Done = i == m.eventCount-1
			}

			select {
			case eventChan <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChan, nil
}

func (m *conditionalMockAgent) Tools() []tool.Tool {
	return nil
}

func TestCycleAgent_Run_SubAgentError(t *testing.T) {
	// Create agent that returns error.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1}
	subAgent2 := &mockAgent{name: "agent-2", shouldError: true}

	cycleAgent := New(Options{
		Name:      "test-cycle",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "test-004",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	eventChan, err := cycleAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("CycleAgent.Run() failed: %v", err)
	}

	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have event from agent1 + error event.
	if len(events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(events))
	}

	// Last event should be error.
	lastEvent := events[len(events)-1]
	if lastEvent.Error == nil {
		t.Error("Expected error event")
	}
}

func TestCycleAgent_ShouldEscalate(t *testing.T) {
	cycleAgent := New(Options{Name: "test"})

	// Test nil event.
	if cycleAgent.shouldEscalate(nil) {
		t.Error("Nil event should not escalate")
	}

	// Test normal event.
	normalEvent := event.New("test", "test")
	if cycleAgent.shouldEscalate(normalEvent) {
		t.Error("Normal event should not escalate")
	}

	// Test error event.
	errorEvent := event.NewErrorEvent("test", "test", model.ErrorTypeFlowError, "test error")
	if !cycleAgent.shouldEscalate(errorEvent) {
		t.Error("Error event should escalate")
	}

	// Test done error event.
	doneErrorEvent := event.New("test", "test")
	doneErrorEvent.Object = model.ObjectTypeError
	doneErrorEvent.Done = true
	if !cycleAgent.shouldEscalate(doneErrorEvent) {
		t.Error("Done error event should escalate")
	}
}

func TestCycleAgent_ChannelBufferSize(t *testing.T) {
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
