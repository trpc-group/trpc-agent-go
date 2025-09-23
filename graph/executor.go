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
	"runtime/debug"
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
	defaultStepTimeout           = time.Duration(0) // No timeout by default, users can set if needed.
	defaultCheckpointSaveTimeout = 10 * time.Second // Default timeout for checkpoint save operations.
)

// Executor executes a graph with the given initial state using Pregel-style BSP execution.
//
// Runtime isolation principle:
//   - Executor is designed to be reusable across concurrent runs.
//   - It must not hold per-run mutable state; such data lives in ExecutionContext.
//   - Checkpoint-derived artifacts (e.g., lastCheckpoint, pendingWrites) are
//     carried inside ExecutionContext and never stored on the Executor.
//
// This makes it safe to share a single Executor instance between many
// concurrent invocations without cross-run interference.
type Executor struct {
	graph                 *Graph
	channelBufferSize     int
	maxSteps              int
	stepTimeout           time.Duration
	nodeTimeout           time.Duration
	checkpointSaveTimeout time.Duration
	checkpointSaver       CheckpointSaver
	checkpointManager     *CheckpointManager
}

// ExecutorOption is a function that configures an Executor.
type ExecutorOption func(*ExecutorOptions)

// ExecutorOptions contains configuration options for creating an Executor.
type ExecutorOptions struct {
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// MaxSteps is the maximum number of steps for graph execution.
	MaxSteps int
	// StepTimeout is the timeout for each step (default: 0 = no timeout).
	StepTimeout time.Duration
	// NodeTimeout is the timeout for individual node execution
	// (default: derived from StepTimeout/2 when StepTimeout>0; otherwise no timeout).
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
	// Calculate node timeout: use provided value or derive from step timeout if step timeout is set.
	nodeTimeout := options.NodeTimeout
	if nodeTimeout == 0 && options.StepTimeout > 0 {
		// Only derive from step timeout if step timeout is explicitly set.
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
	// Use a visited set keyed by underlying pointer to break reference cycles
	// across pointers, maps, and slices. The values are the already-created
	// copies to return when revisiting the same reference.
	visited := make(map[uintptr]any)

	var copyRecursive func(reflect.Value) any
	copyRecursive = func(rv reflect.Value) any {
		if !rv.IsValid() {
			return nil
		}

		switch rv.Kind() {
		case reflect.Interface:
			if rv.IsNil() {
				return nil
			}
			return copyRecursive(rv.Elem())

		case reflect.Ptr:
			if rv.IsNil() {
				return nil
			}
			ptr := rv.Pointer()
			if cached, ok := visited[ptr]; ok {
				return cached
			}
			elem := rv.Elem()
			newPtr := reflect.New(elem.Type())
			// Cache before descending to handle self-referential structures.
			visited[ptr] = newPtr.Interface()
			newPtr.Elem().Set(reflect.ValueOf(copyRecursive(elem)))
			return newPtr.Interface()

		case reflect.Map:
			if rv.IsNil() {
				return reflect.Zero(rv.Type()).Interface()
			}
			ptr := rv.Pointer()
			if cached, ok := visited[ptr]; ok {
				return cached
			}
			newMap := reflect.MakeMapWithSize(rv.Type(), rv.Len())
			visited[ptr] = newMap.Interface()
			for _, mk := range rv.MapKeys() {
				mv := rv.MapIndex(mk)
				newMap.SetMapIndex(mk, reflect.ValueOf(copyRecursive(mv)))
			}
			return newMap.Interface()

		case reflect.Slice:
			if rv.IsNil() {
				return reflect.Zero(rv.Type()).Interface()
			}
			ptr := rv.Pointer()
			if cached, ok := visited[ptr]; ok {
				return cached
			}
			l := rv.Len()
			newSlice := reflect.MakeSlice(rv.Type(), l, l)
			visited[ptr] = newSlice.Interface()
			for i := 0; i < l; i++ {
				newSlice.Index(i).Set(reflect.ValueOf(copyRecursive(rv.Index(i))))
			}
			return newSlice.Interface()

		case reflect.Array:
			l := rv.Len()
			newArr := reflect.New(rv.Type()).Elem()
			for i := 0; i < l; i++ {
				newArr.Index(i).Set(reflect.ValueOf(copyRecursive(rv.Index(i))))
			}
			return newArr.Interface()

		case reflect.Struct:
			// Create a new struct and copy only exported fields to avoid
			// touching unexported sync primitives, etc.
			newStruct := reflect.New(rv.Type()).Elem()
			for i := 0; i < rv.NumField(); i++ {
				ft := rv.Type().Field(i)
				if ft.PkgPath != "" { // unexported
					continue
				}
				dstField := newStruct.Field(i)
				if !dstField.CanSet() {
					continue
				}
				srcField := rv.Field(i)
				copied := copyRecursive(srcField)
				// If copied is nil, Set with a typed zero value to avoid
				// "reflect: call of reflect.Value.Set on zero Value".
				if copied == nil {
					dstField.Set(reflect.Zero(dstField.Type()))
					continue
				}
				srcVal := reflect.ValueOf(copied)
				// Align types as needed.
				if srcVal.Type().AssignableTo(dstField.Type()) {
					dstField.Set(srcVal)
				} else if srcVal.Type().ConvertibleTo(dstField.Type()) {
					dstField.Set(srcVal.Convert(dstField.Type()))
				} else {
					// Fallback: set zero value if types are incompatible.
					dstField.Set(reflect.Zero(dstField.Type()))
				}
			}
			return newStruct.Interface()

		case reflect.Func, reflect.Chan, reflect.UnsafePointer:
			// Not safely copyable/serializable; return zero value.
			return reflect.Zero(rv.Type()).Interface()

		default:
			// Scalars and other immutable kinds – return as-is.
			return rv.Interface()
		}
	}

	// Fast-path for common JSON-compatible types without reflection.
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
	}

	return copyRecursive(reflect.ValueOf(value))
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
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Errorf("panic in executor goroutine: %v\n%s", r, string(stack))
				agent.EmitEvent(ctx, invocation, eventChan, NewPregelErrorEvent(
					WithPregelEventInvocationID(invocation.InvocationID),
					WithPregelEventStepNumber(-1),
					WithPregelEventError(fmt.Sprintf("executor panic: %v", r)),
				))
			}
			close(eventChan)
		}()
		if err := e.executeGraph(ctx, initialState, invocation, eventChan, startTime); err != nil {
			// Check if this is an interrupt error.
			if IsInterruptError(err) {
				// For interrupt errors, we don't emit an error event.
				// The interrupt will be handled by the caller.
				return
			}
			// Emit error event for other errors.
			agent.EmitEvent(ctx, invocation, eventChan, NewPregelErrorEvent(
				WithPregelEventInvocationID(invocation.InvocationID),
				WithPregelEventStepNumber(-1),
				WithPregelEventError(err.Error()),
			))
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
	execState, checkpointConfig, resumed, resumedStep, lastCkpt, restoredPending :=
		e.prepareCheckpointAndState(ctx, initialState, invocation)

	execState = e.processResumeCommand(execState, initialState)

	execCtx := e.buildExecutionContext(
		eventChan, invocation.InvocationID, execState, resumed, lastCkpt,
	)
	if len(restoredPending) > 0 {
		execCtx.pendingWrites = append(execCtx.pendingWrites[:0], restoredPending...)
	}

	if resumed && len(execCtx.pendingWrites) > 0 {
		log.Debugf("🔧 Executor: applying %d pending writes", len(execCtx.pendingWrites))
		e.applyPendingWrites(ctx, invocation, execCtx, execCtx.pendingWrites)
	}

	if e.checkpointSaver != nil && !resumed {
		if err := e.createCheckpointAndSave(
			ctx, &checkpointConfig, execCtx.State, CheckpointSourceInput, -1, execCtx,
		); err != nil {
			log.Debugf("Failed to create initial checkpoint: %v", err)
		}
	}

	startStep := 0
	if resumed && resumedStep >= 0 {
		startStep = resumedStep + 1
	}

	stepsExecuted, err := e.runBspLoop(ctx, invocation, execCtx, &checkpointConfig, startStep)
	if err != nil {
		return err
	}

	agent.EmitEvent(ctx, invocation, eventChan, e.buildCompletionEvent(execCtx, startTime, stepsExecuted))
	return nil
}

