package runner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

// ExecutionMode defines how an orchestrator executes its runners.
type ExecutionMode int

const (
	// SequentialMode executes runners in sequence.
	SequentialMode ExecutionMode = iota

	// ParallelMode executes runners in parallel.
	ParallelMode

	// DynamicMode lets the orchestrator decide the execution mode based on dependencies.
	DynamicMode
)

// OrchestratorConfig defines configuration for an orchestrator.
type OrchestratorConfig struct {
	// Mode is the execution mode for the orchestrator.
	Mode ExecutionMode `json:"mode"`

	// Timeout is the maximum duration for the entire orchestration.
	Timeout time.Duration `json:"timeout"`

	// MaxConcurrent is the maximum number of concurrent runner executions.
	MaxConcurrent int `json:"max_concurrent"`

	// BufferSize is the size of event channels.
	BufferSize int `json:"buffer_size"`

	// Custom contains additional custom configuration.
	Custom map[string]interface{} `json:"custom,omitempty"`
}

// DefaultOrchestratorConfig returns a default orchestrator configuration.
func DefaultOrchestratorConfig() OrchestratorConfig {
	return OrchestratorConfig{
		Mode:          SequentialMode,
		Timeout:       5 * time.Minute,
		MaxConcurrent: 5,
		BufferSize:    100,
		Custom:        make(map[string]interface{}),
	}
}

// Orchestrator manages the execution of multiple runners.
type Orchestrator struct {
	name     string
	logger   *slog.Logger
	config   OrchestratorConfig
	registry *Registry
	runners  []string // ordered list of runner names to execute
	mu       sync.RWMutex
	active   bool
}

// NewOrchestrator creates a new orchestrator.
func NewOrchestrator(name string, config OrchestratorConfig, logger *slog.Logger) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}

	if name == "" {
		name = "orchestrator"
	}

	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 5
	}

	if config.BufferSize <= 0 {
		config.BufferSize = 100
	}

	return &Orchestrator{
		name:     name,
		logger:   logger,
		config:   config,
		registry: NewRegistry(),
		runners:  make([]string, 0),
		active:   false,
	}
}

// Name returns the name of the orchestrator.
func (o *Orchestrator) Name() string {
	return o.name
}

// RegisterRunner registers a runner with the orchestrator.
func (o *Orchestrator) RegisterRunner(runner Runner) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if err := o.registry.RegisterRunner(runner); err != nil {
		return err
	}

	// Add the runner to the ordered list
	o.runners = append(o.runners, runner.Name())
	return nil
}

// SetRunnerOrder sets the execution order of runners.
func (o *Orchestrator) SetRunnerOrder(runnerNames []string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Validate all runners exist
	for _, name := range runnerNames {
		if _, err := o.registry.GetRunner(name); err != nil {
			return fmt.Errorf("cannot set order: %w", err)
		}
	}

	o.runners = make([]string, len(runnerNames))
	copy(o.runners, runnerNames)
	return nil
}

// Start initializes the orchestrator and starts all registered runners.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.active {
		return nil // Already started
	}

	o.logger.Info("Starting orchestrator", "name", o.name)

	// Start all runners
	var startupErrs []error
	for _, name := range o.runners {
		runner, err := o.registry.GetRunner(name)
		if err != nil {
			startupErrs = append(startupErrs, fmt.Errorf("failed to get runner %q: %w", name, err))
			continue
		}

		if err := runner.Start(ctx); err != nil {
			startupErrs = append(startupErrs, fmt.Errorf("failed to start runner %q: %w", name, err))
			continue
		}
	}

	if len(startupErrs) > 0 {
		// At least one runner failed to start, attempt to stop any that did start
		for _, name := range o.runners {
			runner, err := o.registry.GetRunner(name)
			if err != nil {
				continue
			}
			_ = runner.Stop(ctx) // Best effort cleanup
		}

		return fmt.Errorf("failed to start one or more runners: %v", startupErrs)
	}

	o.active = true
	return nil
}

// Stop gracefully shuts down the orchestrator and all registered runners.
func (o *Orchestrator) Stop(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.active {
		return nil // Already stopped
	}

	o.logger.Info("Stopping orchestrator", "name", o.name)

	// Stop all runners (in reverse order)
	var shutdownErrs []error
	for i := len(o.runners) - 1; i >= 0; i-- {
		name := o.runners[i]
		runner, err := o.registry.GetRunner(name)
		if err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("failed to get runner %q: %w", name, err))
			continue
		}

		if err := runner.Stop(ctx); err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("failed to stop runner %q: %w", name, err))
			continue
		}
	}

	o.active = false

	if len(shutdownErrs) > 0 {
		return fmt.Errorf("failed to stop one or more runners: %v", shutdownErrs)
	}

	return nil
}

// Run executes a workflow with the given input.
func (o *Orchestrator) Run(ctx context.Context, input message.Message) (*message.Message, error) {
	o.mu.RLock()
	active := o.active
	mode := o.config.Mode
	o.mu.RUnlock()

	if !active {
		return nil, fmt.Errorf("orchestrator %s is not active", o.name)
	}

	// Create a timeout context if specified in config
	if o.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.config.Timeout)
		defer cancel()
	}

	startTime := time.Now()
	o.logger.Info("Orchestrating workflow",
		"name", o.name,
		"mode", mode,
		"runners", len(o.runners),
	)

	var result *message.Message
	var err error

	// Create a copy of the input to use in runners
	inputCopy := input

	switch mode {
	case SequentialMode:
		result, err = o.runSequential(ctx, inputCopy)
	case ParallelMode:
		result, err = o.runParallel(ctx, inputCopy)
	case DynamicMode:
		result, err = o.runDynamic(ctx, inputCopy)
	default:
		err = fmt.Errorf("unsupported execution mode: %v", mode)
	}

	duration := time.Since(startTime)
	if err != nil {
		o.logger.Error("Orchestration failed",
			"name", o.name,
			"error", err.Error(),
			"duration_ms", duration.Milliseconds(),
		)
		return nil, err
	}

	o.logger.Info("Orchestration completed successfully",
		"name", o.name,
		"duration_ms", duration.Milliseconds(),
	)

	return result, nil
}

