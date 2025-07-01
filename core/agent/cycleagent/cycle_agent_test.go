package cycleagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing",
	}
}

// SubAgents implements the agent.Agent interface for testing.
func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent implements the agent.Agent interface for testing.
func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
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
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should run 2 iterations * 2 agents = 4 events.
	require.Equal(t, 4, len(events))

	// Each agent should run exactly maxIter times.
	require.Equal(t, maxIter, agent1Count)
	require.Equal(t, maxIter, agent2Count)
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
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have events from both agents in first iteration (escalation stops loop).
	require.Equal(t, 2, len(events))

	// Should run only 1 iteration due to escalation.
	require.Equal(t, 1, agent1Count)
	require.Equal(t, 1, agent2Count)

	// Last event should be error event.
	lastEvent := events[len(events)-1]
	require.NotNil(t, lastEvent.Error)
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
	require.NoError(t, err)

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
	require.Equal(t, expectedEvents, len(events))

	// agent1 should run triggerAfter times.
	require.Equal(t, triggerAfter, agent1Count)
}

// conditionalMockAgent escalates when a condition is met.
type conditionalMockAgent struct {
	name           string
	eventCount     int
	executionCount *int
	triggerAfter   int
	trackCount     *int // External counter to track
}

func (m *conditionalMockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Conditional mock agent for testing",
	}
}

// SubAgents implements the agent.Agent interface for testing.
func (m *conditionalMockAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent implements the agent.Agent interface for testing.
func (m *conditionalMockAgent) FindSubAgent(name string) agent.Agent {
	return nil
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
	require.Equal(t, 2, len(events))

	// Last event should be error.
	lastEvent := events[len(events)-1]
	require.NotNil(t, lastEvent.Error)
}

func TestCycleAgent_ShouldEscalate(t *testing.T) {
	cycleAgent := New(Options{Name: "test"})

	// Test nil event.
	require.False(t, cycleAgent.shouldEscalate(nil))

	// Test normal event.
	normalEvent := event.New("test", "test")
	require.False(t, cycleAgent.shouldEscalate(normalEvent))

	// Test error event.
	errorEvent := event.NewErrorEvent("test", "test", model.ErrorTypeFlowError, "test error")
	require.True(t, cycleAgent.shouldEscalate(errorEvent))

	// Test done error event.
	doneErrorEvent := event.New("test", "test")
	doneErrorEvent.Object = model.ObjectTypeError
	doneErrorEvent.Done = true
	require.True(t, cycleAgent.shouldEscalate(doneErrorEvent))
}

func TestCycleAgent_ChannelBufferSize(t *testing.T) {
	// Test default.
	agent1 := New(Options{Name: "test1"})
	require.Equal(t, defaultChannelBufferSize, agent1.channelBufferSize)

	// Test custom.
	agent2 := New(Options{Name: "test2", ChannelBufferSize: 100})
	require.Equal(t, 100, agent2.channelBufferSize)
}

func TestCycleAgent_WithCallbacks(t *testing.T) {
	// Create agent callbacks.
	callbacks := agent.NewAgentCallbacks()

	// Test before agent callback that skips execution.
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
		if invocation.Message.Content == "skip" {
			return nil, nil
		}
		return nil, nil
	})

	// Create cycle agent with callbacks.
	maxIterations := 3
	cycleAgent := New(Options{
		Name:           "test-cycle-agent",
		SubAgents:      []agent.Agent{&mockAgent{name: "agent1"}, &mockAgent{name: "agent2"}},
		MaxIterations:  &maxIterations,
		AgentCallbacks: callbacks,
	})

	// Test skip execution.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-skip",
		AgentName:    "test-cycle-agent",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "skip",
		},
	}

	ctx := context.Background()
	eventChan, err := cycleAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Should not receive any events since execution was skipped.
	// Wait a bit to ensure no events are sent.
	time.Sleep(50 * time.Millisecond)

	// Check if channel is closed (no events sent).
	select {
	case evt, ok := <-eventChan:
		require.False(t, ok, "Expected no events, but received: %v", evt)
		// If ok is false, channel is closed which is expected.
	default:
		// Channel is still open, which means no events were sent (expected).
	}
}