// prepareCheckpointAndState initializes or restores state and checkpointing.
func (e *Executor) prepareCheckpointAndState(
	ctx context.Context,
	initialState State,
	invocation *agent.Invocation,
) (State, map[string]any, bool, int, *Checkpoint, []PendingWrite) {
	if e.checkpointSaver == nil {
		execState := e.initializeState(initialState)
		e.initializeChannels(execState, true)
		return execState, nil, false, 0, nil, nil
	}
	return e.resumeOrInitWithSaver(ctx, initialState, invocation)
}

// resumeOrInitWithSaver handles state preparation when checkpoint saver is set.
func (e *Executor) resumeOrInitWithSaver(
	ctx context.Context,
	initialState State,
	invocation *agent.Invocation,
) (State, map[string]any, bool, int, *Checkpoint, []PendingWrite) {
	var lineageID string
	if id, ok := initialState[CfgKeyLineageID].(string); ok && id != "" {
		lineageID = id
	} else if invocation.InvocationID != "" {
		lineageID = invocation.InvocationID
	} else {
		lineageID = fmt.Sprintf("lineage_%d", time.Now().UnixNano())
		log.Debugf("Generated new lineage_id: %s", lineageID)
	}
	var namespace, checkpointID string
	if ns, ok := initialState[CfgKeyCheckpointNS].(string); ok {
		namespace = ns
	}
	if id, ok := initialState[CfgKeyCheckpointID].(string); ok {
		checkpointID = id
		log.Debugf("Resuming from checkpoint_id: %s", checkpointID)
	}
	checkpointConfig := CreateCheckpointConfig(lineageID, checkpointID, namespace)
	log.Debugf(
		"Checkpoint config: lineage=%s, checkpoint_id=%s, namespace=%s",
		lineageID, checkpointID, namespace,
	)

	tuple, err := e.checkpointSaver.GetTuple(ctx, checkpointConfig)
	if err != nil || tuple == nil || tuple.Checkpoint == nil {
		log.Debug("No checkpoint found, starting fresh")
		execState := e.initializeState(initialState)
		e.initializeChannels(execState, true)
		return execState, checkpointConfig, false, 0, nil, nil
	}

	log.Debugf("Resuming from checkpoint ID=%s", tuple.Checkpoint.ID)
	restored := e.restoreStateFromCheckpoint(tuple)
	restored = e.mergeInitialStateNonInternal(restored, initialState)

	resumedStep := 0
	if tuple.Metadata != nil {
		resumedStep = tuple.Metadata.Step
		log.Debugf("Resuming from step %d", resumedStep)
	}
	lastCheckpoint := tuple.Checkpoint
	e.initializeChannels(restored, true)
	if tuple.Config != nil {
		checkpointConfig = tuple.Config
	}
	pending := tuple.PendingWrites
	log.Debugf(
		"Loaded checkpoint - PendingWrites=%d, NextNodes=%v, NextChannels=%v",
		len(pending), tuple.Checkpoint.NextNodes, tuple.Checkpoint.NextChannels,
	)
	e.applyExecutableNextNodes(restored, tuple)
	return restored, checkpointConfig, true, resumedStep, lastCheckpoint, pending
}

// restoreStateFromCheckpoint converts checkpoint channel values back into state.
// It mirrors the original inline logic: convert values to schema field types,
// then add any missing schema defaults or zero values so downstream nodes see
// consistent shapes, exactly as prior to refactor.
func (e *Executor) restoreStateFromCheckpoint(tuple *CheckpointTuple) State {
	restored := make(State)
	for k, v := range tuple.Checkpoint.ChannelValues {
		restored[k] = v
	}
	if e.graph.Schema() == nil {
		return restored
	}
	for key, value := range restored {
		if field, exists := e.graph.Schema().Fields[key]; exists {
			converted := e.restoreCheckpointValueWithSchema(value, field)
			if reflect.TypeOf(converted) != reflect.TypeOf(value) {
				restored[key] = converted
			}
		}
	}
	for key, field := range e.graph.Schema().Fields {
		if _, exists := restored[key]; !exists {
			if field.Default != nil {
				restored[key] = field.Default()
			} else {
				restored[key] = reflect.Zero(field.Type).Interface()
			}
		}
	}
	return restored
}

