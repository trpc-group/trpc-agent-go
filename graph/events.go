//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

// Package graph provides graph-based workflow execution.
package graph

import (
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Event authors for graph-related events.
const (
	// AuthorGraphNode is the author for individual node execution events.
	AuthorGraphNode = "graph-node"
	// AuthorGraphPregel is the author for Pregel-specific events.
	AuthorGraphPregel = "graph-pregel"
)

// Event object types for graph-related events.
const (
	// ObjectTypeGraphExecution is the object type for graph execution events.
	ObjectTypeGraphExecution = "graph.execution"
	// ObjectTypeGraphNodeExecution is the object type for node execution events.
	ObjectTypeGraphNodeExecution = "graph.node.execution"
	// ObjectTypeGraphNodeStart is the object type for node start events.
	ObjectTypeGraphNodeStart = "graph.node.start"
	// ObjectTypeGraphNodeComplete is the object type for node completion events.
	ObjectTypeGraphNodeComplete = "graph.node.complete"
	// ObjectTypeGraphNodeError is the object type for node error events.
	ObjectTypeGraphNodeError = "graph.node.error"
	// ObjectTypeGraphPregelStep is the object type for Pregel step events.
	ObjectTypeGraphPregelStep = "graph.pregel.step"
	// ObjectTypeGraphPregelPlanning is the object type for Pregel planning events.
	ObjectTypeGraphPregelPlanning = "graph.pregel.planning"
	// ObjectTypeGraphPregelExecution is the object type for Pregel execution events.
	ObjectTypeGraphPregelExecution = "graph.pregel.execution"
	// ObjectTypeGraphPregelUpdate is the object type for Pregel update events.
	ObjectTypeGraphPregelUpdate = "graph.pregel.update"
	// ObjectTypeGraphChannelUpdate is the object type for channel update events.
	ObjectTypeGraphChannelUpdate = "graph.channel.update"
	// ObjectTypeGraphStateUpdate is the object type for state update events.
	ObjectTypeGraphStateUpdate = "graph.state.update"
)

// Metadata keys for storing event metadata in StateDelta.
const (
	// MetadataKeyNode is the key for node execution metadata.
	MetadataKeyNode = "_node_metadata"
	// MetadataKeyPregel is the key for Pregel step metadata.
	MetadataKeyPregel = "_pregel_metadata"
	// MetadataKeyChannel is the key for channel update metadata.
	MetadataKeyChannel = "_channel_metadata"
	// MetadataKeyState is the key for state update metadata.
	MetadataKeyState = "_state_metadata"
	// MetadataKeyCompletion is the key for completion metadata.
	MetadataKeyCompletion = "_completion_metadata"
)

// NodeType represents the type of a graph node.
type NodeType string

// Node type constants.
const (
	NodeTypeFunction NodeType = "function"
	NodeTypeLLM      NodeType = "llm"
	NodeTypeTool     NodeType = "tool"
	NodeTypeJoin     NodeType = "join"
	NodeTypeRouter   NodeType = "router"
)

// String returns the string representation of the node type.
func (nt NodeType) String() string {
	return string(nt)
}

// ExecutionPhase represents the phase of node execution.
type ExecutionPhase string

// Execution phase constants.
const (
	ExecutionPhaseStart    ExecutionPhase = "start"
	ExecutionPhaseComplete ExecutionPhase = "complete"
	ExecutionPhaseError    ExecutionPhase = "error"
)

// String returns the string representation of the execution phase.
func (ep ExecutionPhase) String() string {
	return string(ep)
}

// PregelPhase represents the phase of Pregel execution.
type PregelPhase string

// Pregel phase constants.
const (
	PregelPhasePlanning  PregelPhase = "planning"
	PregelPhaseExecution PregelPhase = "execution"
	PregelPhaseUpdate    PregelPhase = "update"
	PregelPhaseComplete  PregelPhase = "complete"
	PregelPhaseError     PregelPhase = "error"
)

// String returns the string representation of the Pregel phase.
func (pp PregelPhase) String() string {
	return string(pp)
}

// NodeExecutionMetadata contains metadata about node execution.
type NodeExecutionMetadata struct {
	// NodeID is the unique identifier of the node.
	NodeID string `json:"nodeId"`
	// NodeType is the type of the node.
	NodeType NodeType `json:"nodeType"`
	// Phase is the execution phase.
	Phase ExecutionPhase `json:"phase"`
	// StartTime is when the execution started.
	StartTime time.Time `json:"startTime,omitempty"`
	// EndTime is when the execution completed.
	EndTime time.Time `json:"endTime,omitempty"`
	// Duration is the execution duration.
	Duration time.Duration `json:"duration,omitempty"`
	// InputKeys are the keys of input state.
	InputKeys []string `json:"inputKeys,omitempty"`
	// OutputKeys are the keys of output state.
	OutputKeys []string `json:"outputKeys,omitempty"`
	// Error is the error message if execution failed.
	Error string `json:"error,omitempty"`
	// ToolCalls contains tool call information for tool nodes.
	ToolCalls []model.ToolCall `json:"toolCalls,omitempty"`
	// ModelName contains the model name for LLM nodes.
	ModelName string `json:"modelName,omitempty"`
	// StepNumber is the Pregel step number.
	StepNumber int `json:"stepNumber,omitempty"`
}

// PregelStepMetadata contains metadata about Pregel step execution.
type PregelStepMetadata struct {
	// StepNumber is the step number.
	StepNumber int `json:"stepNumber"`
	// Phase is the Pregel phase.
	Phase PregelPhase `json:"phase"`
	// TaskCount is the number of tasks in this step.
	TaskCount int `json:"taskCount"`
	// UpdatedChannels are the channels updated in this step.
	UpdatedChannels []string `json:"updatedChannels,omitempty"`
	// ActiveNodes are the nodes active in this step.
	ActiveNodes []string `json:"activeNodes,omitempty"`
	// StartTime is when the step started.
	StartTime time.Time `json:"startTime,omitempty"`
	// EndTime is when the step completed.
	EndTime time.Time `json:"endTime,omitempty"`
	// Duration is the step duration.
	Duration time.Duration `json:"duration,omitempty"`
	// Error is the error message if step failed.
	Error string `json:"error,omitempty"`
}

// ChannelUpdateMetadata contains metadata about channel updates.
type ChannelUpdateMetadata struct {
	// ChannelName is the name of the channel.
	ChannelName string `json:"channelName"`
	// ChannelType is the type of the channel.
	ChannelType channel.Type `json:"channelType"`
	// ValueCount is the number of values in the channel.
	ValueCount int `json:"valueCount"`
	// Available indicates if the channel is available.
	Available bool `json:"available"`
	// TriggeredNodes are the nodes triggered by this channel.
	TriggeredNodes []string `json:"triggeredNodes,omitempty"`
}

// StateUpdateMetadata contains metadata about state updates.
type StateUpdateMetadata struct {
	// UpdatedKeys are the keys that were updated.
	UpdatedKeys []string `json:"updatedKeys"`
	// RemovedKeys are the keys that were removed.
	RemovedKeys []string `json:"removedKeys,omitempty"`
	// StateSize is the total size of the state.
	StateSize int `json:"stateSize"`
}

// JSONMetadata represents the JSON structure for metadata stored in StateDelta.
type JSONMetadata struct {
	// Node metadata for node execution events.
	Node *NodeExecutionMetadata `json:"node,omitempty"`
	// Pregel metadata for Pregel step events.
	Pregel *PregelStepMetadata `json:"pregel,omitempty"`
	// Channel metadata for channel update events.
	Channel *ChannelUpdateMetadata `json:"channel,omitempty"`
	// State metadata for state update events.
	State *StateUpdateMetadata `json:"state,omitempty"`
	// Completion metadata for completion events.
	Completion *CompletionMetadata `json:"completion,omitempty"`
}

// CompletionMetadata contains metadata about graph completion.
type CompletionMetadata struct {
	// TotalSteps is the total number of steps executed.
	TotalSteps int `json:"totalSteps"`
	// TotalDuration is the total execution duration.
	TotalDuration time.Duration `json:"totalDuration"`
	// FinalStateKeys is the number of keys in the final state.
	FinalStateKeys int `json:"finalStateKeys"`
}

// EventOption is a function that configures a graph event.
type EventOption func(*event.Event)

// WithNodeMetadata adds node execution metadata to the event.
func WithNodeMetadata(metadata NodeExecutionMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyNode] = jsonData
		}
	}
}

