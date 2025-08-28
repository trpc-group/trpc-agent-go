//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	defaultChannelBufferSize = 256
	defaultMaxSteps          = 100
	defaultStepTimeout       = 30 * time.Second
)

// Executor executes a graph with the given initial state using Pregel-style BSP execution.
type Executor struct {
	graph             *Graph
	channelBufferSize int
	maxSteps          int
	stepTimeout       time.Duration
}

// ExecutorOption is a function that configures an Executor.
type ExecutorOption func(*ExecutorOptions)

// ExecutorOptions contains configuration options for creating an Executor.
type ExecutorOptions struct {
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// MaxSteps is the maximum number of steps for graph execution.
	MaxSteps int
	// StepTimeout is the timeout for each step (default: 30s).
	StepTimeout time.Duration
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

// NewExecutor creates a new graph executor.
func NewExecutor(graph *Graph, opts ...ExecutorOption) (*Executor, error) {
	if err := graph.validate(); err != nil {
		return nil, fmt.Errorf("invalid graph: %w", err)
	}
	var options ExecutorOptions
	options.ChannelBufferSize = defaultChannelBufferSize // Default buffer size.
	options.MaxSteps = defaultMaxSteps                   // Default max steps.
	options.StepTimeout = defaultStepTimeout             // Default step timeout.
	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}
	return &Executor{
		graph:             graph,
		channelBufferSize: options.ChannelBufferSize,
		maxSteps:          options.MaxSteps,
		stepTimeout:       options.StepTimeout,
	}, nil
}

// Task represents a task to be executed in a step.
type Task struct {
	NodeID   string
	Input    any
	Writes   []channelWriteEntry
	Triggers []string
	TaskID   string
	TaskPath []string
}