// mergeInitialStateNonInternal merges caller-provided initial values that are
// not internal (do not start with "_") and not already present in restored.
// This preserves the previous behavior where pre-populated inputs could be
// respected when resuming from checkpoints.
func (e *Executor) mergeInitialStateNonInternal(restored, initial State) State {
	for key, value := range initial {
		if _, exists := restored[key]; !exists && !strings.HasPrefix(key, "_") {
			restored[key] = value
		}
	}
	return restored
}

// applyExecutableNextNodes sets StateKeyNextNodes when suitable.
// This is important for initial checkpoints (step -1) that have the entry
// point set, so a forked resume can continue from the beginning.
func (e *Executor) applyExecutableNextNodes(restored State, tuple *CheckpointTuple) {
	if len(tuple.PendingWrites) != 0 || len(tuple.Checkpoint.NextNodes) == 0 {
		return
	}
	for _, nodeID := range tuple.Checkpoint.NextNodes {
		if nodeID != End && nodeID != "" {
			restored[StateKeyNextNodes] = tuple.Checkpoint.NextNodes
			return
		}
	}
}

// processResumeCommand applies resume-related fields from the initial state.
func (e *Executor) processResumeCommand(execState, initialState State) State {
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
	return execState
}

// buildExecutionContext constructs the execution context including versionsSeen.
func (e *Executor) buildExecutionContext(
	eventChan chan<- *event.Event,
	invocationID string,
	state State,
	resumed bool,
	lastCheckpoint *Checkpoint,
) *ExecutionContext {
	versionsSeen := make(map[string]map[string]int64)
	if resumed && lastCheckpoint != nil && lastCheckpoint.VersionsSeen != nil {
		for nodeID, nodeVersions := range lastCheckpoint.VersionsSeen {
			versionsSeen[nodeID] = make(map[string]int64)
			for ch, version := range nodeVersions {
				versionsSeen[nodeID][ch] = version
			}
		}
		log.Debugf(
			"Restored versionsSeen for %d nodes from checkpoint",
			len(versionsSeen),
		)
	}
	return &ExecutionContext{
		Graph:          e.graph,
		State:          state,
		EventChan:      eventChan,
		InvocationID:   invocationID,
		resumed:        resumed,
		versionsSeen:   versionsSeen,
		lastCheckpoint: lastCheckpoint,
	}
}

// runBspLoop runs the BSP execution loop from the given start step.
func (e *Executor) runBspLoop(
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
	checkpointConfig *map[string]any,
	startStep int,
) (int, error) {
	var stepsExecuted int
	for step := startStep; step < e.maxSteps; step++ {
		var stepCtx context.Context
		var stepCancel context.CancelFunc
		if e.stepTimeout > 0 {
			stepCtx, stepCancel = context.WithTimeout(ctx, e.stepTimeout)
		} else {
			stepCtx, stepCancel = context.WithCancel(ctx)
		}
		var tasks []*Task
		var err error
		if step == 0 && execCtx.resumed && startStep > 0 {
			tasks = e.planBasedOnChannelTriggers(execCtx, step)
		} else {
			tasks, err = e.planStep(ctx, invocation, execCtx, step)
		}
		if err != nil {
			stepCancel()
			return stepsExecuted, fmt.Errorf("planning failed at step %d: %w", step, err)
		}
		if len(tasks) == 0 {
			stepCancel()
			break
		}
		if err := e.executeStep(stepCtx, invocation, execCtx, tasks, step); err != nil {
			if interrupt, ok := GetInterruptError(err); ok {
				stepCancel()
				return stepsExecuted, e.handleInterrupt(stepCtx, invocation, execCtx, interrupt, step, *checkpointConfig)
			}
			stepCancel()
			return stepsExecuted, fmt.Errorf("execution failed at step %d: %w", step, err)
		}
		if err := e.updateChannels(stepCtx, invocation, execCtx, step); err != nil {
			stepCancel()
			return stepsExecuted, fmt.Errorf("update failed at step %d: %w", step, err)
		}
		if e.checkpointSaver != nil && *checkpointConfig != nil {
			log.Debugf("Creating checkpoint at step %d", step)
			if err := e.createCheckpointAndSave(
				ctx, checkpointConfig, execCtx.State, CheckpointSourceLoop, step, execCtx,
			); err != nil {
				log.Debugf("Failed to create checkpoint at step %d: %v", step, err)
			}
		}
		stepCancel()
		stepsExecuted++
	}
	return stepsExecuted, nil
}

// buildCompletionEvent prepares the completion event with a state snapshot.
func (e *Executor) buildCompletionEvent(
	execCtx *ExecutionContext,
	startTime time.Time,
	stepsExecuted int,
) *event.Event {
	// Take a deep snapshot of the final state under read lock.
	// IMPORTANT: Skip volatile/non-serializable keys (e.g., Session, callbacks, exec context)
	// to avoid racing on their internal maps/slices managed by other goroutines.
	execCtx.stateMutex.RLock()
	finalStateCopy := make(State, len(execCtx.State))
	for k, v := range execCtx.State {
		if isUnsafeStateKey(k) {
			continue
		}
		finalStateCopy[k] = deepCopyAny(v)
	}
	execCtx.stateMutex.RUnlock()
	completionEvent := NewGraphCompletionEvent(
		WithCompletionEventInvocationID(execCtx.InvocationID),
		WithCompletionEventFinalState(finalStateCopy),
		WithCompletionEventTotalSteps(stepsExecuted),
		WithCompletionEventTotalDuration(time.Since(startTime)),
	)
	if completionEvent.StateDelta == nil {
		completionEvent.StateDelta = make(map[string][]byte)
	}
	// Reuse the deep-copied snapshot to populate StateDelta to avoid an
	// additional full deep copy.
	for key, value := range finalStateCopy {
		if jsonData, err := json.Marshal(value); err == nil {
			completionEvent.StateDelta[key] = jsonData
		}
	}
	return completionEvent
}

