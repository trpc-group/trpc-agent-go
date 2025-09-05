//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

const (
	// AuthorGraphExecutor is the author of the graph executor.
	AuthorGraphExecutor = "graph-executor"
)

var (
	defaultChannelBufferSize     = 256
	defaultMaxSteps              = 100
	defaultStepTimeout           = time.Duration(0) // No timeout by default, users can set if needed
	defaultCheckpointSaveTimeout = 10 * time.Second // Default timeout for checkpoint save operations
)

// Executor executes a graph with the given initial state using Pregel-style BSP execution.
type Executor struct {
	graph                 *Graph
	channelBufferSize     int
	maxSteps              int
	stepTimeout           time.Duration
	nodeTimeout           time.Duration
	checkpointSaveTimeout time.Duration
	checkpointSaver       CheckpointSaver
	checkpointManager     *CheckpointManager
	lastCheckpoint        *Checkpoint
	pendingWrites         []PendingWrite
	nextNodesToExecute    []string // Nodes to execute when resuming from checkpoint
}

// ExecutorOption is a function that configures an Executor.
type ExecutorOption func(*ExecutorOptions)

// ExecutorOptions contains configuration options for creating an Executor.
type ExecutorOptions struct {
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// MaxSteps is the maximum number of steps for graph execution.
	MaxSteps int
	// StepTimeout is the timeout for each step (default: 5m).
	StepTimeout time.Duration
	// NodeTimeout is the timeout for individual node execution (default: StepTimeout/2, min 1s).
	NodeTimeout time.Duration
	// CheckpointSaveTimeout is the timeout for saving checkpoints (default: 10s).
	CheckpointSaveTimeout time.Duration
	// CheckpointSaver is the checkpoint saver for persisting graph state.
	CheckpointSaver CheckpointSaver
}

// WithChannelBufferSize sets the buffer size for event channels.
func WithChannelBufferSize(size int) ExecutorOption {
	return func(opts *ExecutorOptions) {
		opts.ChannelBufferSize = size
	}
}

// WithMaxSteps sets the maximum number of steps for graph execution.
func WithMaxSteps(maxSteps int) ExecutorOption {
	return func(opts *ExecutorOptions) {
		opts.MaxSteps = maxSteps
	}
}

// WithStepTimeout sets the timeout for each step.
func WithStepTimeout(timeout time.Duration) ExecutorOption {
	return func(opts *ExecutorOptions) {
		opts.StepTimeout = timeout
	}
}

// WithNodeTimeout sets the timeout for individual node execution.
func WithNodeTimeout(timeout time.Duration) ExecutorOption {
	return func(opts *ExecutorOptions) {
		opts.NodeTimeout = timeout
	}
}

// WithCheckpointSaver sets the checkpoint saver for the executor.
func WithCheckpointSaver(saver CheckpointSaver) ExecutorOption {
	return func(opts *ExecutorOptions) {
		opts.CheckpointSaver = saver
	}
}

// WithCheckpointSaveTimeout sets the timeout for checkpoint save operations.
func WithCheckpointSaveTimeout(timeout time.Duration) ExecutorOption {
	return func(opts *ExecutorOptions) {
		opts.CheckpointSaveTimeout = timeout
	}
}

// NewExecutor creates a new graph executor.
func NewExecutor(graph *Graph, opts ...ExecutorOption) (*Executor, error) {
	if err := graph.validate(); err != nil {
		return nil, fmt.Errorf("invalid graph: %w", err)
	}
	var options ExecutorOptions
	options.ChannelBufferSize = defaultChannelBufferSize         // Default buffer size.
	options.MaxSteps = defaultMaxSteps                           // Default max steps.
	options.StepTimeout = defaultStepTimeout                     // Default step timeout.
	options.CheckpointSaveTimeout = defaultCheckpointSaveTimeout // Default checkpoint save timeout.
	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}
	// Calculate node timeout: use provided value or derive from step timeout if step timeout is set
	nodeTimeout := options.NodeTimeout
	if nodeTimeout == 0 && options.StepTimeout > 0 {
		// Only derive from step timeout if step timeout is explicitly set
		nodeTimeout = options.StepTimeout / 2
		if nodeTimeout < time.Second {
			nodeTimeout = time.Second
		}
	}

	executor := &Executor{
		graph:                 graph,
		channelBufferSize:     options.ChannelBufferSize,
		maxSteps:              options.MaxSteps,
		stepTimeout:           options.StepTimeout,
		nodeTimeout:           nodeTimeout,
		checkpointSaveTimeout: options.CheckpointSaveTimeout,
		checkpointSaver:       options.CheckpointSaver,
	}
	// Create checkpoint manager if saver is provided.
	if options.CheckpointSaver != nil {
		executor.checkpointManager = NewCheckpointManager(options.CheckpointSaver)
	}
	return executor, nil
}

// Task represents a task to be executed in a step.
type Task struct {
	NodeID   string              // NodeID is the ID of the node to execute.
	Input    any                 // Input is the input of the task.
	Writes   []channelWriteEntry // Writes is the writes of the task.
	Triggers []string            // Triggers is the triggers of the task.
	TaskID   string              // TaskID is the ID of the task.
	TaskPath []string            // TaskPath is the path of the task.
	Overlay  State               // Overlay is the overlay state of the task.
}

// Step represents a single step in execution.
type Step struct {
	StepNumber      int             // StepNumber is the number of the step.
	Tasks           []*Task         // Tasks is the tasks of the step.
	State           State           // State is the state of the step.
	UpdatedChannels map[string]bool // UpdatedChannels is the updated channels of the step.
}

// deepCopyAny performs a deep copy of common JSON-serializable Go types to
// avoid sharing mutable references (maps/slices) across goroutines.
func deepCopyAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(v))
		for k, vv := range v {
			copied[k] = deepCopyAny(vv)
		}
		return copied
	case []any:
		copied := make([]any, len(v))
		for i := range v {
			copied[i] = deepCopyAny(v[i])
		}
		return copied
	case []string:
		copied := make([]string, len(v))
		copy(copied, v)
		return copied
	case []int:
		copied := make([]int, len(v))
		copy(copied, v)
		return copied
	case []float64:
		copied := make([]float64, len(v))
		copy(copied, v)
		return copied
	case time.Time:
		return v
	default:
		// For other scalar or struct types, rely on value semantics
		// or JSON marshaler to handle safely.
		return v
	}
}

// deepCopyState clones the State, recursively copying nested maps/slices.
func deepCopyState(s State) State {
	out := make(State, len(s))
	for k, v := range s {
		out[k] = deepCopyAny(v)
	}
	return out
}

// Execute executes the graph with the given initial state using Pregel-style BSP execution.
func (e *Executor) Execute(
	ctx context.Context,
	initialState State,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is nil")
	}
	ctx, span := trace.Tracer.Start(ctx, "execute_graph")
	defer span.End()
	startTime := time.Now()
	// Create event channel.
	eventChan := make(chan *event.Event, e.channelBufferSize)
	// Start execution in a goroutine.
	go func() {
		defer close(eventChan)
		if err := e.executeGraph(ctx, initialState, invocation, eventChan, startTime); err != nil {
			// Check if this is an interrupt error
			if IsInterruptError(err) {
				// For interrupt errors, we don't emit an error event
				// The interrupt will be handled by the caller
				return
			}
			// Emit error event for other errors.
			errorEvent := NewPregelErrorEvent(
				WithPregelEventInvocationID(invocation.InvocationID),
				WithPregelEventStepNumber(-1),
				WithPregelEventError(err.Error()),
			)
			select {
			case eventChan <- errorEvent:
			default:
			}
		}
	}()
	return eventChan, nil
}