// WithPregelMetadata adds Pregel step metadata to the event.
func WithPregelMetadata(metadata PregelStepMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyPregel] = jsonData
		}
	}
}

// WithChannelMetadata adds channel update metadata to the event.
func WithChannelMetadata(metadata ChannelUpdateMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyChannel] = jsonData
		}
	}
}

// WithStateMetadata adds state update metadata to the event.
func WithStateMetadata(metadata StateUpdateMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyState] = jsonData
		}
	}
}

// NewGraphEvent creates a new graph-related event.
func NewGraphEvent(invocationID, author, objectType string, opts ...EventOption) *event.Event {
	e := event.New(invocationID, author, event.WithObject(objectType))
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// NodeEventOptions contains options for creating node events.
type NodeEventOptions struct {
	InvocationID string
	NodeID       string
	NodeType     NodeType
	StepNumber   int
	StartTime    time.Time
	EndTime      time.Time
	InputKeys    []string
	OutputKeys   []string
	ToolCalls    []model.ToolCall
	ModelName    string
	Error        string
}

// NodeEventOption is a function that configures node event options.
type NodeEventOption func(*NodeEventOptions)

// WithNodeEventInvocationID sets the invocation ID for node events.
func WithNodeEventInvocationID(invocationID string) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithNodeEventNodeID sets the node ID for node events.
func WithNodeEventNodeID(nodeID string) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.NodeID = nodeID
	}
}

