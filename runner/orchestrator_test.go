package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

// createMockRunner creates a mock runner with the given name and optional error.
func createMockRunner(name string, runErr error) *mockRunnerForOrchestrator {
	return &mockRunnerForOrchestrator{
		name:    name,
		runErr:  runErr,
		outputs: make([]string, 0),
	}
}

// mockRunnerForOrchestrator implements the Runner interface for testing.
type mockRunnerForOrchestrator struct {
	name     string
	active   bool
	runErr   error
	runCount int
	outputs  []string
}

func (m *mockRunnerForOrchestrator) Name() string {
	return m.name
}

func (m *mockRunnerForOrchestrator) Start(ctx context.Context) error {
	m.active = true
	return nil
}

func (m *mockRunnerForOrchestrator) Stop(ctx context.Context) error {
	m.active = false
	return nil
}

func (m *mockRunnerForOrchestrator) Run(ctx context.Context, input message.Message) (*message.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		m.runCount++

		if m.runErr != nil {
			return nil, m.runErr
		}

		output := "Output from " + m.name + ": " + input.Content
		m.outputs = append(m.outputs, output)
		return message.NewAssistantMessage(output), nil
	}
}

func (m *mockRunnerForOrchestrator) RunAsync(ctx context.Context, input message.Message) (<-chan *event.Event, error) {
	if m.runErr != nil {
		return nil, m.runErr
	}

	eventCh := make(chan *event.Event, 1)

	go func() {
		defer close(eventCh)

		select {
		case <-ctx.Done():
			eventCh <- event.NewErrorEvent(ctx.Err(), 500)
			return
		default:
			m.runCount++
			output := "Async output from " + m.name + ": " + input.Content
			m.outputs = append(m.outputs, output)
			resp := message.NewAssistantMessage(output)
			eventCh <- event.NewMessageEvent(resp)
		}
	}()

	return eventCh, nil
}

func TestOrchestratorCreation(t *testing.T) {
	// Test with all parameters provided
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config := DefaultOrchestratorConfig()

	orchestrator := NewOrchestrator("test-orchestrator", config, logger)

	if orchestrator.Name() != "test-orchestrator" {
		t.Errorf("Expected orchestrator name 'test-orchestrator', got %s", orchestrator.Name())
	}

	// Test default name
	orchestrator2 := NewOrchestrator("", config, logger)
	if orchestrator2.Name() != "orchestrator" {
		t.Errorf("Expected default name 'orchestrator', got %s", orchestrator2.Name())
	}

	// Test config defaults
	orchestrator3 := NewOrchestrator("test3", OrchestratorConfig{}, logger)
	if orchestrator3.config.MaxConcurrent != 5 {
		t.Errorf("Expected default MaxConcurrent to be 5, got %d", orchestrator3.config.MaxConcurrent)
	}

	if orchestrator3.config.BufferSize != 100 {
		t.Errorf("Expected default BufferSize to be 100, got %d", orchestrator3.config.BufferSize)
	}
}

func TestOrchestratorRunnerManagement(t *testing.T) {
	orchestrator := NewOrchestrator("runner-mgmt", DefaultOrchestratorConfig(), nil)

	// Add runners
	runner1 := createMockRunner("runner1", nil)
	runner2 := createMockRunner("runner2", nil)

	if err := orchestrator.RegisterRunner(runner1); err != nil {
		t.Fatalf("Failed to register runner1: %v", err)
	}

	if err := orchestrator.RegisterRunner(runner2); err != nil {
		t.Fatalf("Failed to register runner2: %v", err)
	}

	// Check runner count
	runners := orchestrator.ListRunners()
	if len(runners) != 2 {
		t.Errorf("Expected 2 runners, got %d", len(runners))
	}

	// Set a custom order
	newOrder := []string{"runner2", "runner1"}
	if err := orchestrator.SetRunnerOrder(newOrder); err != nil {
		t.Fatalf("Failed to set new runner order: %v", err)
	}

	// Get a runner
	retrievedRunner, err := orchestrator.GetRunner("runner1")
	if err != nil {
		t.Fatalf("Failed to get runner1: %v", err)
	}

	if retrievedRunner.Name() != "runner1" {
		t.Errorf("Expected retrieved runner name to be 'runner1', got %s", retrievedRunner.Name())
	}
}