// isUnsafeStateKey reports whether the key points to values that are
// non-serializable or potentially mutated concurrently by other subsystems
// (e.g., session service), which should be excluded from final snapshots.
func isUnsafeStateKey(key string) bool {
	switch key {
	case StateKeyExecContext,
		StateKeyParentAgent,
		StateKeyToolCallbacks,
		StateKeyModelCallbacks,
		StateKeyAgentCallbacks,
		StateKeyCurrentNodeID,
		StateKeySession:
		return true
	default:
		return false
	}
}

// createCheckpoint creates a checkpoint for the current state.
func (e *Executor) createCheckpoint(ctx context.Context, config map[string]any, state State, source string, step int) error {
	if e.checkpointSaver == nil {
		return nil
	}

	// Convert state to channel values with deep copy to avoid concurrent
	// mutations of nested maps/slices during checkpoint serialization.
	channelValues := make(map[string]any)
	for k, v := range state {
		if isUnsafeStateKey(k) {
			continue
		}
		channelValues[k] = deepCopyAny(v)
	}

	// Create channel versions (simple incrementing integers for now).
	channelVersions := make(map[string]int64)
	for k := range state {
		channelVersions[k] = 1 // This should be managed by the graph execution.
	}

	// Create versions seen (simplified for now).
	versionsSeen := make(map[string]map[string]int64)

	// Create checkpoint.
	checkpoint := NewCheckpoint(channelValues, channelVersions, versionsSeen)

	// Create metadata..
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
		// Checkpoint saver is nil.
		return nil
	}

	// Creating checkpoint from state.

	// IMPORTANT: Use the current state from execCtx which has all node updates,
	// not the state parameter which may be stale.
	stateCopy := make(State)
	execCtx.stateMutex.RLock()
	for k, v := range execCtx.State {
		stateCopy[k] = v
	}
	execCtx.stateMutex.RUnlock()

	// Create checkpoint object.
	checkpoint := e.createCheckpointFromState(stateCopy, step, execCtx)
	if checkpoint == nil {
		log.Debug("Failed to create checkpoint object")
		return fmt.Errorf("failed to create checkpoint")
	}

	// Set parent checkpoint ID from config if available.
	if parentCheckpointID := GetCheckpointID(*config); parentCheckpointID != "" {
		checkpoint.ParentCheckpointID = parentCheckpointID
		// Set parent checkpoint ID.
	}

	// Created checkpoint object.

	// Create metadata.
	metadata := &CheckpointMetadata{
		Source: source,
		Step:   step,
		Extra:  make(map[string]any),
	}

	// Get pending writes atomically.
	execCtx.pendingMu.Lock()
	pendingWrites := make([]PendingWrite, len(execCtx.pendingWrites))
	copy(pendingWrites, execCtx.pendingWrites)
	execCtx.pendingWrites = nil // Clear after copying.
	execCtx.pendingMu.Unlock()

	// Track new versions for channels that were updated.
	newVersions := make(map[string]int64)
	channels := e.graph.getAllChannels()
	for channelName, channel := range channels {
		if channel.IsAvailable() {
			newVersions[channelName] = channel.Version
		}
	}

	// Set channel versions in checkpoint for version semantics.
	checkpoint.ChannelVersions = newVersions

	// Set next nodes and channels for recovery.
	if source == CheckpointSourceInput && step == -1 {
		// For initial checkpoints, set the entry point as the next node.
		// This ensures that if someone forks and resumes from this checkpoint,
		// the workflow will start from the beginning.
		if entryPoint := e.graph.EntryPoint(); entryPoint != "" {
			checkpoint.NextNodes = []string{entryPoint}
			log.Debugf("Initial checkpoint - setting NextNodes to entry point: %v", checkpoint.NextNodes)
		}
		checkpoint.NextChannels = e.getNextChannels(execCtx.State)
	} else {
		checkpoint.NextNodes = e.getNextNodes(execCtx.State)
		checkpoint.NextChannels = e.getNextChannels(execCtx.State)
	}

	// Use PutFull for atomic storage.
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
	// Successfully saved checkpoint.
	// Clear step marks after checkpoint creation.
	e.clearChannelStepMarks()

	// Update external config with the new checkpoint_id.
	*config = updatedConfig
	// Updated config with new checkpoint ID.
	return nil
}