// executeGraph executes the graph using Pregel-style BSP execution.
func (e *Executor) executeGraph(
	ctx context.Context,
	initialState State,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	startTime time.Time,
) error {
	var execState State
	var checkpointConfig map[string]any
	var resumed bool
	var resumedStep int
	// Check for checkpoint saver and try to resume from checkpoint first.
	if e.checkpointSaver != nil {
		// Checkpoint saver is available, initializing checkpoint config
		// Extract lineage ID from state first, then invocation, or generate one.
		var lineageID string
		if id, ok := initialState["lineage_id"].(string); ok && id != "" {
			lineageID = id
			// Found lineage_id in state
		} else if invocation.InvocationID != "" {
			lineageID = invocation.InvocationID
			// Using invocation ID as lineage_id
		} else {
			lineageID = fmt.Sprintf("lineage_%d", time.Now().UnixNano())
			log.Debugf("Generated new lineage_id: %s", lineageID)
		}
		// Also check for namespace and checkpoint_id in state.
		var namespace, checkpointID string
		if ns, ok := initialState["checkpoint_ns"].(string); ok {
			namespace = ns
			// Found checkpoint_ns in state
		}
		if id, ok := initialState["checkpoint_id"].(string); ok {
			checkpointID = id
			log.Debugf("Resuming from checkpoint_id: %s", checkpointID)
		}

		checkpointConfig = CreateCheckpointConfig(lineageID, checkpointID, namespace)
		log.Debugf("Checkpoint config: lineage=%s, checkpoint_id=%s, namespace=%s", lineageID, checkpointID, namespace)

		// Try to resume from existing checkpoint if available.
		if tuple, err := e.checkpointSaver.GetTuple(
			ctx, checkpointConfig,
		); err == nil && tuple != nil && tuple.Checkpoint != nil {
			log.Debugf("Resuming from checkpoint ID=%s", tuple.Checkpoint.ID)
			// Restore state from checkpoint.
			restored := make(State)
			for k, v := range tuple.Checkpoint.ChannelValues {
				// Restoring key from checkpoint
				restored[k] = v
			}
			// State restored from checkpoint

			// Convert restored values to proper types based on schema.
			if e.graph.Schema() != nil {
				// Converting restored values to proper types based on schema
				for key, value := range restored {
					if field, exists := e.graph.Schema().Fields[key]; exists {
						converted := e.restoreCheckpointValueWithSchema(value, field)
						if reflect.TypeOf(converted) != reflect.TypeOf(value) {
							// Type conversion applied
							restored[key] = converted
						}
					}
				}
			}
			// Add any missing schema defaults for fields not in checkpoint.
			if e.graph.Schema() != nil {
				// Applying schema defaults for missing fields
				for key, field := range e.graph.Schema().Fields {
					if _, exists := restored[key]; !exists {
						// Use default function if available, otherwise provide zero value.
						if field.Default != nil {
							defaultVal := field.Default()
							// Adding schema default
							restored[key] = defaultVal
						} else {
							zeroVal := reflect.Zero(field.Type).Interface()
							// Adding zero value
							restored[key] = zeroVal
						}
					}
				}
			}
			// Merge any pre-populated values from initialState that might have been
			// added by the caller (e.g., checkpoint example pre-populating counter values)
			for key, value := range initialState {
				// Only add values from initialState if they're not internal framework keys
				// and not already in the restored state from checkpoint
				if _, exists := restored[key]; !exists && !strings.HasPrefix(key, "_") {
					// Merging initial state into restored state
					restored[key] = value
				}
			}
			// Final restored state ready
			execState = restored
			resumed = true
			// Record the step from the checkpoint metadata.
			if tuple.Metadata != nil {
				resumedStep = tuple.Metadata.Step
				log.Debugf("Resuming from step %d", resumedStep)
			}
			// Record last checkpoint for version-based planning.
			e.lastCheckpoint = tuple.Checkpoint
			// Initialize channels with restored state.
			e.initializeChannels(restored, true)
			// Use storage-provided config if present (e.g., resolved checkpoint_id).
			if tuple.Config != nil {
				checkpointConfig = tuple.Config
				// Using tuple.Config with checkpoint
			}
			// Store pending writes for later application.
			e.pendingWrites = tuple.PendingWrites

			// Log checkpoint details for debugging
			log.Debugf("Loaded checkpoint - PendingWrites=%d, NextNodes=%v, NextChannels=%v",
				len(e.pendingWrites), tuple.Checkpoint.NextNodes, tuple.Checkpoint.NextChannels)

			// Handle NextNodes for checkpoints that need to trigger execution
			// This is important for initial checkpoints (step -1) that have the entry point set
			if len(tuple.Checkpoint.NextNodes) > 0 {
				// Check if NextNodes contains actual node IDs (not __end__)
				hasExecutableNodes := false
				for _, nodeID := range tuple.Checkpoint.NextNodes {
					if nodeID != End && nodeID != "" {
						hasExecutableNodes = true
						break
					}
				}

				if hasExecutableNodes && len(e.pendingWrites) == 0 {
					log.Debugf("Checkpoint has executable NextNodes: %v", tuple.Checkpoint.NextNodes)
					restored[StateKeyNextNodes] = tuple.Checkpoint.NextNodes
					execState = restored
				}
			}
		} else {
			log.Debug("No checkpoint found, starting fresh")
			// No checkpoint, initialize state with defaults.
			execState = e.initializeState(initialState)
			// Initialize channels with initial state.
			e.initializeChannels(execState, true)
		}
	} else {
		// No checkpoint saver, initialize normally.
		execState = e.initializeState(initialState)
		e.initializeChannels(execState, true)
	}

	// Check if we're resuming from an interrupt.
	if cmd, ok := initialState[StateKeyCommand].(*Command); ok {
		// Apply resume values if present.
		if cmd.Resume != nil {
			execState[ResumeChannel] = cmd.Resume
		}
		if cmd.ResumeMap != nil {
			execState[StateKeyResumeMap] = cmd.ResumeMap
		}
		delete(execState, StateKeyCommand)
	}

	// Create execution context.
	// Creating ExecutionContext
	execStateKeys := make([]string, 0, len(execState))
	for k := range execState {
		execStateKeys = append(execStateKeys, k)
	}
	// ExecutionContext state initialized

	// State initialized

	execCtx := &ExecutionContext{
		Graph:        e.graph,
		State:        execState,
		EventChan:    eventChan,
		InvocationID: invocation.InvocationID,
		resumed:      resumed,
	}

	// ExecutionContext created

	// Apply pending writes if we resumed from checkpoint.
	if resumed && e.pendingWrites != nil {
		log.Debugf("🔧 Executor: applying %d pending writes", len(e.pendingWrites))
		// Applying pending writes
		e.applyPendingWrites(execCtx, e.pendingWrites)
	}

	// Create initial checkpoint if we didn't resume and have a checkpoint saver.
	if e.checkpointSaver != nil && !resumed {
		if err := e.createCheckpointAndSave(
			ctx, &checkpointConfig, execCtx.State, CheckpointSourceInput, -1, execCtx,
		); err != nil {
			log.Debugf("Failed to create initial checkpoint: %v", err)
		}
	}

	// BSP execution loop.
	// Start from the appropriate step when resuming.
	startStep := 0
	if resumed && resumedStep >= 0 {
		startStep = resumedStep + 1
	}
	for step := startStep; step < e.maxSteps; step++ {
		// Create step context with timeout
		var stepCtx context.Context
		var stepCancel context.CancelFunc
		if e.stepTimeout > 0 {
			stepCtx, stepCancel = context.WithTimeout(ctx, e.stepTimeout)
		} else {
			stepCtx, stepCancel = context.WithCancel(ctx)
		}

		// Plan phase: determine which nodes to execute.
		var tasks []*Task
		var err error
		if step == 0 && execCtx.resumed && resumedStep >= 0 {
			// If resumed from a non-initial checkpoint, plan purely based on channel
			// triggers to continue from the restored frontier rather than the entry point.
			// Using channel triggers for resumed execution
			tasks = e.planBasedOnChannelTriggers(execCtx, step)
		} else {
			// For initial execution or when resuming from an initial checkpoint (step -1),
			// use normal planning which starts from the entry point.
			// Using normal planning
			tasks, err = e.planStep(execCtx, step)
		}
		if err != nil {
			stepCancel()
			return fmt.Errorf("planning failed at step %d: %w", step, err)
		}

		if len(tasks) == 0 {
			stepCancel()
			break
		}
		// Execute phase: run all tasks concurrently.
		if err := e.executeStep(stepCtx, execCtx, tasks, step); err != nil {
			// Check if this is an interrupt that should be handled.
			if interrupt, ok := GetInterruptError(err); ok {
				stepCancel()
				return e.handleInterrupt(stepCtx, execCtx, interrupt, step, checkpointConfig)
			}
			stepCancel()
			return fmt.Errorf("execution failed at step %d: %w", step, err)
		}
		// Update phase: process channel updates.
		if err := e.updateChannels(stepCtx, execCtx, step); err != nil {
			stepCancel()
			return fmt.Errorf("update failed at step %d: %w", step, err)
		}

		// Create checkpoint after each step if checkpoint saver is available.
		// Use parent context (ctx) for checkpoint operations, not step context
		if e.checkpointSaver != nil && checkpointConfig != nil {
			log.Debugf("Creating checkpoint at step %d", step)
			if err := e.createCheckpointAndSave(ctx, &checkpointConfig, execCtx.State, CheckpointSourceLoop, step, execCtx); err != nil {
				log.Debugf("Failed to create checkpoint at step %d: %v", step, err)
			}
		}

		stepCancel()
	}
	// Emit completion event.
	// Create a copy of the final state to avoid concurrent access issues
	finalStateCopy := make(State)
	execCtx.stateMutex.RLock()
	maps.Copy(finalStateCopy, execCtx.State)
	execCtx.stateMutex.RUnlock()

	completionEvent := NewGraphCompletionEvent(
		WithCompletionEventInvocationID(execCtx.InvocationID),
		WithCompletionEventFinalState(finalStateCopy),
		WithCompletionEventTotalSteps(e.maxSteps),
		WithCompletionEventTotalDuration(time.Since(startTime)),
	)

	// Add final state to StateDelta for test access.
	if completionEvent.StateDelta == nil {
		completionEvent.StateDelta = make(map[string][]byte)
	}
	// Snapshot the state under read lock and deep-copy nested maps/slices to
	// avoid concurrent iteration/write during JSON marshaling.
	execCtx.stateMutex.RLock()
	stateSnapshot := deepCopyState(execCtx.State)
	execCtx.stateMutex.RUnlock()
	for key, value := range stateSnapshot {
		if jsonData, err := json.Marshal(value); err == nil {
			completionEvent.StateDelta[key] = jsonData
		}
	}
	// Always deliver completion event to consumers.
	select {
	case eventChan <- completionEvent:
	default:
	}
	return nil
}