// WithNodeEventNodeType sets the node type for node events.
func WithNodeEventNodeType(nodeType NodeType) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.NodeType = nodeType
	}
}

// WithNodeEventStepNumber sets the step number for node events.
func WithNodeEventStepNumber(stepNumber int) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.StepNumber = stepNumber
	}
}

// WithNodeEventStartTime sets the start time for node events.
func WithNodeEventStartTime(startTime time.Time) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.StartTime = startTime
	}
}

// WithNodeEventEndTime sets the end time for node events.
func WithNodeEventEndTime(endTime time.Time) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.EndTime = endTime
	}
}

// WithNodeEventInputKeys sets the input keys for node events.
func WithNodeEventInputKeys(inputKeys []string) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.InputKeys = inputKeys
	}
}

// WithNodeEventOutputKeys sets the output keys for node events.
func WithNodeEventOutputKeys(outputKeys []string) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.OutputKeys = outputKeys
	}
}

// WithNodeEventToolCalls sets the tool calls for node events.
func WithNodeEventToolCalls(toolCalls []model.ToolCall) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.ToolCalls = toolCalls
	}
}

// WithNodeEventModelName sets the model name for node events.
func WithNodeEventModelName(modelName string) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.ModelName = modelName
	}
}

// WithNodeEventError sets the error message for node events.
func WithNodeEventError(errMsg string) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.Error = errMsg
	}
}

// PregelEventOptions contains options for creating Pregel events.
type PregelEventOptions struct {
	InvocationID    string
	StepNumber      int
	Phase           PregelPhase
	TaskCount       int
	UpdatedChannels []string
	ActiveNodes     []string
	StartTime       time.Time
	EndTime         time.Time
	Error           string
}