func TestOrchestratorSequentialExecution(t *testing.T) {
	orchestrator := NewOrchestrator("sequential", OrchestratorConfig{
		Mode: SequentialMode,
	}, nil)

	// Add runners
	runner1 := createMockRunner("runner1", nil)
	runner2 := createMockRunner("runner2", nil)

	orchestrator.RegisterRunner(runner1)
	orchestrator.RegisterRunner(runner2)

	// Start orchestrator
	ctx := context.Background()
	if err := orchestrator.Start(ctx); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Run the orchestrator
	input := message.NewUserMessage("Test sequential input")
	result, err := orchestrator.Run(ctx, *input)

	// Check result
	if err != nil {
		t.Fatalf("Failed to run sequential orchestration: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// The result should be from the last runner
	if !strings.Contains(result.Content, "runner2") {
		t.Errorf("Expected result to be from runner2, got: %s", result.Content)
	}
}

func TestOrchestratorWithError(t *testing.T) {
	orchestrator := NewOrchestrator("error-test", OrchestratorConfig{
		Mode: SequentialMode,
	}, nil)

	// Add runners with the middle one failing
	runner1 := createMockRunner("runner1", nil)
	expectedErr := errors.New("runner2 error")
	runner2 := createMockRunner("runner2", expectedErr)

	orchestrator.RegisterRunner(runner1)
	orchestrator.RegisterRunner(runner2)

	// Start orchestrator
	ctx := context.Background()
	if err := orchestrator.Start(ctx); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Run the orchestrator
	input := message.NewUserMessage("Test error input")
	_, err := orchestrator.Run(ctx, *input)

	// Check error
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if !strings.Contains(err.Error(), "runner2 error") {
		t.Errorf("Expected error to contain 'runner2 error', got: %v", err)
	}
}

// TestOrchestratorStop tests the Stop method
func TestOrchestratorStop(t *testing.T) {
	// Create an orchestrator
	name := "stop-test"
	orchestrator := NewOrchestrator(name, DefaultOrchestratorConfig(), nil)

	// Create mocked runners
	runner1 := createMockRunner("runner1", nil)
	runner2 := createMockRunner("runner2", nil)

	// Register runners
	err := orchestrator.RegisterRunner(runner1)
	if err != nil {
		t.Fatalf("Failed to register runner1: %v", err)
	}
	err = orchestrator.RegisterRunner(runner2)
	if err != nil {
		t.Fatalf("Failed to register runner2: %v", err)
	}

	// Start the orchestrator
	ctx := context.Background()
	err = orchestrator.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Store the active status for verification
	runner1Active := runner1.active
	runner2Active := runner2.active
	if !runner1Active || !runner2Active {
		t.Error("Expected runners to be active after orchestrator start")
	}

	// Stop the orchestrator
	err = orchestrator.Stop(ctx)
	if err != nil {
		t.Fatalf("Failed to stop orchestrator: %v", err)
	}

	// Verify all runners were stopped
	if runner1.active {
		t.Error("Expected runner1 to be inactive after Stop")
	}
	if runner2.active {
		t.Error("Expected runner2 to be inactive after Stop")
	}
}

// TestOrchestratorIsActive tests the IsActive method
func TestOrchestratorIsActive(t *testing.T) {
	// Create an orchestrator
	name := "isactive-test"
	orchestrator := NewOrchestrator(name, DefaultOrchestratorConfig(), nil)

	// Initially, it should be inactive
	if orchestrator.IsActive() {
		t.Error("Expected orchestrator to be inactive initially")
	}

	// Start the orchestrator
	ctx := context.Background()
	err := orchestrator.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Now it should be active
	if !orchestrator.IsActive() {
		t.Error("Expected orchestrator to be active after Start")
	}

	// Stop the orchestrator
	err = orchestrator.Stop(ctx)
	if err != nil {
		t.Fatalf("Failed to stop orchestrator: %v", err)
	}

	// Now it should be inactive again
	if orchestrator.IsActive() {
		t.Error("Expected orchestrator to be inactive after Stop")
	}
}

// TestOrchestratorRunAsync tests the RunAsync method
func TestOrchestratorRunAsync(t *testing.T) {
	// Create an orchestrator in sequential mode
	name := "async-test"
	config := OrchestratorConfig{
		Mode: SequentialMode,
	}
	orchestrator := NewOrchestrator(name, config, nil)

	// Create runners that will return outputs for the async test
	runner1 := createMockRunner("async-runner1", nil)
	runner2 := createMockRunner("async-runner2", nil)

	// Register runners
	_ = orchestrator.RegisterRunner(runner1)
	_ = orchestrator.RegisterRunner(runner2)
	_ = orchestrator.SetRunnerOrder([]string{"async-runner1", "async-runner2"})

	// Start orchestrator
	ctx := context.Background()
	_ = orchestrator.Start(ctx)

	// Run async
	input := message.NewUserMessage("Test async input")
	eventCh, err := orchestrator.RunAsync(ctx, *input)
	if err != nil {
		t.Fatalf("Failed to run async: %v", err)
	}

	// Collect events
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Check events: should have at least one event from each runner plus a completion event
	if len(events) < 2 {
		t.Errorf("Expected at least 2 events, got %d", len(events))
	}

	// Verify runners were called
	if runner1.runCount != 1 {
		t.Errorf("Expected runner1 to be called once, got %d", runner1.runCount)
	}
	if runner2.runCount != 1 {
		t.Errorf("Expected runner2 to be called once, got %d", runner2.runCount)
	}

	// Check for completion event
	var foundCompletion bool
	for _, evt := range events {
		if evt.Type == event.TypeCustom && evt.Name == "orchestrator.completed" {
			foundCompletion = true
			// Check orchestrator completion event
			if meta, ok := evt.Data.(map[string]interface{}); ok {
				if meta["orchestrator"] != "async-test" {
					t.Errorf("Expected orchestrator name 'async-test', got '%v'", meta["orchestrator"])
				}
				if _, ok := meta["duration_ms"]; !ok {
					t.Error("Expected duration_ms in completion event")
				}
			} else {
				t.Error("Expected completion event data to be a map")
			}
		}
	}

	if !foundCompletion {
		t.Error("Expected to find completion event")
	}
}

// TestRunParallel tests the runParallel method through RunAsync with parallel mode
func TestRunParallel(t *testing.T) {
	// Create an orchestrator in parallel mode
	name := "parallel-test"
	config := OrchestratorConfig{
		Mode: ParallelMode,
	}
	orchestrator := NewOrchestrator(name, config, nil)

	// Create runners
	runner1 := createMockRunner("parallel-runner1", nil)
	runner2 := createMockRunner("parallel-runner2", nil)

	// Register runners
	_ = orchestrator.RegisterRunner(runner1)
	_ = orchestrator.RegisterRunner(runner2)

	// Start orchestrator
	ctx := context.Background()
	_ = orchestrator.Start(ctx)

	// Run async
	input := message.NewUserMessage("Test parallel input")
	eventCh, err := orchestrator.RunAsync(ctx, *input)
	if err != nil {
		t.Fatalf("Failed to run async: %v", err)
	}

	// Collect events
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify both runners were called
	if runner1.runCount != 1 {
		t.Errorf("Expected runner1 to be called once, got %d", runner1.runCount)
	}
	if runner2.runCount != 1 {
		t.Errorf("Expected runner2 to be called once, got %d", runner2.runCount)
	}

	// Check for completion event
	var foundCompletion bool
	for _, evt := range events {
		if evt.Type == event.TypeCustom && evt.Name == "orchestrator.completed" {
			foundCompletion = true
		}
	}

	if !foundCompletion {
		t.Error("Expected to find completion event")
	}
}

// TestRunDynamic tests the runDynamic method through RunAsync with dynamic mode
func TestRunDynamic(t *testing.T) {
	// Create an orchestrator in dynamic mode
	name := "dynamic-test"
	config := OrchestratorConfig{
		Mode: DynamicMode,
	}
	orchestrator := NewOrchestrator(name, config, nil)

	// Create runners for the dynamic test
	runner1 := createMockRunner("dynamic-runner1", nil)
	runner2 := createMockRunner("dynamic-runner2", nil)

	// Register runners
	_ = orchestrator.RegisterRunner(runner1)
	_ = orchestrator.RegisterRunner(runner2)
	_ = orchestrator.SetRunnerOrder([]string{"dynamic-runner1"}) // Initial runner

	// Start orchestrator
	ctx := context.Background()
	_ = orchestrator.Start(ctx)

	// Since we can't easily test the dynamic.control event processing in this test setup,
	// we'll verify that at least the first runner gets called
	input := message.NewUserMessage("Test dynamic input")
	eventCh, err := orchestrator.RunAsync(ctx, *input)
	if err != nil {
		t.Fatalf("Failed to run async: %v", err)
	}

	// Collect events
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Check that at least the first runner was called
	if runner1.runCount != 1 {
		t.Errorf("Expected runner1 to be called once, got %d", runner1.runCount)
	}

	// Check for completion event
	var foundCompletion bool
	for _, evt := range events {
		if evt.Type == event.TypeCustom && evt.Name == "orchestrator.completed" {
			foundCompletion = true
		}
	}

	if !foundCompletion {
		t.Error("Expected to find completion event")
	}
}