// createCheckpoint creates a checkpoint for the current state.
func (e *Executor) createCheckpoint(ctx context.Context, config map[string]any, state State, source string, step int) error {
	if e.checkpointSaver == nil {
		return nil
	}

	// Convert state to channel values.
	channelValues := make(map[string]any)
	for k, v := range state {
		channelValues[k] = v
	}

	// Create channel versions (simple incrementing integers for now).
	channelVersions := make(map[string]any)
	for k := range state {
		channelVersions[k] = 1 // This should be managed by the graph execution.
	}

	// Create versions seen (simplified for now).
	versionsSeen := make(map[string]map[string]any)

	// Create checkpoint.
	checkpoint := NewCheckpoint(channelValues, channelVersions, versionsSeen)

	// Create metadata.
	metadata := NewCheckpointMetadata(source, step)

	// Store checkpoint.
	req := PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: channelVersions,
	}
	_, err := e.checkpointSaver.Put(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to store checkpoint: %w", err)
	}

	return nil
}

// createCheckpointAndSave creates a checkpoint and persists any pending writes
// associated with the current step atomically, updating the provided config with the
// returned value from saver.PutFull (which may include the new checkpoint_id).
func (e *Executor) createCheckpointAndSave(
	ctx context.Context,
	config *map[string]any,
	state State,
	source string,
	step int,
	execCtx *ExecutionContext,
) error {
	if e.checkpointSaver == nil {
		// Checkpoint saver is nil
		return nil
	}

	// Creating checkpoint from state

	// IMPORTANT: Use the current state from execCtx which has all node updates,
	// not the state parameter which may be stale
	stateCopy := make(State)
	execCtx.stateMutex.RLock()
	for k, v := range execCtx.State {
		stateCopy[k] = v
	}
	execCtx.stateMutex.RUnlock()

	// Create checkpoint object
	checkpoint := e.createCheckpointFromState(stateCopy, step)
	if checkpoint == nil {
		log.Debug("Failed to create checkpoint object")
		return fmt.Errorf("failed to create checkpoint")
	}

	// Set parent checkpoint ID from config if available
	if parentCheckpointID := GetCheckpointID(*config); parentCheckpointID != "" {
		checkpoint.ParentCheckpointID = parentCheckpointID
		// Set parent checkpoint ID
	}

	// Created checkpoint object

	// Create metadata
	metadata := &CheckpointMetadata{
		Source: source,
		Step:   step,
		Extra:  make(map[string]any),
	}

	// Get pending writes atomically
	execCtx.pendingMu.Lock()
	pendingWrites := make([]PendingWrite, len(execCtx.pendingWrites))
	copy(pendingWrites, execCtx.pendingWrites)
	execCtx.pendingWrites = nil // Clear after copying
	execCtx.pendingMu.Unlock()

	// Track new versions for channels that were updated
	newVersions := make(map[string]any)
	channels := e.graph.getAllChannels()
	for channelName, channel := range channels {
		if channel.IsAvailable() {
			newVersions[channelName] = channel.Version
		}
	}

	// Set channel versions in checkpoint for version semantics
	checkpoint.ChannelVersions = newVersions

	// Set next nodes and channels for recovery
	if source == CheckpointSourceInput && step == -1 {
		// For initial checkpoints, set the entry point as the next node
		// This ensures that if someone forks and resumes from this checkpoint,
		// the workflow will start from the beginning
		if entryPoint := e.graph.EntryPoint(); entryPoint != "" {
			checkpoint.NextNodes = []string{entryPoint}
			log.Debugf("Initial checkpoint - setting NextNodes to entry point: %v", checkpoint.NextNodes)
		}
		checkpoint.NextChannels = e.getNextChannels(execCtx.State)
	} else {
		checkpoint.NextNodes = e.getNextNodes(execCtx.State)
		checkpoint.NextChannels = e.getNextChannels(execCtx.State)
	}

	// Use PutFull for atomic storage
	log.Debugf("Saving checkpoint ID=%s, Source=%s, Step=%d, NextNodes=%v, PendingWrites=%d",
		checkpoint.ID, source, step, checkpoint.NextNodes, len(pendingWrites))
	updatedConfig, err := e.checkpointSaver.PutFull(ctx, PutFullRequest{
		Config:        *config,
		Checkpoint:    checkpoint,
		Metadata:      metadata,
		NewVersions:   newVersions,
		PendingWrites: pendingWrites,
	})
	if err != nil {
		log.Errorf("Failed to save checkpoint %s: %v", checkpoint.ID, err)
		return fmt.Errorf("failed to save checkpoint atomically: %w", err)
	}
	// Successfully saved checkpoint
	// Clear step marks after checkpoint creation
	e.clearChannelStepMarks()

	// Update external config with the new checkpoint_id
	*config = updatedConfig
	// Updated config with new checkpoint ID
	return nil
}

// applyPendingWrites replays pending writes into channels to rebuild frontier.
func (e *Executor) applyPendingWrites(execCtx *ExecutionContext, writes []PendingWrite) {
	if len(writes) == 0 {
		return
	}
	// Sort writes by sequence number for deterministic replay
	sortedWrites := make([]PendingWrite, len(writes))
	copy(sortedWrites, writes)
	sort.Slice(sortedWrites, func(i, j int) bool {
		return sortedWrites[i].Sequence < sortedWrites[j].Sequence
	})
	for _, w := range sortedWrites {
		ch, _ := e.graph.getChannel(w.Channel)
		if ch != nil {
			ch.Update([]any{w.Value}, -1)
			// Emit channel update event to mirror live execution behavior.
			e.emitChannelUpdateEvent(execCtx, w.Channel, ch.Behavior, e.getTriggeredNodes(w.Channel))
		}
	}
}

// getConfigKeys helper to extract keys from config map for logging
func getConfigKeys(config map[string]any) []string {
	var keys []string
	for k := range config {
		keys = append(keys, k)
	}
	return keys
}

// resumeFromCheckpoint resumes execution from a specific checkpoint.
func (e *Executor) resumeFromCheckpoint(ctx context.Context, config map[string]any) (State, error) {
	log.Debugf("resumeFromCheckpoint: called with config keys: %v", getConfigKeys(config))

	if e.checkpointSaver == nil {
		// No checkpoint saver, returning nil
		return nil, nil
	}

	tuple, err := e.checkpointSaver.GetTuple(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve checkpoint: %w", err)
	}

	if tuple == nil {
		return nil, nil
	}

	// Record last checkpoint for version-based planning.
	e.lastCheckpoint = tuple.Checkpoint

	// Convert channel values back to state.
	state := make(State)
	for k, v := range tuple.Checkpoint.ChannelValues {
		state[k] = v
	}

	// Initialize channels with the restored state
	e.initializeChannels(state, false)

	// Apply pending writes if available, otherwise use NextChannels as fallback
	log.Debugf("resumeFromCheckpoint: PendingWrites=%d, NextNodes=%v, NextChannels=%v",
		len(tuple.PendingWrites), tuple.Checkpoint.NextNodes, tuple.Checkpoint.NextChannels)

	if len(tuple.PendingWrites) > 0 {
		// Create a temporary execution context for replay
		tempExecCtx := &ExecutionContext{
			State:        state,
			EventChan:    make(chan *event.Event, 100),
			InvocationID: "resume-replay",
		}
		e.applyPendingWrites(tempExecCtx, tuple.PendingWrites)
		log.Debugf("Applied %d pending writes", len(tuple.PendingWrites))
	} else if len(tuple.Checkpoint.NextNodes) > 0 {
		// Fallback: use NextNodes to trigger execution when no pending writes or channels
		// This is particularly important for initial checkpoints that have the entry point set
		log.Debugf("Using NextNodes to trigger execution: %v", tuple.Checkpoint.NextNodes)
		// Store the nodes in the state so they can be picked up during planning
		state[StateKeyNextNodes] = tuple.Checkpoint.NextNodes
		// Added NextNodes to state
	} else if len(tuple.Checkpoint.NextChannels) > 0 {
		// Fallback: use NextChannels to trigger frontier when no pending writes
		for _, chName := range tuple.Checkpoint.NextChannels {
			if ch, ok := e.graph.getChannel(chName); ok && ch != nil {
				// Use a marker value to trigger the channel
				ch.Update([]any{"resume-trigger"}, -1)
			}
		}
	}

	return state, nil
}