// PregelEventOption is a function that configures Pregel event options.
type PregelEventOption func(*PregelEventOptions)

// WithPregelEventInvocationID sets the invocation ID for Pregel events.
func WithPregelEventInvocationID(invocationID string) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithPregelEventStepNumber sets the step number for Pregel events.
func WithPregelEventStepNumber(stepNumber int) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.StepNumber = stepNumber
	}
}

// WithPregelEventPhase sets the phase for Pregel events.
func WithPregelEventPhase(phase PregelPhase) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.Phase = phase
	}
}

// WithPregelEventTaskCount sets the task count for Pregel events.
func WithPregelEventTaskCount(taskCount int) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.TaskCount = taskCount
	}
}

// WithPregelEventUpdatedChannels sets the updated channels for Pregel events.
func WithPregelEventUpdatedChannels(updatedChannels []string) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.UpdatedChannels = updatedChannels
	}
}

// WithPregelEventActiveNodes sets the active nodes for Pregel events.
func WithPregelEventActiveNodes(activeNodes []string) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.ActiveNodes = activeNodes
	}
}

// WithPregelEventStartTime sets the start time for Pregel events.
func WithPregelEventStartTime(startTime time.Time) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.StartTime = startTime
	}
}

// WithPregelEventEndTime sets the end time for Pregel events.
func WithPregelEventEndTime(endTime time.Time) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.EndTime = endTime
	}
}

// WithPregelEventError sets the error message for Pregel events.
func WithPregelEventError(errMsg string) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.Error = errMsg
	}
}

// ChannelEventOptions contains options for creating channel events.
type ChannelEventOptions struct {
	InvocationID   string
	ChannelName    string
	ChannelType    channel.Type
	ValueCount     int
	Available      bool
	TriggeredNodes []string
}

// ChannelEventOption is a function that configures channel event options.
type ChannelEventOption func(*ChannelEventOptions)