// applyPendingWrites replays pending writes into channels to rebuild frontier.
func (e *Executor) applyPendingWrites(ctx context.Context, invocation *agent.Invocation,
	execCtx *ExecutionContext, writes []PendingWrite) {
	if len(writes) == 0 {
		return
	}
	// Sort writes by sequence number for deterministic replay.
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
			e.emitChannelUpdateEvent(ctx, invocation, execCtx, w.Channel, ch.Behavior, e.getTriggeredNodes(w.Channel))
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
//
// This helper does not mutate Executor's fields. It only reads from the
// checkpoint store, reconstructs a state memory image, and primes channels
// by replaying pending writes (if any) using a temporary ExecutionContext.
// The caller is responsible for threading any returned checkpoint metadata
// into the per-execution context (ExecutionContext) as needed.
func (e *Executor) resumeFromCheckpoint(
	ctx context.Context,
	invocation *agent.Invocation,
	config map[string]any,
) (State, *Checkpoint, []PendingWrite, error) {
	log.Debugf("resumeFromCheckpoint: called with config keys: %v", getConfigKeys(config))

	if e.checkpointSaver == nil {
		// No checkpoint saver
		return nil, nil, nil, nil
	}

	tuple, err := e.checkpointSaver.GetTuple(ctx, config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to retrieve checkpoint: %w", err)
	}

	if tuple == nil {
		return nil, nil, nil, nil
	}

	// Note: lastCheckpoint is now carried per-execution in ExecutionContext.

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
		e.applyPendingWrites(ctx, invocation, tempExecCtx, tuple.PendingWrites)
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

	return state, tuple.Checkpoint, tuple.PendingWrites, nil
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
func (e *Executor) planStep(ctx context.Context, invocation *agent.Invocation,
	execCtx *ExecutionContext, step int) ([]*Task, error) {
	var tasks []*Task

	// Emit planning step event.
	planEvent := NewPregelStepEvent(
		WithPregelEventInvocationID(execCtx.InvocationID),
		WithPregelEventStepNumber(step),
		WithPregelEventPhase(PregelPhasePlanning),
		WithPregelEventTaskCount(0),
	)
	agent.EmitEvent(ctx, invocation, execCtx.EventChan, planEvent)

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
	if execCtx.resumed && execCtx.lastCheckpoint != nil {
		tasks = e.planBasedOnVersionTriggers(execCtx, step)
	} else {
		// Use traditional availability-based triggering
		tasks = e.planBasedOnAvailabilityTriggers(execCtx, step, triggerToNodes)
	}

	return tasks
}

// planBasedOnVersionTriggers creates tasks based on per-node version tracking.
func (e *Executor) planBasedOnVersionTriggers(execCtx *ExecutionContext, step int) []*Task {
	var tasks []*Task

	if execCtx.lastCheckpoint == nil {
		return tasks
	}

	channels := e.graph.getAllChannels()
	triggerToNodes := e.graph.getTriggerToNodes()

	// Track which nodes we've already scheduled to avoid duplicates.
	scheduledNodes := make(map[string]bool)

	// Check each available channel and determine which nodes should be triggered.
	for channelName, channel := range channels {
		if !channel.IsAvailable() {
			continue
		}

		currentVersion := int64(channel.Version)

		// Get nodes that are triggered by this channel.
		nodeIDs, exists := triggerToNodes[channelName]
		if !exists {
			continue
		}

		// Check each node to see if it should be triggered.
		for _, nodeID := range nodeIDs {
			// Skip if already scheduled.
			if scheduledNodes[nodeID] {
				continue
			}

			// Check if this node should be triggered based on version tracking.
			if e.shouldTriggerNode(nodeID, channelName, currentVersion, execCtx.lastCheckpoint) {
				task := e.createTask(nodeID, execCtx.State, step)
				if task != nil {
					tasks = append(tasks, task)
					scheduledNodes[nodeID] = true
					log.Debugf("Scheduled node %s for execution (triggered by channel %s)", nodeID, channelName)
				}
			}
		}
	}

	// Acknowledge all available channels after planning.
	for _, channel := range channels {
		if channel.IsAvailable() {
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

// executeStep executes all tasks concurrently.
func (e *Executor) executeStep(
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
	tasks []*Task,
	step int,
) error {
	// Emit execution step event.
	e.emitExecutionStepEvent(ctx, invocation, execCtx, tasks, step)
	// Execute tasks concurrently.
	var wg sync.WaitGroup
	results := make(chan error, len(tasks))

	for _, t := range tasks {
		wg.Add(1)
		go func(t *Task) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Errorf("panic executing task %s: %v\n%s", t.NodeID, r, string(debug.Stack()))
					results <- fmt.Errorf("task panic: %v", r)
				}
			}()
			if err := e.executeSingleTask(ctx, invocation, execCtx, t, step); err != nil {
				results <- err
			}
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
func (e *Executor) emitExecutionStepEvent(ctx context.Context, invocation *agent.Invocation,
	execCtx *ExecutionContext, tasks []*Task, step int) {
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
	agent.EmitEvent(ctx, invocation, execCtx.EventChan, execEvent)
}

// executeSingleTask executes a single task and handles all its events.
func (e *Executor) executeSingleTask(
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
	t *Task,
	step int,
) error {
	nodeCtx, nodeCancel := e.newNodeContext(ctx)
	defer nodeCancel()
	// Get node type and emit start event.
	nodeType := e.getNodeType(t.NodeID)
	nodeStart := time.Now()
	e.emitNodeStartEvent(ctx, invocation, execCtx, t.NodeID, nodeType, step, nodeStart)

	// Create callback context.
	callbackCtx := e.newNodeCallbackContext(execCtx, t.NodeID, nodeType, step, nodeStart)

	// Build per-task state copy.
	stateCopy := e.buildTaskStateCopy(execCtx, t)

	// Merge callbacks: global callbacks run first, then per-node callbacks.
	mergedCallbacks := e.getMergedCallbacks(stateCopy, t.NodeID)

	// Run before node callbacks.
	if handled, err := e.runBeforeCallbacks(
		ctx, invocation, mergedCallbacks, callbackCtx, stateCopy, execCtx, t,
		nodeType, nodeStart, step,
	); handled || err != nil {
		return err
	}

	// Ensure pre-callback state mutations are visible to the node function.
	// We pass the callback-mutated state copy as the task input so that
	// executeNodeFunction uses it (instead of rebuilding from the global state).
	// This preserves overlay application done in buildTaskStateCopy and respects
	// any in-place state changes made by before-node callbacks.
	t.Input = stateCopy

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
		e.emitNodeErrorEvent(ctx, invocation, execCtx, t.NodeID, nodeType, step, err)
		return fmt.Errorf("node %s execution failed: %w", t.NodeID, err)
	}

	// Run after node callbacks.
	if res, err := e.runAfterCallbacks(
		ctx, invocation, mergedCallbacks, callbackCtx, stateCopy, result,
		execCtx, t.NodeID, nodeType, step,
	); err != nil {
		return err
	} else if res != nil {
		result = res
	}

	// Handle result and process channel writes.
	if err := e.handleNodeResult(ctx, invocation, execCtx, t, result); err != nil {
		return err
	}

	// Update versions seen for this node after successful execution.
	e.updateVersionsSeen(execCtx, t.NodeID, t.Triggers)

	// Process conditional edges after node execution.
	if err := e.processConditionalEdges(ctx, invocation, execCtx, t.NodeID, step); err != nil {
		return fmt.Errorf("conditional edge processing failed for node %s: %w", t.NodeID, err)
	}

	// Emit node completion event.
	e.emitNodeCompleteEvent(ctx, invocation, execCtx, t.NodeID, nodeType, step, nodeStart)

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

// newNodeContext creates a context for a single node execution with timeout.
func (e *Executor) newNodeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if e.nodeTimeout > 0 {
		return context.WithTimeout(ctx, e.nodeTimeout)
	}
	return context.WithCancel(ctx)
}

// newNodeCallbackContext builds callback context for node lifecycle events.
func (e *Executor) newNodeCallbackContext(
	execCtx *ExecutionContext,
	nodeID string,
	nodeType NodeType,
	step int,
	start time.Time,
) *NodeCallbackContext {
	return &NodeCallbackContext{
		NodeID:             nodeID,
		NodeName:           e.getNodeName(nodeID),
		NodeType:           nodeType,
		StepNumber:         step,
		ExecutionStartTime: start,
		InvocationID:       execCtx.InvocationID,
		SessionID:          e.getSessionID(execCtx),
	}
}

// buildTaskStateCopy returns the per-task input state, including overlay.
func (e *Executor) buildTaskStateCopy(execCtx *ExecutionContext, t *Task) State {
	// Always construct an isolated state copy so node code can freely mutate
	// without racing with other goroutines. Skip or shallow-copy unsafe keys
	// whose internals may be mutated concurrently by other subsystems.
	execCtx.stateMutex.RLock()
	defer execCtx.stateMutex.RUnlock()

	var base State
	if t.Input != nil {
		if inputState, ok := t.Input.(State); ok {
			base = inputState
		}
	}
	if base == nil {
		base = execCtx.State
	}

	stateCopy := make(State, len(base))
	for k, v := range base {
		if isUnsafeStateKey(k) {
			// Preserve pointer/reference without deep copying to avoid racing
			// on nested structures (e.g., session internals) during reflection.
			stateCopy[k] = v
			continue
		}
		stateCopy[k] = deepCopyAny(v)
	}

	// Preserve callback pointers that contain function values which cannot be
	// deep-copied safely via reflection (functions would become nil and cause
	// panics when invoked). For these keys, reuse the original pointer from the
	// base state.
	for _, cbKey := range []string{
		StateKeyNodeCallbacks,
		StateKeyToolCallbacks,
		StateKeyModelCallbacks,
		StateKeyAgentCallbacks,
	} {
		if v, ok := base[cbKey]; ok && v != nil {
			stateCopy[cbKey] = v
		}
	}

	// Apply overlay if present to form the isolated input view.
	if t.Overlay != nil && e.graph.Schema() != nil {
		stateCopy = e.graph.Schema().ApplyUpdate(stateCopy, t.Overlay)
	}

	// Inject execution context helpers used by nodes.
	stateCopy[StateKeyExecContext] = execCtx
	stateCopy[StateKeyCurrentNodeID] = t.NodeID

	return stateCopy
}

// getMergedCallbacks merges global and per-node callbacks for a node.
func (e *Executor) getMergedCallbacks(stateCopy State, nodeID string) *NodeCallbacks {
	globalCallbacks, _ := stateCopy[StateKeyNodeCallbacks].(*NodeCallbacks)
	node, exists := e.graph.Node(nodeID)
	var perNodeCallbacks *NodeCallbacks
	if exists {
		perNodeCallbacks = node.callbacks
	}
	return e.mergeNodeCallbacks(globalCallbacks, perNodeCallbacks)
}

// runBeforeCallbacks executes before-node callbacks and handles early result.
func (e *Executor) runBeforeCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	callbacks *NodeCallbacks,
	cbCtx *NodeCallbackContext,
	stateCopy State,
	execCtx *ExecutionContext,
	t *Task,
	nodeType NodeType,
	nodeStart time.Time,
	step int,
) (bool, error) {
	if callbacks == nil {
		return false, nil
	}
	customResult, err := callbacks.RunBeforeNode(ctx, cbCtx, stateCopy)
	if err != nil {
		e.emitNodeErrorEvent(ctx, invocation, execCtx, t.NodeID, nodeType, step, err)
		return true, fmt.Errorf("before node callback failed for node %s: %w", t.NodeID, err)
	}
	if customResult == nil {
		return false, nil
	}
	if err := e.handleNodeResult(ctx, invocation, execCtx, t, customResult); err != nil {
		return true, err
	}
	if err := e.processConditionalEdges(ctx, invocation, execCtx, t.NodeID, step); err != nil {
		return true, fmt.Errorf("conditional edge processing failed for node %s: %w", t.NodeID, err)
	}
	e.emitNodeCompleteEvent(ctx, invocation, execCtx, t.NodeID, nodeType, step, nodeStart)
	return true, nil
}

// runAfterCallbacks executes after-node callbacks and returns an override.
func (e *Executor) runAfterCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	callbacks *NodeCallbacks,
	cbCtx *NodeCallbackContext,
	stateCopy State,
	result any,
	execCtx *ExecutionContext,
	nodeID string,
	nodeType NodeType,
	step int,
) (any, error) {
	if callbacks == nil {
		return nil, nil
	}
	customResult, err := callbacks.RunAfterNode(ctx, cbCtx, stateCopy, result, nil)
	if err != nil {
		e.emitNodeErrorEvent(ctx, invocation, execCtx, nodeID, nodeType, step, err)
		return nil, fmt.Errorf("after node callback failed for node %s: %w", nodeID, err)
	}
	return customResult, nil
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

	// Add global callbacks first for Before and OnNodeError.
	merged.BeforeNode = append(merged.BeforeNode, global.BeforeNode...)
	merged.OnNodeError = append(merged.OnNodeError, global.OnNodeError...)

	// For per-node callbacks, Before callbacks execute after global.
	merged.BeforeNode = append(merged.BeforeNode, perNode.BeforeNode...)
	merged.OnNodeError = append(merged.OnNodeError, perNode.OnNodeError...)

	// For After callbacks, execute per-node first, then global, so per-node can
	// shape/override the result before global observers run.
	merged.AfterNode = append(merged.AfterNode, perNode.AfterNode...)
	merged.AfterNode = append(merged.AfterNode, global.AfterNode...)

	return merged
}

// emitNodeStartEvent emits the node start event.
func (e *Executor) emitNodeStartEvent(
	ctx context.Context,
	invocation *agent.Invocation,
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
	agent.EmitEvent(ctx, invocation, execCtx.EventChan, startEvent)
}

// executeNodeFunction executes the actual node function.
func (e *Executor) executeNodeFunction(
	ctx context.Context, execCtx *ExecutionContext, t *Task,
) (res any, err error) {
	// Recover from panics in user-provided node functions to prevent
	// the whole service from crashing. Convert to error so the normal
	// error handling path (callbacks, events, checkpointing) can run.
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Errorf("panic in node %s: %v\n%s", t.NodeID, r, string(stack))
			err = fmt.Errorf("node %s panic: %v", t.NodeID, r)
			res = nil
		}
	}()
	nodeID := t.NodeID
	node, exists := e.graph.Node(nodeID)
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}

	// Prefer the prebuilt task input which is already a deep copy created by
	// buildTaskStateCopy. If missing (e.g., legacy paths), deep-copy the
	// current global state here as a fallback.
	var input State
	if t.Input != nil {
		if s, ok := t.Input.(State); ok {
			input = s
		}
	}

	if input == nil {
		execCtx.stateMutex.RLock()
		tmp := make(State, len(execCtx.State))
		for k, v := range execCtx.State {
			if isUnsafeStateKey(k) {
				tmp[k] = v
				continue
			}
			tmp[k] = deepCopyAny(v)
		}
		// Apply overlay if present to form the isolated input view.
		if t.Overlay != nil && e.graph.Schema() != nil {
			tmp = e.graph.Schema().ApplyUpdate(tmp, t.Overlay)
		}
		execCtx.stateMutex.RUnlock()
		// Inject execution context helpers used by nodes.
		tmp[StateKeyExecContext] = execCtx
		tmp[StateKeyCurrentNodeID] = nodeID
		input = tmp
	}
	input[StateKeyToolCallbacks] = node.toolCallbacks

	return node.Function(ctx, input)
}