// RunAsync executes a workflow with the given input and returns a channel of events.
func (o *Orchestrator) RunAsync(ctx context.Context, input message.Message) (<-chan *event.Event, error) {
	o.mu.RLock()
	active := o.active
	mode := o.config.Mode
	bufferSize := o.config.BufferSize
	o.mu.RUnlock()

	if !active {
		return nil, fmt.Errorf("orchestrator %s is not active", o.name)
	}

	// Create a timeout context if specified in config
	var cancel context.CancelFunc
	if o.config.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, o.config.Timeout)
		// Note: Not deferring cancel as this is an async operation
	}

	startTime := time.Now()
	o.logger.Info("Orchestrating workflow asynchronously",
		"name", o.name,
		"mode", mode,
		"runners", len(o.runners),
	)

	// Create a copy of the input to use in runners
	inputCopy := input

	// Create the event channel
	eventCh := make(chan *event.Event, bufferSize)

	// Start the workflow in a goroutine
	go func() {
		defer func() {
			if cancel != nil {
				cancel()
			}

			// Send a final event with timing information
			duration := time.Since(startTime)
			eventCh <- event.NewCustomEvent("orchestrator.completed", map[string]interface{}{
				"orchestrator": o.name,
				"duration_ms":  duration.Milliseconds(),
			})

			close(eventCh)
		}()

		var result *message.Message
		var err error

		switch mode {
		case SequentialMode:
			result, err = o.runSequential(ctx, inputCopy)
		case ParallelMode:
			result, err = o.runParallel(ctx, inputCopy)
		case DynamicMode:
			result, err = o.runDynamic(ctx, inputCopy)
		default:
			err = fmt.Errorf("unsupported execution mode: %v", mode)
		}

		if err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		if result != nil {
			eventCh <- event.NewMessageEvent(result)
		}
	}()

	return eventCh, nil
}

// runSequential executes runners in sequence.
func (o *Orchestrator) runSequential(ctx context.Context, input message.Message) (*message.Message, error) {
	o.mu.RLock()
	runners := make([]string, len(o.runners))
	copy(runners, o.runners)
	o.mu.RUnlock()

	var currentInput message.Message = input
	var result *message.Message

	for _, name := range runners {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrContextDone, ctx.Err())
		default:
			// Continue with execution
		}

		// Get the runner
		runner, err := o.registry.GetRunner(name)
		if err != nil {
			return nil, fmt.Errorf("failed to get runner %q: %w", name, err)
		}

		// Run the runner
		o.logger.Debug("Executing runner", "orchestrator", o.name, "runner", name)
		result, err = runner.Run(ctx, currentInput)
		if err != nil {
			return nil, fmt.Errorf("runner %q execution failed: %w", name, err)
		}

		// Use the result as input for the next runner
		if result != nil {
			currentInput = *result
		}
	}

	return result, nil
}

// runParallel executes runners in parallel and returns the final result.
func (o *Orchestrator) runParallel(ctx context.Context, input message.Message) (*message.Message, error) {
	o.mu.RLock()
	maxConcurrent := o.config.MaxConcurrent
	runners := make([]string, len(o.runners))
	copy(runners, o.runners)
	o.mu.RUnlock()

	if len(runners) == 0 {
		return nil, fmt.Errorf("no runners configured")
	}

	// Create a semaphore to limit concurrency
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	results := make([]*message.Message, len(runners))
	errs := make([]error, len(runners))

	// Execute all runners in parallel
	for i, name := range runners {
		wg.Add(1)
		go func(idx int, runnerName string) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			// Get the runner
			runner, err := o.registry.GetRunner(runnerName)
			if err != nil {
				errs[idx] = fmt.Errorf("failed to get runner %q: %w", runnerName, err)
				return
			}

			// Run the runner
			o.logger.Debug("Executing runner in parallel",
				"orchestrator", o.name,
				"runner", runnerName,
				"index", idx)

			result, err := runner.Run(ctx, input)
			if err != nil {
				errs[idx] = fmt.Errorf("runner %q execution failed: %w", runnerName, err)
				return
			}

			results[idx] = result
		}(i, name)
	}

	// Wait for all executions to complete
	wg.Wait()

	// Check for errors
	var firstErr error
	for _, err := range errs {
		if err != nil {
			firstErr = err
			break
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}

	// Return the result from the last runner for consistency
	return results[len(results)-1], nil
}

// runDynamic determines the best execution strategy based on dependencies.
// Currently a placeholder for more sophisticated orchestration.
func (o *Orchestrator) runDynamic(ctx context.Context, input message.Message) (*message.Message, error) {
	// Dynamic mode is a placeholder for more sophisticated orchestration logic
	// For now, we default to sequential mode
	o.logger.Info("Using sequential mode for dynamic orchestration", "name", o.name)
	return o.runSequential(ctx, input)
}

// GetRunner gets a runner by name.
func (o *Orchestrator) GetRunner(name string) (Runner, error) {
	return o.registry.GetRunner(name)
}

// IsActive returns whether the orchestrator is active.
func (o *Orchestrator) IsActive() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.active
}

// ListRunners lists all registered runners in their execution order.
func (o *Orchestrator) ListRunners() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := make([]string, len(o.runners))
	copy(result, o.runners)
	return result
}