// initializeState initializes the execution state with schema defaults.
func (e *Executor) initializeState(initialState State) State {
	execState := make(State)
	// Copy initial state.
	for key, value := range initialState {
		execState[key] = value
	}
	// Add schema defaults for missing fields.
	if e.graph.Schema() != nil {
		for key, field := range e.graph.Schema().Fields {
			if _, exists := execState[key]; !exists {
				// Use default function if available, otherwise provide zero value.
				if field.Default != nil {
					execState[key] = field.Default()
				} else {
					execState[key] = reflect.Zero(field.Type).Interface()
				}
			}
		}
	}
	return execState
}

// initializeChannels initializes channels with input state.
// If updateChannels is false, only registers channels without triggering updates.
func (e *Executor) initializeChannels(state State, updateChannels bool) {
	// Create input channels for each state key.
	for key := range state {
		channelName := fmt.Sprintf("%s%s", ChannelInputPrefix, key)
		e.graph.addChannel(channelName, channel.BehaviorLastValue)
		if updateChannels {
			channel, _ := e.graph.getChannel(channelName)
			if channel != nil {
				channel.Update([]any{state[key]}, -1)
			}
		}
	}
}

// planStep determines which nodes to execute in the current step.
func (e *Executor) planStep(execCtx *ExecutionContext, step int) ([]*Task, error) {
	var tasks []*Task

	// Emit planning step event.
	planEvent := NewPregelStepEvent(
		WithPregelEventInvocationID(execCtx.InvocationID),
		WithPregelEventStepNumber(step),
		WithPregelEventPhase(PregelPhasePlanning),
		WithPregelEventTaskCount(0),
	)
	select {
	case execCtx.EventChan <- planEvent:
	default:
	}

	// Check if we have nodes to execute from a resumed checkpoint stored in state
	// This needs to be checked regardless of step number when resuming
	execCtx.stateMutex.RLock()
	nextNodesValue, hasNextNodes := execCtx.State[StateKeyNextNodes]
	execCtx.stateMutex.RUnlock()

	if hasNextNodes {
		log.Debugf("planStep: step=%d, found %s in state", step, StateKeyNextNodes)

		if nextNodes, ok := nextNodesValue.([]string); ok && len(nextNodes) > 0 {
			log.Debugf("Using %s from state: %v", StateKeyNextNodes, nextNodes)
			// Create tasks for the nodes stored in the state
			for _, nodeID := range nextNodes {
				execCtx.stateMutex.RLock()
				stateCopy := make(State, len(execCtx.State))
				for key, value := range execCtx.State {
					stateCopy[key] = value
				}
				execCtx.stateMutex.RUnlock()

				task := e.createTask(nodeID, stateCopy, step)
				if task != nil {
					tasks = append(tasks, task)
				}
			}
			// Remove the special key from state after using it
			execCtx.stateMutex.Lock()
			delete(execCtx.State, StateKeyNextNodes)
			execCtx.stateMutex.Unlock()
			return tasks, nil
		}
	}

	// If there are pending tasks produced by prior fan-out, schedule them first.
	execCtx.tasksMutex.Lock()
	if len(execCtx.pendingTasks) > 0 {
		tasks = append(tasks, execCtx.pendingTasks...)
		execCtx.pendingTasks = nil
	}
	execCtx.tasksMutex.Unlock()
	if len(tasks) > 0 {
		return tasks, nil
	}

	// Check if this is the first step (entry point).
	if step == 0 {
		// Use the normal entry point
		entryPoint := e.graph.EntryPoint()
		if entryPoint == "" {
			return nil, errors.New("no entry point defined")
		}
		// Planning step 0, entry point

		// Acquire read lock to safely access state for task creation.
		execCtx.stateMutex.RLock()
		stateCopy := make(State, len(execCtx.State))
		for key, value := range execCtx.State {
			stateCopy[key] = value
		}
		execCtx.stateMutex.RUnlock()

		task := e.createTask(entryPoint, stateCopy, step)
		if task != nil {
			tasks = append(tasks, task)
		} else if entryPoint != End {
			log.Warnf("❌ Step %d: Failed to create task for entry point %s", step, entryPoint)
		}
	} else {
		// Plan based on channel triggers.
		tasks = e.planBasedOnChannelTriggers(execCtx, step)
	}
	return tasks, nil
}

// planBasedOnChannelTriggers creates tasks for nodes triggered by channel updates.
func (e *Executor) planBasedOnChannelTriggers(execCtx *ExecutionContext, step int) []*Task {
	var tasks []*Task
	triggerToNodes := e.graph.getTriggerToNodes()

	// If this is a resumed execution, use version-based triggering
	if execCtx.resumed && e.lastCheckpoint != nil {
		tasks = e.planBasedOnVersionTriggers(execCtx, step)
	} else {
		// Use traditional availability-based triggering
		tasks = e.planBasedOnAvailabilityTriggers(execCtx, step, triggerToNodes)
	}

	return tasks
}

// planBasedOnVersionTriggers creates tasks based on version differences.
func (e *Executor) planBasedOnVersionTriggers(execCtx *ExecutionContext, step int) []*Task {
	var tasks []*Task

	if e.lastCheckpoint == nil {
		return tasks
	}

	// Get channels that have new versions since last checkpoint
	channels := e.graph.getAllChannels()
	for channelName, channel := range channels {
		if !channel.IsAvailable() {
			continue
		}

		// Check if channel version has increased since last checkpoint
		lastVersion, exists := e.lastCheckpoint.ChannelVersions[channelName]
		if !exists {
			// New channel, trigger all connected nodes
			tasks = append(tasks, e.createTasksForChannel(channelName, execCtx.State, step)...)
			channel.Acknowledge()
			continue
		}

		// Compare versions (handle both int and json.Number)
		var lastVersionInt int64
		switch v := lastVersion.(type) {
		case int:
			lastVersionInt = int64(v)
		case int64:
			lastVersionInt = v
		case float64:
			lastVersionInt = int64(v)
		case json.Number:
			if i, err := v.Int64(); err == nil {
				lastVersionInt = i
			} else {
				continue // Skip if version comparison fails
			}
		default:
			continue // Skip unknown version types
		}

		// If channel version has increased, trigger connected nodes
		if int64(channel.Version) > lastVersionInt {
			tasks = append(tasks, e.createTasksForChannel(channelName, execCtx.State, step)...)
			channel.Acknowledge()
		}
	}

	return tasks
}

// planBasedOnAvailabilityTriggers creates tasks based on channel availability.
func (e *Executor) planBasedOnAvailabilityTriggers(
	execCtx *ExecutionContext,
	step int,
	triggerToNodes map[string][]string,
) []*Task {
	var tasks []*Task

	for channelName, nodeIDs := range triggerToNodes {
		channel, _ := e.graph.getChannel(channelName)
		if channel == nil {
			continue
		}

		if !channel.IsAvailable() {
			continue
		}

		for _, nodeID := range nodeIDs {
			task := e.createTask(nodeID, execCtx.State, step)
			if task != nil {
				tasks = append(tasks, task)
			} else if nodeID != End {
				// Don't log error for virtual end node - it's expected.
				log.Warnf("    ❌ Failed to create task for %s", nodeID)
			}
		}

		// Mark channel as consumed for this step.
		channel.Acknowledge()
	}

	return tasks
}

// createTasksForChannel creates tasks for all nodes connected to a channel.
func (e *Executor) createTasksForChannel(channelName string, state State, step int) []*Task {
	var tasks []*Task
	triggerToNodes := e.graph.getTriggerToNodes()

	if nodeIDs, exists := triggerToNodes[channelName]; exists {
		for _, nodeID := range nodeIDs {
			task := e.createTask(nodeID, state, step)
			if task != nil {
				tasks = append(tasks, task)
			} else if nodeID != End {
				log.Warnf("    ❌ Failed to create task for %s", nodeID)
			}
		}
	}

	return tasks
}

// createTask creates a task for a node.
func (e *Executor) createTask(nodeID string, state State, step int) *Task {
	// Handle virtual end node - it doesn't need to be executed.
	if nodeID == End {
		return nil
	}

	node, exists := e.graph.Node(nodeID)
	if !exists {
		return nil
	}

	log.Debugf("🔧 createTask: creating task for nodeID='%s', step=%d", nodeID, step)
	stateKeys := make([]string, 0, len(state))
	for k := range state {
		stateKeys = append(stateKeys, k)
	}
	log.Debugf("🔧 createTask: state has %d keys: %v", len(state), stateKeys)

	// Log key state values that we're interested in tracking
	// State prepared for task

	if stepCountVal, exists := state["step_count"]; exists {
		log.Debugf("🔧 createTask: state contains step_count=%v (type: %T)", stepCountVal, stepCountVal)
	}

	// Special logging for final node to track the counter issue
	if nodeID == "final" {
		log.Debugf("🔧 createTask: FINAL NODE - counter=%v, step_count=%v", state["counter"], state["step_count"])
	}

	return &Task{
		NodeID:   nodeID,
		Input:    state,
		Writes:   node.writers,
		Triggers: node.triggers,
		TaskID:   fmt.Sprintf("%s-%d", nodeID, step),
		TaskPath: []string{nodeID},
	}
}