// emitNodeErrorEvent emits the node error event.
func (e *Executor) emitNodeErrorEvent(
	ctx context.Context,
	invocation *agent.Invocation,
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
	agent.EmitEvent(ctx, invocation, execCtx.EventChan, errorEvent)
}

// handleNodeResult handles the result from node execution.
func (e *Executor) handleNodeResult(ctx context.Context, invocation *agent.Invocation,
	execCtx *ExecutionContext, t *Task, result any) error {
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
			if err := e.handleCommandResult(ctx, invocation, execCtx, v); err != nil {
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
		e.processChannelWrites(ctx, invocation, execCtx, t.TaskID, t.Writes)
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

		// Resolve writers/triggers from the target node rather than the source task.
		var targetWriters []channelWriteEntry
		var targetTriggers []string
		if node, exists := e.graph.Node(target); exists && node != nil {
			targetWriters = node.writers
			targetTriggers = node.triggers
		}

		// Create task with merged state and target node channel config.
		newTask := &Task{
			NodeID:   target,
			Input:    mergedState,
			Writes:   targetWriters,
			Triggers: targetTriggers,
			TaskID:   fmt.Sprintf("%s-%d", target, nextStep),
			TaskPath: append([]string{}, t.TaskPath...),
			Overlay:  nil,
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

	// Use schema-based reducers when available for proper merging.
	if e.graph != nil && e.graph.Schema() != nil {
		execCtx.State = e.graph.Schema().ApplyUpdate(execCtx.State, stateResult)
		return
	}
	// Fallback to direct assignment if no schema available.
	maps.Copy(execCtx.State, stateResult)
}

// handleCommandResult handles a Command result from node execution.
func (e *Executor) handleCommandResult(ctx context.Context, invocation *agent.Invocation,
	execCtx *ExecutionContext, cmdResult *Command) error {
	// Update state with command updates.
	if cmdResult.Update != nil {
		e.updateStateFromResult(execCtx, cmdResult.Update)
	}

	// Handle GoTo routing.
	if cmdResult.GoTo != "" {
		e.handleCommandRouting(ctx, invocation, execCtx, cmdResult.GoTo)
	}

	return nil
}

// handleCommandRouting handles the routing specified by a Command.
func (e *Executor) handleCommandRouting(ctx context.Context, invocation *agent.Invocation,
	execCtx *ExecutionContext, targetNode string) {
	// Create trigger channel for the target node (including self).
	triggerChannel := fmt.Sprintf("%s%s", ChannelTriggerPrefix, targetNode)
	e.graph.addNodeTrigger(triggerChannel, targetNode)

	// Write to the channel to trigger the target node.
	ch, _ := e.graph.getChannel(triggerChannel)
	if ch != nil {
		ch.Update([]any{channelUpdateMarker}, -1)
	}

	// Emit channel update event.
	e.emitChannelUpdateEvent(ctx, invocation, execCtx, triggerChannel, channel.BehaviorLastValue, []string{targetNode})
}

// processChannelWrites processes the channel writes for a task.
func (e *Executor) processChannelWrites(ctx context.Context, invocation *agent.Invocation,
	execCtx *ExecutionContext, taskID string, writes []channelWriteEntry) {
	for _, write := range writes {
		ch, _ := e.graph.getChannel(write.Channel)
		if ch != nil {
			ch.Update([]any{write.Value}, -1)

			// Emit channel update event.
			e.emitChannelUpdateEvent(ctx, invocation, execCtx, write.Channel, ch.Behavior, e.getTriggeredNodes(write.Channel))
			// Accumulate into pendingWrites to be saved with the next checkpoint.
			execCtx.pendingMu.Lock()
			execCtx.pendingWrites = append(execCtx.pendingWrites, PendingWrite{
				Channel:  write.Channel,
				Value:    write.Value,
				TaskID:   taskID,
				Sequence: execCtx.seq.Add(1), // Use atomic increment for deterministic replay
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
	ctx context.Context,
	invocation *agent.Invocation,
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
	agent.EmitEvent(ctx, invocation, execCtx.EventChan, channelEvent)
}

// emitNodeCompleteEvent emits the node completion event.
func (e *Executor) emitNodeCompleteEvent(
	ctx context.Context,
	invocation *agent.Invocation,
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
	agent.EmitEvent(ctx, invocation, execCtx.EventChan, completeEvent)
}

// updateChannels processes channel updates and emits events.
func (e *Executor) updateChannels(ctx context.Context, invocation *agent.Invocation,
	execCtx *ExecutionContext, step int) error {
	e.emitUpdateStepEvent(ctx, invocation, execCtx, step)
	e.emitStateUpdateEvent(ctx, invocation, execCtx)
	return nil
}

// emitUpdateStepEvent emits the update step event.
func (e *Executor) emitUpdateStepEvent(ctx context.Context, invocation *agent.Invocation, execCtx *ExecutionContext, step int) {
	updatedChannels := e.getUpdatedChannels()
	updateEvent := NewPregelStepEvent(
		WithPregelEventInvocationID(execCtx.InvocationID),
		WithPregelEventStepNumber(step),
		WithPregelEventPhase(PregelPhaseUpdate),
		WithPregelEventTaskCount(len(updatedChannels)),
		WithPregelEventUpdatedChannels(updatedChannels),
	)
	agent.EmitEvent(ctx, invocation, execCtx.EventChan, updateEvent)
}

// emitStateUpdateEvent emits the state update event.
func (e *Executor) emitStateUpdateEvent(ctx context.Context, invocation *agent.Invocation, execCtx *ExecutionContext) {
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
	agent.EmitEvent(ctx, invocation, execCtx.EventChan, stateEvent)
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
	invocation *agent.Invocation,
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
	return e.processConditionalResult(ctx, invocation, execCtx, condEdge, result, step)
}

// processConditionalResult processes the result of a conditional edge evaluation.
func (e *Executor) processConditionalResult(
	ctx context.Context,
	invocation *agent.Invocation,
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
	channelName := fmt.Sprintf("%s%s", ChannelBranchPrefix, target)
	e.graph.addChannel(channelName, channel.BehaviorLastValue)
	e.graph.addNodeTrigger(channelName, target)

	// Trigger the target by writing to the channel.
	ch, ok := e.graph.getChannel(channelName)
	if ok && ch != nil {
		ch.Update([]any{channelUpdateMarker}, -1)
		e.emitChannelUpdateEvent(ctx, invocation, execCtx, channelName,
			channel.BehaviorLastValue, []string{target})
	} else {
		log.Warnf("❌ Step %d: Failed to get channel %s", step, channelName)
	}
	return nil
}

// handleInterrupt handles an interrupt during graph execution.
func (e *Executor) handleInterrupt(
	ctx context.Context,
	invocation *agent.Invocation,
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
		checkpoint := e.createCheckpointFromState(currentState, step, execCtx)

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

	agent.EmitEvent(ctx, invocation, execCtx.EventChan, interruptEvent)

	// Return the interrupt error to propagate it to the caller.
	return interrupt
}

// createCheckpointFromState creates a checkpoint from the current execution state.
func (e *Executor) createCheckpointFromState(state State, step int, execCtx *ExecutionContext) *Checkpoint {
	// Convert state to channel values, ensuring we capture the latest state
	// including any updates from nodes that haven't been written to channels yet.
	channelValues := make(map[string]any)
	for k, v := range state {
		if isUnsafeStateKey(k) {
			continue
		}
		channelValues[k] = deepCopyAny(v)
	}

	// Create channel versions from current channel states
	channelVersions := make(map[string]int64)
	channels := e.graph.getAllChannels()
	for channelName, channel := range channels {
		if channel.IsAvailable() {
			channelVersions[channelName] = channel.Version
		}
	}

	// Create versions seen from execution context.
	versionsSeen := make(map[string]map[string]int64)
	if execCtx != nil {
		execCtx.versionsSeenMu.RLock()
		for nodeID, nodeVersions := range execCtx.versionsSeen {
			versionsSeen[nodeID] = make(map[string]int64)
			for channel, version := range nodeVersions {
				versionsSeen[nodeID][channel] = version
			}
		}
		execCtx.versionsSeenMu.RUnlock()
	}

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

// updateVersionsSeen updates the versions seen by a node after task execution.
func (e *Executor) updateVersionsSeen(execCtx *ExecutionContext, nodeID string, triggers []string) {
	execCtx.versionsSeenMu.Lock()
	defer execCtx.versionsSeenMu.Unlock()

	// Initialize map for node if needed.
	if execCtx.versionsSeen[nodeID] == nil {
		execCtx.versionsSeen[nodeID] = make(map[string]int64)
	}

	// Record current version of all trigger channels this node has seen.
	channels := e.graph.getAllChannels()
	for _, trigger := range triggers {
		if channel, exists := channels[trigger]; exists {
			execCtx.versionsSeen[nodeID][trigger] = channel.Version
			log.Debugf("Node %s saw channel %s version %d", nodeID, trigger, channel.Version)
		}
	}
}

// shouldTriggerNode checks if a node should be triggered based on version tracking.
func (e *Executor) shouldTriggerNode(
	nodeID string,
	channelName string,
	currentVersion int64,
	lastCheckpoint *Checkpoint,
) bool {
	if lastCheckpoint == nil || lastCheckpoint.VersionsSeen == nil {
		// No checkpoint or no version tracking - should trigger.
		return true
	}

	// Get what this node has seen before.
	nodeVersions, nodeExists := lastCheckpoint.VersionsSeen[nodeID]
	if !nodeExists {
		// Node has never run - should trigger.
		log.Debugf("Node %s has never run, triggering", nodeID)
		return true
	}

	// Check if node has seen this channel version.
	seenVersion, channelSeen := nodeVersions[channelName]
	if !channelSeen {
		// Node hasn't seen this channel before - should trigger.
		log.Debugf("Node %s hasn't seen channel %s before, triggering", nodeID, channelName)
		return true
	}

	// Only trigger if channel has newer version than what node has seen.
	shouldTrigger := currentVersion > seenVersion
	if shouldTrigger {
		log.Debugf("Node %s should trigger: channel %s version %d > seen %d",
			nodeID, channelName, currentVersion, seenVersion)
	} else {
		log.Debugf("Node %s already saw channel %s version %d", nodeID, channelName, currentVersion)
	}
	return shouldTrigger
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