// Step represents a single step in execution.
type Step struct {
	StepNumber      int
	Tasks           []*Task
	State           State
	UpdatedChannels map[string]bool
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
			// Emit error event.
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
	// Initialize channels with state keys.
	e.initializeChannels(initialState)

	// Initialize state with schema defaults.
	execState := e.initializeState(initialState)

	// Create execution context.
	execCtx := &ExecutionContext{
		Graph:        e.graph,
		State:        execState,
		EventChan:    eventChan,
		InvocationID: invocation.InvocationID,
	}

	// BSP execution loop.
	for step := 0; step < e.maxSteps; step++ {
		// Plan phase: determine which nodes to execute.
		tasks, err := e.planStep(execCtx, step)
		if err != nil {
			return fmt.Errorf("planning failed at step %d: %w", step, err)
		}

		if len(tasks) == 0 {
			break
		}
		// Execute phase: run all tasks concurrently.
		if err := e.executeStep(ctx, execCtx, tasks, step); err != nil {
			return fmt.Errorf("execution failed at step %d: %w", step, err)
		}
		// Update phase: process channel updates.
		if err := e.updateChannels(ctx, execCtx, step); err != nil {
			return fmt.Errorf("update failed at step %d: %w", step, err)
		}
	}
	// Emit completion event.
	completionEvent := NewGraphCompletionEvent(
		WithCompletionEventInvocationID(execCtx.InvocationID),
		WithCompletionEventFinalState(execCtx.State),
		WithCompletionEventTotalSteps(e.maxSteps),
		WithCompletionEventTotalDuration(time.Since(startTime)),
	)

	// Add final state to StateDelta for test access.
	if completionEvent.StateDelta == nil {
		completionEvent.StateDelta = make(map[string][]byte)
	}
	// Snapshot the state under read lock to avoid concurrent map iteration
	// while other goroutines may still append metadata.
	execCtx.stateMutex.RLock()
	stateSnapshot := make(State, len(execCtx.State))
	for key, value := range execCtx.State {
		stateSnapshot[key] = value
	}
	execCtx.stateMutex.RUnlock()
	for key, value := range stateSnapshot {
		if jsonData, err := json.Marshal(value); err == nil {
			completionEvent.StateDelta[key] = jsonData
		}
	}
	select {
	case eventChan <- completionEvent:
	default:
	}
	return nil
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
func (e *Executor) initializeChannels(state State) {
	// Create input channels for each state key.
	for key := range state {
		channelName := fmt.Sprintf("input:%s", key)
		e.graph.addChannel(channelName, channel.BehaviorLastValue)

		channel, _ := e.graph.getChannel(channelName)
		if channel != nil {
			channel.Update([]any{state[key]})
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

	// Check if this is the first step (entry point).
	if step == 0 {
		entryPoint := e.graph.EntryPoint()
		if entryPoint == "" {
			return nil, errors.New("no entry point defined")
		}

		task := e.createTask(entryPoint, execCtx.State, step)
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
	// Get node type and emit start event.
	nodeType := e.getNodeType(t.NodeID)
	execStartTime := time.Now()
	e.emitNodeStartEvent(execCtx, t.NodeID, nodeType, step, execStartTime)

	// Create callback context.
	callbackCtx := &NodeCallbackContext{
		NodeID:             t.NodeID,
		NodeName:           e.getNodeName(t.NodeID),
		NodeType:           nodeType,
		StepNumber:         step,
		ExecutionStartTime: execStartTime,
		InvocationID:       execCtx.InvocationID,
		SessionID:          e.getSessionID(execCtx),
	}

	// Get state copy for callbacks.
	execCtx.stateMutex.RLock()
	stateCopy := make(State, len(execCtx.State))
	for k, v := range execCtx.State {
		stateCopy[k] = v
	}
	// Add execution context to state so nodes can access event channel.
	stateCopy[StateKeyExecContext] = execCtx
	execCtx.stateMutex.RUnlock()

	// Add current node ID to state so nodes can access it.
	stateCopy[StateKeyCurrentNodeID] = t.NodeID

	// Run before node callbacks.
	nodeCallbacks, _ := stateCopy[StateKeyNodeCallbacks].(*NodeCallbacks)
	if nodeCallbacks != nil {
		customResult, err := nodeCallbacks.RunBeforeNode(ctx, callbackCtx, stateCopy)
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
			e.emitNodeCompleteEvent(execCtx, t.NodeID, nodeType, step, execStartTime)
			return nil
		}
	}

	// Execute the node function.
	result, err := e.executeNodeFunction(ctx, execCtx, t.NodeID)
	if err != nil {
		// Run on node error callbacks.
		if nodeCallbacks != nil {
			nodeCallbacks.RunOnNodeError(ctx, callbackCtx, stateCopy, err)
		}
		e.emitNodeErrorEvent(execCtx, t.NodeID, nodeType, step, err)
		return fmt.Errorf("node %s execution failed: %w", t.NodeID, err)
	}

	// Run after node callbacks.
	if nodeCallbacks != nil {
		customResult, err := nodeCallbacks.RunAfterNode(ctx, callbackCtx, stateCopy, result, nil)
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
	e.emitNodeCompleteEvent(execCtx, t.NodeID, nodeType, step, execStartTime)

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
	ctx context.Context, execCtx *ExecutionContext, nodeID string,
) (any, error) {
	node, exists := e.graph.Node(nodeID)
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}
	// Execute the node with read lock on state.
	execCtx.stateMutex.RLock()
	stateCopy := make(State, len(execCtx.State))
	for k, v := range execCtx.State {
		stateCopy[k] = v
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

	// Update state with node result if it's a State.
	if stateResult, ok := result.(State); ok {
		e.updateStateFromResult(execCtx, stateResult)
	} else if cmdResult, ok := result.(*Command); ok && cmdResult != nil {
		if err := e.handleCommandResult(ctx, execCtx, cmdResult); err != nil {
			return err
		}
	}

	// Process channel writes.
	if len(t.Writes) > 0 {
		e.processChannelWrites(execCtx, t.Writes)
	}

	return nil
}

// updateStateFromResult updates the execution context state from a State result.
func (e *Executor) updateStateFromResult(execCtx *ExecutionContext, stateResult State) {
	execCtx.stateMutex.Lock()
	// Use schema reducers when available to preserve history and merge correctly.
	if e.graph.Schema() != nil {
		execCtx.State = e.graph.Schema().ApplyUpdate(execCtx.State, stateResult)
	} else {
		for key, value := range stateResult {
			execCtx.State[key] = value
		}
	}
	execCtx.stateMutex.Unlock()
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
		ch.Update([]any{channelUpdateMarker})
	}

	// Emit channel update event.
	e.emitChannelUpdateEvent(execCtx, triggerChannel, channel.BehaviorLastValue, []string{targetNode})
}

// processChannelWrites processes the channel writes for a task.
func (e *Executor) processChannelWrites(execCtx *ExecutionContext, writes []channelWriteEntry) {
	for _, write := range writes {
		ch, _ := e.graph.getChannel(write.Channel)
		if ch != nil {
			ch.Update([]any{write.Value})

			// Emit channel update event.
			e.emitChannelUpdateEvent(execCtx, write.Channel, ch.Behavior, e.getTriggeredNodes(write.Channel))
		}
	}
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
	execCtx.stateMutex.RUnlock()

	stateEvent := NewStateUpdateEvent(
		WithStateEventInvocationID(execCtx.InvocationID),
		WithStateEventUpdatedKeys(stateKeys),
		WithStateEventStateSize(len(execCtx.State)),
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
	result, err := condEdge.Condition(ctx, execCtx.State)
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
		ch.Update([]any{channelUpdateMarker})
		e.emitChannelUpdateEvent(execCtx, channelName, channel.BehaviorLastValue, []string{target})
	} else {
		log.Warnf("❌ Step %d: Failed to get channel %s", step, channelName)
	}
	return nil
}