// createTaskWithOverlay creates a task for a node with an overlay state applied at execution time.
func (e *Executor) createTaskWithOverlay(nodeID string, overlay State, step int) *Task {
	if nodeID == End {
		return nil
	}
	node, exists := e.graph.Node(nodeID)
	if !exists {
		return nil
	}
	return &Task{
		NodeID:   nodeID,
		Input:    nil,
		Writes:   node.writers,
		Triggers: node.triggers,
		TaskID:   fmt.Sprintf("%s-%d", nodeID, step),
		TaskPath: []string{nodeID},
		Overlay:  overlay,
	}
}

// executeStep executes all tasks concurrently.
func (e *Executor) executeStep(
	ctx context.Context,
	execCtx *ExecutionContext,
	tasks []*Task,
	step int,
) error {
	// Emit execution step event.
	e.emitExecutionStepEvent(execCtx, tasks, step)
	// Execute tasks concurrently.
	var wg sync.WaitGroup
	results := make(chan error, len(tasks))

	for _, t := range tasks {
		wg.Add(1)
		go func(t *Task) {
			defer wg.Done()
			results <- e.executeSingleTask(ctx, execCtx, t, step)
		}(t)
	}

	// Wait for all tasks to complete.
	wg.Wait()
	close(results)

	// Check for errors.
	for err := range results {
		if err != nil {
			return err
		}
	}

	return nil
}

// emitExecutionStepEvent emits the execution step event.
func (e *Executor) emitExecutionStepEvent(execCtx *ExecutionContext, tasks []*Task, step int) {
	activeNodes := make([]string, len(tasks))
	for i, task := range tasks {
		activeNodes[i] = task.NodeID
	}

	execEvent := NewPregelStepEvent(
		WithPregelEventInvocationID(execCtx.InvocationID),
		WithPregelEventStepNumber(step),
		WithPregelEventPhase(PregelPhaseExecution),
		WithPregelEventTaskCount(len(tasks)),
		WithPregelEventActiveNodes(activeNodes),
	)
	select {
	case execCtx.EventChan <- execEvent:
	default:
	}
}

// executeSingleTask executes a single task and handles all its events.
func (e *Executor) executeSingleTask(
	ctx context.Context,
	execCtx *ExecutionContext,
	t *Task,
	step int,
) error {
	var nodeCtx context.Context
	var nodeCancel context.CancelFunc
	if e.nodeTimeout > 0 {
		nodeCtx, nodeCancel = context.WithTimeout(ctx, e.nodeTimeout)
	} else {
		nodeCtx, nodeCancel = context.WithCancel(ctx)
	}
	defer nodeCancel()
	// Get node type and emit start event.
	nodeType := e.getNodeType(t.NodeID)
	nodeStart := time.Now()
	e.emitNodeStartEvent(execCtx, t.NodeID, nodeType, step, nodeStart)

	// Create callback context.
	callbackCtx := &NodeCallbackContext{
		NodeID:             t.NodeID,
		NodeName:           e.getNodeName(t.NodeID),
		NodeType:           nodeType,
		StepNumber:         step,
		ExecutionStartTime: nodeStart,
		InvocationID:       execCtx.InvocationID,
		SessionID:          e.getSessionID(execCtx),
	}

	// Get state copy for callbacks using same logic as node execution, so
	// callbacks observe the exact per-task input (including fan-out input).
	execCtx.stateMutex.RLock()
	var stateCopy State
	if t.Input != nil {
		if inputState, ok := t.Input.(State); ok {
			stateCopy = make(State, len(inputState))
			maps.Copy(stateCopy, inputState)
		}
	}
	if stateCopy == nil {
		stateCopy = make(State, len(execCtx.State))
		maps.Copy(stateCopy, execCtx.State)
		if t.Overlay != nil && e.graph.Schema() != nil {
			stateCopy = e.graph.Schema().ApplyUpdate(stateCopy, t.Overlay)
		}
	}
	// Add execution context to state so nodes can access event channel.
	stateCopy[StateKeyExecContext] = execCtx
	execCtx.stateMutex.RUnlock()

	// Add current node ID to state so nodes can access it.
	stateCopy[StateKeyCurrentNodeID] = t.NodeID

	// Get global and per-node callbacks.
	globalCallbacks, _ := stateCopy[StateKeyNodeCallbacks].(*NodeCallbacks)
	node, exists := e.graph.Node(t.NodeID)
	var perNodeCallbacks *NodeCallbacks
	if exists {
		perNodeCallbacks = node.callbacks
	}

	// Merge callbacks: global callbacks run first, then per-node callbacks.
	mergedCallbacks := e.mergeNodeCallbacks(globalCallbacks, perNodeCallbacks)

	// Run before node callbacks.
	if mergedCallbacks != nil {
		customResult, err := mergedCallbacks.RunBeforeNode(ctx, callbackCtx, stateCopy)
		if err != nil {
			e.emitNodeErrorEvent(execCtx, t.NodeID, nodeType, step, err)
			return fmt.Errorf("before node callback failed for node %s: %w", t.NodeID, err)
		}
		if customResult != nil {
			// Use custom result from callback.
			if err := e.handleNodeResult(ctx, execCtx, t, customResult); err != nil {
				return err
			}
			// Process conditional edges after node execution.
			if err := e.processConditionalEdges(ctx, execCtx, t.NodeID, step); err != nil {
				return fmt.Errorf("conditional edge processing failed for node %s: %w", t.NodeID, err)
			}
			// Emit node completion event.
			e.emitNodeCompleteEvent(execCtx, t.NodeID, nodeType, step, nodeStart)
			return nil
		}
	}

	// Execute the node function.
	result, err := e.executeNodeFunction(nodeCtx, execCtx, t)
	if err != nil {
		// Check if this is an interrupt error
		if IsInterruptError(err) {
			// For interrupt errors, we need to set the node ID and task ID
			if interrupt, ok := GetInterruptError(err); ok {
				interrupt.NodeID = t.NodeID
				interrupt.TaskID = t.NodeID // Use NodeID as TaskID for now
				interrupt.Step = step
			}
			return err // Return interrupt error directly without wrapping
		}

		// Run on node error callbacks.
		if mergedCallbacks != nil {
			mergedCallbacks.RunOnNodeError(ctx, callbackCtx, stateCopy, err)
		}
		e.emitNodeErrorEvent(execCtx, t.NodeID, nodeType, step, err)
		return fmt.Errorf("node %s execution failed: %w", t.NodeID, err)
	}

	// Run after node callbacks.
	if mergedCallbacks != nil {
		customResult, err := mergedCallbacks.RunAfterNode(ctx, callbackCtx, stateCopy, result, nil)
		if err != nil {
			e.emitNodeErrorEvent(execCtx, t.NodeID, nodeType, step, err)
			return fmt.Errorf("after node callback failed for node %s: %w", t.NodeID, err)
		}
		if customResult != nil {
			result = customResult
		}
	}

	// Handle result and process channel writes.
	if err := e.handleNodeResult(ctx, execCtx, t, result); err != nil {
		return err
	}

	// Process conditional edges after node execution.
	if err := e.processConditionalEdges(ctx, execCtx, t.NodeID, step); err != nil {
		return fmt.Errorf("conditional edge processing failed for node %s: %w", t.NodeID, err)
	}

	// Emit node completion event.
	e.emitNodeCompleteEvent(execCtx, t.NodeID, nodeType, step, nodeStart)

	return nil
}

// getNodeType retrieves the node type for a given node ID.
func (e *Executor) getNodeType(nodeID string) NodeType {
	node, exists := e.graph.Node(nodeID)
	if !exists {
		return NodeTypeFunction // Default fallback.
	}
	return node.Type
}

// getNodeName retrieves the node name for a given node ID.
func (e *Executor) getNodeName(nodeID string) string {
	node, exists := e.graph.Node(nodeID)
	if !exists {
		return nodeID // Default to node ID if node not found.
	}
	return node.Name
}

// getSessionID retrieves the session ID from the execution context.
func (e *Executor) getSessionID(execCtx *ExecutionContext) string {
	if execCtx == nil {
		return ""
	}
	execCtx.stateMutex.RLock()
	defer execCtx.stateMutex.RUnlock()
	if sess, ok := execCtx.State[StateKeySession]; ok {
		if s, ok := sess.(*session.Session); ok && s != nil {
			return s.ID
		}
	}
	return ""
}