// WithChannelEventInvocationID sets the invocation ID for channel events.
func WithChannelEventInvocationID(invocationID string) ChannelEventOption {
	return func(opts *ChannelEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithChannelEventChannelName sets the channel name for channel events.
func WithChannelEventChannelName(channelName string) ChannelEventOption {
	return func(opts *ChannelEventOptions) {
		opts.ChannelName = channelName
	}
}

// WithChannelEventChannelType sets the channel type for channel events.
func WithChannelEventChannelType(channelType channel.Type) ChannelEventOption {
	return func(opts *ChannelEventOptions) {
		opts.ChannelType = channelType
	}
}

// WithChannelEventValueCount sets the value count for channel events.
func WithChannelEventValueCount(valueCount int) ChannelEventOption {
	return func(opts *ChannelEventOptions) {
		opts.ValueCount = valueCount
	}
}

// WithChannelEventAvailable sets the availability for channel events.
func WithChannelEventAvailable(available bool) ChannelEventOption {
	return func(opts *ChannelEventOptions) {
		opts.Available = available
	}
}

// WithChannelEventTriggeredNodes sets the triggered nodes for channel events.
func WithChannelEventTriggeredNodes(triggeredNodes []string) ChannelEventOption {
	return func(opts *ChannelEventOptions) {
		opts.TriggeredNodes = triggeredNodes
	}
}

// StateEventOptions contains options for creating state events.
type StateEventOptions struct {
	InvocationID string
	UpdatedKeys  []string
	RemovedKeys  []string
	StateSize    int
}

// StateEventOption is a function that configures state event options.
type StateEventOption func(*StateEventOptions)

// WithStateEventInvocationID sets the invocation ID for state events.
func WithStateEventInvocationID(invocationID string) StateEventOption {
	return func(opts *StateEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithStateEventUpdatedKeys sets the updated keys for state events.
func WithStateEventUpdatedKeys(updatedKeys []string) StateEventOption {
	return func(opts *StateEventOptions) {
		opts.UpdatedKeys = updatedKeys
	}
}

// WithStateEventRemovedKeys sets the removed keys for state events.
func WithStateEventRemovedKeys(removedKeys []string) StateEventOption {
	return func(opts *StateEventOptions) {
		opts.RemovedKeys = removedKeys
	}
}

// WithStateEventStateSize sets the state size for state events.
func WithStateEventStateSize(stateSize int) StateEventOption {
	return func(opts *StateEventOptions) {
		opts.StateSize = stateSize
	}
}

// CompletionEventOptions contains options for creating completion events.
type CompletionEventOptions struct {
	InvocationID  string
	FinalState    State
	TotalSteps    int
	TotalDuration time.Duration
}

// CompletionEventOption is a function that configures completion event options.
type CompletionEventOption func(*CompletionEventOptions)

// WithCompletionEventInvocationID sets the invocation ID for completion events.
func WithCompletionEventInvocationID(invocationID string) CompletionEventOption {
	return func(opts *CompletionEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithCompletionEventFinalState sets the final state for completion events.
func WithCompletionEventFinalState(finalState State) CompletionEventOption {
	return func(opts *CompletionEventOptions) {
		opts.FinalState = finalState
	}
}

// WithCompletionEventTotalSteps sets the total steps for completion events.
func WithCompletionEventTotalSteps(totalSteps int) CompletionEventOption {
	return func(opts *CompletionEventOptions) {
		opts.TotalSteps = totalSteps
	}
}

// WithCompletionEventTotalDuration sets the total duration for completion events.
func WithCompletionEventTotalDuration(totalDuration time.Duration) CompletionEventOption {
	return func(opts *CompletionEventOptions) {
		opts.TotalDuration = totalDuration
	}
}

// NewNodeStartEvent creates a new node start event.
func NewNodeStartEvent(opts ...NodeEventOption) *event.Event {
	options := &NodeEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := NodeExecutionMetadata{
		NodeID:     options.NodeID,
		NodeType:   options.NodeType,
		Phase:      ExecutionPhaseStart,
		StartTime:  options.StartTime,
		InputKeys:  options.InputKeys,
		StepNumber: options.StepNumber,
	}
	return NewGraphEvent(options.InvocationID, AuthorGraphNode, ObjectTypeGraphNodeStart,
		WithNodeMetadata(metadata))
}

// NewNodeCompleteEvent creates a new node completion event.
func NewNodeCompleteEvent(opts ...NodeEventOption) *event.Event {
	options := &NodeEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := NodeExecutionMetadata{
		NodeID:     options.NodeID,
		NodeType:   options.NodeType,
		Phase:      ExecutionPhaseComplete,
		StartTime:  options.StartTime,
		EndTime:    options.EndTime,
		Duration:   options.EndTime.Sub(options.StartTime),
		OutputKeys: options.OutputKeys,
		ToolCalls:  options.ToolCalls,
		ModelName:  options.ModelName,
		StepNumber: options.StepNumber,
	}
	return NewGraphEvent(options.InvocationID, AuthorGraphNode, ObjectTypeGraphNodeComplete,
		WithNodeMetadata(metadata))
}

// NewNodeErrorEvent creates a new node error event.
func NewNodeErrorEvent(opts ...NodeEventOption) *event.Event {
	options := &NodeEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := NodeExecutionMetadata{
		NodeID:     options.NodeID,
		NodeType:   options.NodeType,
		Phase:      ExecutionPhaseError,
		StartTime:  options.StartTime,
		EndTime:    options.EndTime,
		Duration:   options.EndTime.Sub(options.StartTime),
		Error:      options.Error,
		StepNumber: options.StepNumber,
	}
	return NewGraphEvent(options.InvocationID, AuthorGraphNode, ObjectTypeGraphNodeError,
		WithNodeMetadata(metadata))
}

// NewPregelStepEvent creates a new Pregel step event.
func NewPregelStepEvent(opts ...PregelEventOption) *event.Event {
	options := &PregelEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := PregelStepMetadata{
		StepNumber:      options.StepNumber,
		Phase:           options.Phase,
		TaskCount:       options.TaskCount,
		UpdatedChannels: options.UpdatedChannels,
		ActiveNodes:     options.ActiveNodes,
		StartTime:       options.StartTime,
		EndTime:         options.EndTime,
		Duration:        options.EndTime.Sub(options.StartTime),
	}
	return NewGraphEvent(options.InvocationID, AuthorGraphPregel, ObjectTypeGraphPregelStep,
		WithPregelMetadata(metadata))
}

// NewPregelErrorEvent creates a new Pregel error event.
func NewPregelErrorEvent(opts ...PregelEventOption) *event.Event {
	options := &PregelEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := PregelStepMetadata{
		StepNumber: options.StepNumber,
		Phase:      options.Phase,
		StartTime:  options.StartTime,
		EndTime:    options.EndTime,
		Duration:   options.EndTime.Sub(options.StartTime),
		Error:      options.Error,
	}
	return NewGraphEvent(options.InvocationID, AuthorGraphPregel, ObjectTypeGraphPregelStep,
		WithPregelMetadata(metadata))
}

// NewChannelUpdateEvent creates a new channel update event.
func NewChannelUpdateEvent(opts ...ChannelEventOption) *event.Event {
	options := &ChannelEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := ChannelUpdateMetadata{
		ChannelName:    options.ChannelName,
		ChannelType:    options.ChannelType,
		ValueCount:     options.ValueCount,
		Available:      options.Available,
		TriggeredNodes: options.TriggeredNodes,
	}
	return NewGraphEvent(options.InvocationID, AuthorGraphPregel, ObjectTypeGraphChannelUpdate,
		WithChannelMetadata(metadata))
}

// NewStateUpdateEvent creates a new state update event.
func NewStateUpdateEvent(opts ...StateEventOption) *event.Event {
	options := &StateEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := StateUpdateMetadata{
		UpdatedKeys: options.UpdatedKeys,
		RemovedKeys: options.RemovedKeys,
		StateSize:   options.StateSize,
	}
	return NewGraphEvent(options.InvocationID, AuthorGraphExecutor, ObjectTypeGraphStateUpdate,
		WithStateMetadata(metadata))
}

// NewGraphCompletionEvent creates a new graph completion event.
func NewGraphCompletionEvent(opts ...CompletionEventOption) *event.Event {
	options := &CompletionEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// Extract final response from state if available
	var finalResponse string
	if v, ok := options.FinalState[StateKeyLastResponse].(string); ok {
		finalResponse = v
	}

	e := NewGraphEvent(options.InvocationID, AuthorGraphExecutor, ObjectTypeGraphExecution)
	e.Response.Done = true
	if finalResponse != "" {
		e.Response.Choices = []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: finalResponse,
				},
			},
		}
	}

	// Add completion metadata to StateDelta
	if e.StateDelta == nil {
		e.StateDelta = make(map[string][]byte)
	}
	completionMetadata := CompletionMetadata{
		TotalSteps:     options.TotalSteps,
		TotalDuration:  options.TotalDuration,
		FinalStateKeys: len(extractStateKeys(options.FinalState)),
	}
	if jsonData, err := json.Marshal(completionMetadata); err == nil {
		e.StateDelta[MetadataKeyCompletion] = jsonData
	}

	return e
}

// extractStateKeys extracts all keys from a state map.
func extractStateKeys(state State) []string {
	keys := make([]string, 0, len(state))
	// Create a copy of the state to avoid concurrent access issues
	stateCopy := make(State, len(state))
	for k, v := range state {
		stateCopy[k] = v
	}
	for k := range stateCopy {
		keys = append(keys, k)
	}
	return keys
}