// mergeNodeCallbacks merges global and per-node callbacks.
// Global callbacks are executed first, followed by per-node callbacks.
// This allows per-node callbacks to override or extend global behavior.
func (e *Executor) mergeNodeCallbacks(global, perNode *NodeCallbacks) *NodeCallbacks {
	if global == nil && perNode == nil {
		return nil
	}
	if global == nil {
		return perNode
	}
	if perNode == nil {
		return global
	}

	// Create a new merged callbacks instance.
	merged := NewNodeCallbacks()

	// Add global callbacks first (they execute first).
	merged.BeforeNode = append(merged.BeforeNode, global.BeforeNode...)
	merged.AfterNode = append(merged.AfterNode, global.AfterNode...)
	merged.OnNodeError = append(merged.OnNodeError, global.OnNodeError...)

	// Add per-node callbacks (they execute after global callbacks).
	merged.BeforeNode = append(merged.BeforeNode, perNode.BeforeNode...)
	merged.AfterNode = append(merged.AfterNode, perNode.AfterNode...)
	merged.OnNodeError = append(merged.OnNodeError, perNode.OnNodeError...)

	return merged
}

// emitNodeStartEvent emits the node start event.
func (e *Executor) emitNodeStartEvent(
	execCtx *ExecutionContext,
	nodeID string,
	nodeType NodeType,
	step int,
	startTime time.Time,
) {
	if execCtx.EventChan == nil {
		return
	}

	execCtx.stateMutex.RLock()
	inputKeys := extractStateKeys(execCtx.State)

	// Extract model input for LLM nodes.
	var modelInput string
	if nodeType == NodeTypeLLM {
		if userInput, exists := execCtx.State[StateKeyUserInput]; exists {
			if input, ok := userInput.(string); ok {
				modelInput = input
			}
		}
	}

	execCtx.stateMutex.RUnlock()

	startEvent := NewNodeStartEvent(
		WithNodeEventInvocationID(execCtx.InvocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(nodeType),
		WithNodeEventStepNumber(step),
		WithNodeEventStartTime(startTime),
		WithNodeEventInputKeys(inputKeys),
		WithNodeEventModelInput(modelInput),
	)
	select {
	case execCtx.EventChan <- startEvent:
	default:
	}
}

// executeNodeFunction executes the actual node function.
func (e *Executor) executeNodeFunction(
	ctx context.Context, execCtx *ExecutionContext, t *Task,
) (any, error) {
	nodeID := t.NodeID
	node, exists := e.graph.Node(nodeID)
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}

	// Execute the node with read lock on state.
	execCtx.stateMutex.RLock()

	// Determine the state to use for this task.
	var stateCopy State
	if t.Input != nil {
		// Use the task's input state (for fan-out branches).
		if inputState, ok := t.Input.(State); ok {
			stateCopy = make(State, len(inputState))
			maps.Copy(stateCopy, inputState)
		} else {
			// Fallback to global state if Input is not State type.
			stateCopy = make(State, len(execCtx.State))
			maps.Copy(stateCopy, execCtx.State)
		}
	} else {
		// Use the global execution state.
		stateCopy = make(State, len(execCtx.State))
		maps.Copy(stateCopy, execCtx.State)
		// Apply overlay if present to form the isolated input view.
		if t.Overlay != nil && e.graph.Schema() != nil {
			stateCopy = e.graph.Schema().ApplyUpdate(stateCopy, t.Overlay)
		}
	}

	// Add execution context to state so nodes can access event channel.
	stateCopy[StateKeyExecContext] = execCtx
	// Add current node ID to state so nodes can access it.
	stateCopy[StateKeyCurrentNodeID] = nodeID
	execCtx.stateMutex.RUnlock()

	return node.Function(ctx, stateCopy)
}

// emitNodeErrorEvent emits the node error event.
func (e *Executor) emitNodeErrorEvent(
	execCtx *ExecutionContext,
	nodeID string,
	nodeType NodeType,
	step int,
	err error,
) {
	if execCtx.EventChan == nil {
		return
	}

	errorEvent := NewNodeErrorEvent(
		WithNodeEventInvocationID(execCtx.InvocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(nodeType),
		WithNodeEventStepNumber(step),
		WithNodeEventError(err.Error()),
	)
	select {
	case execCtx.EventChan <- errorEvent:
	default:
	}
}

// handleNodeResult handles the result from node execution.
func (e *Executor) handleNodeResult(
	ctx context.Context, execCtx *ExecutionContext, t *Task, result any,
) error {
	if result == nil {
		return nil
	}
	// Handle node result by concrete type.
	fanOut := false
	switch v := result.(type) {
	case State: // State update.
		e.updateStateFromResult(execCtx, v)
	case *Command: // Single command.
		if v != nil {
			if err := e.handleCommandResult(ctx, execCtx, v); err != nil {
				return err
			}
			// If the command explicitly routes via GoTo, avoid also writing to
			// channels from static edges for this task to prevent double-triggering
			// the downstream node (once via GoTo, once via edge writes).
			if v.GoTo != "" {
				fanOut = true
			}
		}
	case []*Command: // Fan-out commands.
		// Fan-out: enqueue tasks with overlays.
		fanOut = true
		e.enqueueCommands(execCtx, t, v)
	default:
	}

	// Process channel writes, unless this is a fan-out case to avoid double trigger.
	if !fanOut && len(t.Writes) > 0 {
		e.processChannelWrites(execCtx, t.Writes)
	}

	return nil
}

// enqueueCommands enqueues a set of commands as pending tasks for subsequent steps.
func (e *Executor) enqueueCommands(execCtx *ExecutionContext, t *Task, cmds []*Command) {
	if len(cmds) == 0 {
		return
	}
	nextStep := 0
	// TaskID embeds step when created; since we don't track current step here,
	// we set 0 and let uniqueness be per node list; this is acceptable for now.
	// If needed, we can carry step into handleNodeResult params later.
	newTasks := make([]*Task, 0, len(cmds))

	// Get a copy of the current global state to merge with each command
	execCtx.stateMutex.RLock()
	globalState := make(State, len(execCtx.State))
	maps.Copy(globalState, execCtx.State)
	execCtx.stateMutex.RUnlock()

	for _, c := range cmds {
		target := c.GoTo
		if target == "" {
			target = t.NodeID
		}

		// Merge global state with command-specific overlay
		mergedState := make(State)
		maps.Copy(mergedState, globalState)
		if c.Update != nil {
			maps.Copy(mergedState, c.Update)
		}

		// Create task with merged state instead of just overlay
		newTask := &Task{
			NodeID:   target,
			Input:    mergedState, // Use merged state instead of nil.
			Writes:   t.Writes,    // Copy writes from source task.
			Triggers: t.Triggers,  // Copy triggers from source task.
			TaskID:   fmt.Sprintf("%s-%d", target, nextStep),
			TaskPath: append([]string{}, t.TaskPath...),
			Overlay:  nil, // No overlay needed since we have merged state.
		}

		newTasks = append(newTasks, newTask)
	}

	execCtx.tasksMutex.Lock()
	execCtx.pendingTasks = append(execCtx.pendingTasks, newTasks...)
	execCtx.tasksMutex.Unlock()
}

// updateStateFromResult updates the execution context state from a State result.
func (e *Executor) updateStateFromResult(execCtx *ExecutionContext, stateResult State) {
	execCtx.stateMutex.Lock()
	defer execCtx.stateMutex.Unlock()

	// Special handling for message-related state to preserve GraphAgent functionality.
	if _, hasMessages := stateResult[StateKeyMessages]; hasMessages {
		maps.Copy(execCtx.State, stateResult)
		return
	}
	// Use schema-based reducers when available for proper merging.
	if e.graph != nil && e.graph.Schema() != nil {
		execCtx.State = e.graph.Schema().ApplyUpdate(execCtx.State, stateResult)
		return
	}
	// Fallback to direct assignment if no schema available.
	maps.Copy(execCtx.State, stateResult)
}

// handleCommandResult handles a Command result from node execution.
func (e *Executor) handleCommandResult(
	ctx context.Context, execCtx *ExecutionContext, cmdResult *Command,
) error {
	// Update state with command updates.
	if cmdResult.Update != nil {
		e.updateStateFromResult(execCtx, cmdResult.Update)
	}

	// Handle GoTo routing.
	if cmdResult.GoTo != "" {
		e.handleCommandRouting(ctx, execCtx, cmdResult.GoTo)
	}

	return nil
}

// handleCommandRouting handles the routing specified by a Command.
func (e *Executor) handleCommandRouting(
	ctx context.Context, execCtx *ExecutionContext, targetNode string,
) {
	// Create trigger channel for the target node (including self).
	triggerChannel := fmt.Sprintf("trigger:%s", targetNode)
	e.graph.addNodeTrigger(triggerChannel, targetNode)

	// Write to the channel to trigger the target node.
	ch, _ := e.graph.getChannel(triggerChannel)
	if ch != nil {
		ch.Update([]any{channelUpdateMarker}, -1)
	}

	// Emit channel update event.
	e.emitChannelUpdateEvent(execCtx, triggerChannel, channel.BehaviorLastValue, []string{targetNode})
}

// processChannelWrites processes the channel writes for a task.
func (e *Executor) processChannelWrites(execCtx *ExecutionContext, writes []channelWriteEntry) {
	for _, write := range writes {
		ch, _ := e.graph.getChannel(write.Channel)
		if ch != nil {
			ch.Update([]any{write.Value}, -1)

			// Emit channel update event.
			e.emitChannelUpdateEvent(execCtx, write.Channel, ch.Behavior, e.getTriggeredNodes(write.Channel))
			// Accumulate into pendingWrites to be saved with the next checkpoint.
			execCtx.pendingMu.Lock()
			execCtx.pendingWrites = append(execCtx.pendingWrites, PendingWrite{
				Channel:  write.Channel,
				Value:    write.Value,
				TaskID:   execCtx.InvocationID, // Use invocation ID as task ID for now
				Sequence: execCtx.seq.Add(1),   // Use atomic increment for deterministic replay
			})
			execCtx.pendingMu.Unlock()
		}
	}
}

// restoreCheckpointValueWithSchema restores a checkpoint value to its proper type using schema information.
func (e *Executor) restoreCheckpointValueWithSchema(value any, field StateField) any {
	// Skip if already the correct type.
	if reflect.TypeOf(value) == field.Type {
		return value
	}
	// Approach 1: Use Default as template if available.
	if field.Default != nil {
		template := field.Default()
		if jsonBytes, err := json.Marshal(value); err == nil {
			// Use a pointer to the template for unmarshaling.
			templatePtr := reflect.New(reflect.TypeOf(template))
			templatePtr.Elem().Set(reflect.ValueOf(template))

			if err := json.Unmarshal(jsonBytes, templatePtr.Interface()); err == nil {
				return templatePtr.Elem().Interface()
			}
		}
	}
	// Approach 2: Use reflection to create correct type.
	if field.Type != nil {
		ptr := reflect.New(field.Type)
		if jsonBytes, err := json.Marshal(value); err == nil {
			if err := json.Unmarshal(jsonBytes, ptr.Interface()); err == nil {
				return ptr.Elem().Interface()
			}
		}
	}
	// Fallback: return value as-is.
	return value
}

// emitChannelUpdateEvent emits a channel update event.
func (e *Executor) emitChannelUpdateEvent(
	execCtx *ExecutionContext,
	channelName string,
	channelType channel.Behavior,
	triggeredNodes []string,
) {
	if execCtx.EventChan == nil {
		return
	}

	channelEvent := NewChannelUpdateEvent(
		WithChannelEventInvocationID(execCtx.InvocationID),
		WithChannelEventChannelName(channelName),
		WithChannelEventChannelType(channelType),
		WithChannelEventAvailable(true),
		WithChannelEventTriggeredNodes(triggeredNodes),
	)
	select {
	case execCtx.EventChan <- channelEvent:
	default:
	}
}

// emitNodeCompleteEvent emits the node completion event.
func (e *Executor) emitNodeCompleteEvent(
	execCtx *ExecutionContext,
	nodeID string,
	nodeType NodeType,
	step int,
	startTime time.Time,
) {
	if execCtx.EventChan == nil {
		return
	}

	execEndTime := time.Now()
	execCtx.stateMutex.RLock()
	outputKeys := extractStateKeys(execCtx.State)
	execCtx.stateMutex.RUnlock()

	completeEvent := NewNodeCompleteEvent(
		WithNodeEventInvocationID(execCtx.InvocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(nodeType),
		WithNodeEventStepNumber(step),
		WithNodeEventStartTime(startTime),
		WithNodeEventEndTime(execEndTime),
		WithNodeEventOutputKeys(outputKeys),
	)
	select {
	case execCtx.EventChan <- completeEvent:
	default:
	}
}

// updateChannels processes channel updates and emits events.
func (e *Executor) updateChannels(ctx context.Context, execCtx *ExecutionContext, step int) error {
	e.emitUpdateStepEvent(execCtx, step)
	e.emitStateUpdateEvent(execCtx)
	return nil
}

// emitUpdateStepEvent emits the update step event.
func (e *Executor) emitUpdateStepEvent(execCtx *ExecutionContext, step int) {
	updatedChannels := e.getUpdatedChannels()
	updateEvent := NewPregelStepEvent(
		WithPregelEventInvocationID(execCtx.InvocationID),
		WithPregelEventStepNumber(step),
		WithPregelEventPhase(PregelPhaseUpdate),
		WithPregelEventTaskCount(len(updatedChannels)),
		WithPregelEventUpdatedChannels(updatedChannels),
	)
	select {
	case execCtx.EventChan <- updateEvent:
	default:
	}
}

// emitStateUpdateEvent emits the state update event.
func (e *Executor) emitStateUpdateEvent(execCtx *ExecutionContext) {
	if execCtx.EventChan == nil {
		return
	}

	execCtx.stateMutex.RLock()
	stateKeys := extractStateKeys(execCtx.State)
	stateLen := len(execCtx.State)
	execCtx.stateMutex.RUnlock()

	stateEvent := NewStateUpdateEvent(
		WithStateEventInvocationID(execCtx.InvocationID),
		WithStateEventUpdatedKeys(stateKeys),
		WithStateEventStateSize(stateLen),
	)
	select {
	case execCtx.EventChan <- stateEvent:
	default:
	}
}

// getUpdatedChannels returns a list of updated channel names.
func (e *Executor) getUpdatedChannels() []string {
	var updated []string
	for name, channel := range e.graph.getAllChannels() {
		if channel.IsAvailable() {
			updated = append(updated, name)
		}
	}
	return updated
}

// getUpdatedChannelsInStep returns a list of channels updated in the current step.
func (e *Executor) getUpdatedChannelsInStep(step int) []string {
	var updated []string
	for name, channel := range e.graph.getAllChannels() {
		if channel.IsUpdatedInStep(step) {
			updated = append(updated, name)
		}
	}
	return updated
}

// getTriggeredNodes returns the list of nodes triggered by a channel.
func (e *Executor) getTriggeredNodes(channelName string) []string {
	triggerToNodes := e.graph.getTriggerToNodes()
	if nodes, exists := triggerToNodes[channelName]; exists {
		return nodes
	}
	return nil
}

// processConditionalEdges evaluates conditional edges for a node and creates dynamic channels.
func (e *Executor) processConditionalEdges(
	ctx context.Context,
	execCtx *ExecutionContext,
	nodeID string,
	step int,
) error {
	condEdge, exists := e.graph.ConditionalEdge(nodeID)
	if !exists {
		return nil
	}

	// Evaluate the conditional function.
	execCtx.stateMutex.RLock()
	stateCopy := make(State, len(execCtx.State))
	maps.Copy(stateCopy, execCtx.State)
	execCtx.stateMutex.RUnlock()
	result, err := condEdge.Condition(ctx, stateCopy)
	if err != nil {
		return fmt.Errorf("conditional edge evaluation failed for node %s: %w", nodeID, err)
	}

	// Process the conditional result.
	return e.processConditionalResult(execCtx, condEdge, result, step)
}

// processConditionalResult processes the result of a conditional edge evaluation.
func (e *Executor) processConditionalResult(
	execCtx *ExecutionContext,
	condEdge *ConditionalEdge,
	result string,
	step int,
) error {
	target, exists := condEdge.PathMap[result]
	if !exists {
		log.Warnf("⚠️ Step %d: No target found for conditional result %v in path map", step, result)
		return nil
	}

	// Create and trigger the target channel.
	channelName := fmt.Sprintf("branch:to:%s", target)
	e.graph.addChannel(channelName, channel.BehaviorLastValue)
	e.graph.addNodeTrigger(channelName, target)

	// Trigger the target by writing to the channel.
	ch, ok := e.graph.getChannel(channelName)
	if ok && ch != nil {
		ch.Update([]any{channelUpdateMarker}, -1)
		e.emitChannelUpdateEvent(execCtx, channelName, channel.BehaviorLastValue, []string{target})
	} else {
		log.Warnf("❌ Step %d: Failed to get channel %s", step, channelName)
	}
	return nil
}

// handleInterrupt handles an interrupt during graph execution.
func (e *Executor) handleInterrupt(
	ctx context.Context,
	execCtx *ExecutionContext,
	interrupt *InterruptError,
	step int,
	checkpointConfig map[string]any,
) error {
	// Create an interrupt checkpoint with the current state.
	if e.checkpointSaver != nil && checkpointConfig != nil {
		// Get the current state with all updates from nodes
		execCtx.stateMutex.RLock()
		currentState := make(State)
		for k, v := range execCtx.State {
			currentState[k] = v
		}
		execCtx.stateMutex.RUnlock()

		// Note: We do NOT remove resume values from state here because
		// they may be needed when the node is re-executed after resume

		// Set interrupt state in the checkpoint.
		checkpoint := e.createCheckpointFromState(currentState, step)

		// IMPORTANT: Set parent checkpoint ID from current config to maintain proper tree structure
		if parentCheckpointID := GetCheckpointID(checkpointConfig); parentCheckpointID != "" {
			checkpoint.ParentCheckpointID = parentCheckpointID
			// Setting parent checkpoint ID for interrupt
		}

		checkpoint.SetInterruptState(
			interrupt.NodeID,
			interrupt.TaskID,
			interrupt.Value,
			step,
			interrupt.Path,
		)

		// Create metadata for the interrupt checkpoint.
		metadata := NewCheckpointMetadata(CheckpointSourceInterrupt, step)
		metadata.IsResuming = false

		// Set next nodes for recovery
		// IMPORTANT: For internal interrupts (from graph.Interrupt within a node),
		// the interrupted node needs to be re-executed to complete its work.
		// We must include it in NextNodes.
		nextNodes := e.getNextNodes(execCtx.State)

		// Ensure the interrupted node is included
		hasNode := false
		for _, nodeID := range nextNodes {
			if nodeID == interrupt.NodeID {
				hasNode = true
				break
			}
		}
		if !hasNode && interrupt.NodeID != "" {
			nextNodes = append([]string{interrupt.NodeID}, nextNodes...)
		}
		checkpoint.NextNodes = nextNodes
		checkpoint.NextChannels = e.getNextChannels(execCtx.State)

		// Store the interrupt checkpoint using PutFull for consistency
		// Use a new context to ensure checkpoint saves even if main context is canceled.
		// Use configured timeout, fallback to default if not set.
		saveTimeout := e.checkpointSaveTimeout
		if saveTimeout == 0 {
			saveTimeout = defaultCheckpointSaveTimeout
		}
		saveCtx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		defer cancel()

		req := PutFullRequest{
			Config:        checkpointConfig,
			Checkpoint:    checkpoint,
			Metadata:      metadata,
			NewVersions:   checkpoint.ChannelVersions,
			PendingWrites: []PendingWrite{},
		}
		updatedConfig, err := e.checkpointSaver.PutFull(saveCtx, req)
		if err != nil {
			log.Debugf("Failed to store interrupt checkpoint: %v", err)
		} else {
			// Update the config with new checkpoint ID for proper parent tracking
			if configurable, ok := checkpointConfig[CfgKeyConfigurable].(map[string]any); ok {
				if updatedConfigurable, ok := updatedConfig[CfgKeyConfigurable].(map[string]any); ok {
					configurable[CfgKeyCheckpointID] = updatedConfigurable[CfgKeyCheckpointID]
				}
			}
		}
	}

	// Emit interrupt event.
	interruptEvent := NewPregelInterruptEvent(
		WithPregelEventInvocationID(execCtx.InvocationID),
		WithPregelEventStepNumber(step),
		WithPregelEventNodeID(interrupt.NodeID),
		WithPregelEventInterruptValue(interrupt.Value),
	)
	select {
	case execCtx.EventChan <- interruptEvent:
	default:
	}

	// Return the interrupt error to propagate it to the caller.
	return interrupt
}

// createCheckpointFromState creates a checkpoint from the current execution state.
func (e *Executor) createCheckpointFromState(state State, step int) *Checkpoint {
	// Convert state to channel values, ensuring we capture the latest state
	// including any updates from nodes that haven't been written to channels yet.
	channelValues := make(map[string]any)
	for k, v := range state {
		channelValues[k] = v
	}

	// Create channel versions from current channel states
	channelVersions := make(map[string]any)
	channels := e.graph.getAllChannels()
	for channelName, channel := range channels {
		if channel.IsAvailable() {
			channelVersions[channelName] = channel.Version
		}
	}

	// Create versions seen for each node (simplified for now)
	versionsSeen := make(map[string]map[string]any)
	// TODO: Implement proper version tracking per node per channel

	// Create checkpoint.
	checkpoint := NewCheckpoint(channelValues, channelVersions, versionsSeen)

	// Use step-specific channels if step is provided, otherwise fallback to all available
	if step >= 0 {
		checkpoint.UpdatedChannels = e.getUpdatedChannelsInStep(step)
	} else {
		checkpoint.UpdatedChannels = e.getUpdatedChannels()
	}
	return checkpoint
}

// getNextNodes determines which nodes should be executed next based on the current state.
func (e *Executor) getNextNodes(state State) []string {
	var nextNodes []string
	// Check for nodes that are ready to execute based on channel triggers
	triggerToNodes := e.graph.getTriggerToNodes()
	for channelName, nodeIDs := range triggerToNodes {
		channel, _ := e.graph.getChannel(channelName)
		if channel != nil && channel.IsAvailable() {
			nextNodes = append(nextNodes, nodeIDs...)
		}
	}
	// Remove duplicates
	seen := make(map[string]bool)
	var uniqueNodes []string
	for _, nodeID := range nextNodes {
		if !seen[nodeID] {
			seen[nodeID] = true
			uniqueNodes = append(uniqueNodes, nodeID)
		}
	}
	return uniqueNodes
}

// getNextChannels determines which channels should be triggered next.
func (e *Executor) getNextChannels(state State) []string {
	var nextChannels []string

	// Get all channels that are available
	channels := e.graph.getAllChannels()
	for channelName, channel := range channels {
		if channel.IsAvailable() {
			nextChannels = append(nextChannels, channelName)
		}
	}
	return nextChannels
}

// getNextChannelsInStep determines which channels were updated in the current step.
func (e *Executor) getNextChannelsInStep(step int) []string {
	var nextChannels []string

	// Get channels that were updated in the current step
	channels := e.graph.getAllChannels()
	for channelName, channel := range channels {
		if channel.IsUpdatedInStep(step) {
			nextChannels = append(nextChannels, channelName)
		}
	}
	return nextChannels
}

// clearChannelStepMarks clears the step marks for all channels after checkpoint creation.
func (e *Executor) clearChannelStepMarks() {
	channels := e.graph.getAllChannels()
	for _, channel := range channels {
		channel.ClearStepMark()
	}
}

// CheckpointManager returns the executor's checkpoint manager.
// Returns nil if no checkpoint saver was configured.
func (e *Executor) CheckpointManager() *CheckpointManager {
	return e.checkpointManager
}

// Fork creates a new branch from an existing checkpoint within the same lineage.
// This allows exploring alternative execution paths from any checkpoint.
func (e *Executor) Fork(ctx context.Context, config map[string]any) (map[string]any, error) {
	if e.checkpointSaver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Get the source checkpoint.
	log.Debugf("Fork: Attempting to get checkpoint with config: %v", config)
	sourceTuple, err := e.checkpointSaver.GetTuple(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get source checkpoint: %w", err)
	}
	if sourceTuple == nil {
		return nil, fmt.Errorf("source checkpoint not found")
	}

	// Fork the checkpoint (creates new ID and sets parent).
	log.Debugf("Fork: Retrieved source checkpoint - ID=%s, Step=%d, NextNodes=%v, PendingWrites=%d",
		sourceTuple.Checkpoint.ID, sourceTuple.Metadata.Step, sourceTuple.Checkpoint.NextNodes, len(sourceTuple.PendingWrites))

	forkedCheckpoint := sourceTuple.Checkpoint.Fork()

	log.Debugf("Fork: Forked checkpoint - ID=%s, NextNodes=%v",
		forkedCheckpoint.ID, forkedCheckpoint.NextNodes)

	// Create metadata for the fork.
	metadata := NewCheckpointMetadata(CheckpointSourceFork, sourceTuple.Metadata.Step)
	metadata.Parents = map[string]string{
		GetNamespace(config): sourceTuple.Checkpoint.ID,
	}

	// Save the forked checkpoint with same lineage_id.
	lineageID := GetLineageID(config)
	namespace := GetNamespace(config)
	newConfig := CreateCheckpointConfig(lineageID, "", namespace)

	// Copy pending writes from the source to ensure resumed execution can continue.
	// If the source has pending writes, we need to preserve them in the fork.
	var pendingWrites []PendingWrite
	if len(sourceTuple.PendingWrites) > 0 {
		pendingWrites = make([]PendingWrite, len(sourceTuple.PendingWrites))
		copy(pendingWrites, sourceTuple.PendingWrites)
	}

	// Use PutFull to save both checkpoint and pending writes atomically.
	req := PutFullRequest{
		Config:        newConfig,
		Checkpoint:    forkedCheckpoint,
		Metadata:      metadata,
		NewVersions:   forkedCheckpoint.ChannelVersions,
		PendingWrites: pendingWrites,
	}

	updatedConfig, err := e.checkpointSaver.PutFull(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to save forked checkpoint: %w", err)
	}

	return updatedConfig, nil
}
