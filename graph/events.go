//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
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
	// ObjectTypeGraphBarrier is the object type for graph-level barrier events.
	ObjectTypeGraphBarrier = "graph.barrier"
	// ObjectTypeGraphNodeExecution is the object type for node execution events.
	ObjectTypeGraphNodeExecution = "graph.node.execution"
	// ObjectTypeGraphNodeStart is the object type for node start events.
	ObjectTypeGraphNodeStart = "graph.node.start"
	// ObjectTypeGraphNodeComplete is the object type for node completion events.
	ObjectTypeGraphNodeComplete = "graph.node.complete"
	// ObjectTypeGraphNodeError is the object type for node error events.
	ObjectTypeGraphNodeError = "graph.node.error"
	// ObjectTypeGraphNodeCustom is the object type for node custom events emitted by NodeFunc.
	ObjectTypeGraphNodeCustom = "graph.node.custom"
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
	// ObjectTypeGraphCheckpoint is the object type for checkpoint events.
	ObjectTypeGraphCheckpoint = "graph.checkpoint"
	// ObjectTypeGraphCheckpointCreated is the object type for checkpoint creation events.
	ObjectTypeGraphCheckpointCreated = "graph.checkpoint.created"
	// ObjectTypeGraphCheckpointCommitted is the object type for checkpoint commit events.
	ObjectTypeGraphCheckpointCommitted = "graph.checkpoint.committed"
	// ObjectTypeGraphCheckpointInterrupt is the object type for checkpoint interrupt events.
	ObjectTypeGraphCheckpointInterrupt = "graph.checkpoint.interrupt"
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
	// MetadataKeyTool is the key for tool execution metadata.
	MetadataKeyTool = "_tool_metadata"
	// MetadataKeyModel is the key for model execution metadata.
	MetadataKeyModel = "_model_metadata"
	// MetadataKeyCheckpoint is the key for checkpoint metadata.
	MetadataKeyCheckpoint = "_checkpoint_metadata"
	// MetadataKeyCacheHit is a synthetic key set on node completion events when
	// a cache hit occurs for the node's output.
	MetadataKeyCacheHit = "_cache_hit"
	// MetadataKeyNodeCustom is the key for node custom event metadata.
	MetadataKeyNodeCustom = "_node_custom_metadata"
)

// NodeType represents the type of a graph node.
type NodeType string

// Node type constants.
const (
	NodeTypeFunction NodeType = "function"
	NodeTypeLLM      NodeType = "llm"
	NodeTypeTool     NodeType = "tool"
	NodeTypeAgent    NodeType = "agent"
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

// ToolExecutionPhase represents the phase of tool execution.
type ToolExecutionPhase string

// Tool execution phase constants.
const (
	ToolExecutionPhaseStart    ToolExecutionPhase = "start"
	ToolExecutionPhaseComplete ToolExecutionPhase = "complete"
	ToolExecutionPhaseError    ToolExecutionPhase = "error"
)

// String returns the string representation of the tool execution phase.
func (tep ToolExecutionPhase) String() string {
	return string(tep)
}

// ModelExecutionPhase represents the phase of model execution.
type ModelExecutionPhase string

// Model execution phase constants.
const (
	ModelExecutionPhaseStart    ModelExecutionPhase = "start"
	ModelExecutionPhaseComplete ModelExecutionPhase = "complete"
	ModelExecutionPhaseError    ModelExecutionPhase = "error"
)

// String returns the string representation of the model execution phase.
func (mep ModelExecutionPhase) String() string {
	return string(mep)
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
	// ModelInput contains the input sent to LLM nodes.
	ModelInput string `json:"modelInput,omitempty"`
	// StepNumber is the Pregel step number.
	StepNumber int `json:"stepNumber,omitempty"`
	// Attempt is the 1-based attempt number for this node execution.
	Attempt int `json:"attempt,omitempty"`
	// MaxAttempts is the maximum allowed attempts when retrying is enabled.
	MaxAttempts int `json:"maxAttempts,omitempty"`
	// NextDelay is the planned delay before the next retry attempt.
	NextDelay time.Duration `json:"nextDelay,omitempty"`
	// Retrying indicates whether a retry will be performed after this error.
	Retrying bool `json:"retrying,omitempty"`
}

// ToolExecutionMetadata contains metadata about tool execution.
type ToolExecutionMetadata struct {
	// ToolName is the name of the tool being executed.
	ToolName string `json:"toolName"`
	// ToolID is the unique identifier of the tool call.
	ToolID string `json:"toolId"`
	// ResponseID is the response/message ID that issued this tool call.
	ResponseID string `json:"responseId,omitempty"`
	// Phase is the execution phase.
	Phase ToolExecutionPhase `json:"phase"`
	// StartTime is when the execution started.
	StartTime time.Time `json:"startTime,omitempty"`
	// EndTime is when the execution completed.
	EndTime time.Time `json:"endTime,omitempty"`
	// Duration is the execution duration.
	Duration time.Duration `json:"duration,omitempty"`
	// Input contains the tool input arguments.
	Input string `json:"input,omitempty"`
	// Output contains the tool output result.
	Output string `json:"output,omitempty"`
	// Error is the error message if execution failed.
	Error string `json:"error,omitempty"`
	// InvocationID is the invocation ID.
	InvocationID string `json:"invocationId,omitempty"`
}

// ModelExecutionMetadata contains metadata about model execution.
type ModelExecutionMetadata struct {
	// ModelName is the name of the model being executed.
	ModelName string `json:"modelName"`
	// NodeID is the unique identifier of the node.
	NodeID string `json:"nodeId"`
	// ResponseID is the response/message ID for this model output.
	ResponseID string `json:"responseId,omitempty"`
	// Phase is the execution phase.
	Phase ModelExecutionPhase `json:"phase"`
	// StartTime is when the execution started.
	StartTime time.Time `json:"startTime,omitempty"`
	// EndTime is when the execution completed.
	EndTime time.Time `json:"endTime,omitempty"`
	// Duration is the execution duration.
	Duration time.Duration `json:"duration,omitempty"`
	// Input contains the model input (messages or prompt).
	Input string `json:"input,omitempty"`
	// Output contains the final model output result.
	Output string `json:"output,omitempty"`
	// Error is the error message if execution failed.
	Error string `json:"error,omitempty"`
	// InvocationID is the invocation ID.
	InvocationID string `json:"invocationId,omitempty"`
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
	// NodeID is the ID of the node where interrupt occurred.
	NodeID string `json:"nodeID,omitempty"`
	// InterruptValue is the value passed to interrupt().
	InterruptValue any `json:"interruptValue,omitempty"`
}

// ChannelUpdateMetadata contains metadata about channel updates.
type ChannelUpdateMetadata struct {
	// ChannelName is the name of the channel.
	ChannelName string `json:"channelName"`
	// ChannelType is the type of the channel.
	ChannelType channel.Behavior `json:"channelType"`
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
	// Tool metadata for tool execution events.
	Tool *ToolExecutionMetadata `json:"tool,omitempty"`
	// Model metadata for model execution events.
	Model *ModelExecutionMetadata `json:"model,omitempty"`
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

// NodeCustomEventCategory represents the category of node custom events.
type NodeCustomEventCategory string

// Node custom event category constants.
const (
	// NodeCustomEventCategoryCustom is the category for general custom events.
	NodeCustomEventCategoryCustom NodeCustomEventCategory = "custom"
	// NodeCustomEventCategoryProgress is the category for progress events.
	NodeCustomEventCategoryProgress NodeCustomEventCategory = "progress"
	// NodeCustomEventCategoryText is the category for streaming text events.
	NodeCustomEventCategoryText NodeCustomEventCategory = "text"
)

// String returns the string representation of the node custom event category.
func (c NodeCustomEventCategory) String() string {
	return string(c)
}

// NodeCustomEventMetadata contains metadata about node custom events.
type NodeCustomEventMetadata struct {
	// EventType is the user-defined event type.
	EventType string `json:"eventType"`
	// Category is the category of the custom event (custom, progress, text).
	Category NodeCustomEventCategory `json:"category"`
	// NodeID is the ID of the node that emitted the event.
	NodeID string `json:"nodeId"`
	// InvocationID is the invocation ID of the current execution.
	InvocationID string `json:"invocationId"`
	// StepNumber is the Pregel step number when the event was emitted.
	StepNumber int `json:"stepNumber,omitempty"`
	// Timestamp is when the event was emitted.
	Timestamp time.Time `json:"timestamp"`
	// Payload is the custom payload data.
	Payload any `json:"payload,omitempty"`
	// Progress is the progress percentage (0-100) for progress events.
	Progress float64 `json:"progress,omitempty"`
	// Message is the message for progress events or text content for text events.
	Message string `json:"message,omitempty"`
}

// EventOption is a function that configures a graph event.
type EventOption func(*event.Event)

// WithNodeMetadata adds node execution metadata to the event.
func WithNodeMetadata(metadata NodeExecutionMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON.
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON.
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyNode] = jsonData
		}
	}
}

// WithToolMetadata adds tool execution metadata to the event.
func WithToolMetadata(metadata ToolExecutionMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON.
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON.
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyTool] = jsonData
		}
	}
}

// WithModelMetadata adds model execution metadata to the event.
func WithModelMetadata(metadata ModelExecutionMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON.
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON.
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyModel] = jsonData
		}
	}
}

// WithPregelMetadata adds Pregel step metadata to the event.
func WithPregelMetadata(metadata PregelStepMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON.
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON.
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyPregel] = jsonData
		}
	}
}

// WithChannelMetadata adds channel update metadata to the event.
func WithChannelMetadata(metadata ChannelUpdateMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON.
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON.
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyChannel] = jsonData
		}
	}
}

// WithStateMetadata adds state update metadata to the event.
func WithStateMetadata(metadata StateUpdateMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON.
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON.
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyState] = jsonData
		}
	}
}

// WithNodeCustomMetadata adds node custom event metadata to the event.
func WithNodeCustomMetadata(metadata NodeCustomEventMetadata) EventOption {
	return func(e *event.Event) {
		// Store metadata in StateDelta as JSON.
		if e.StateDelta == nil {
			e.StateDelta = make(map[string][]byte)
		}
		// Marshal metadata to JSON.
		if jsonData, err := json.Marshal(metadata); err == nil {
			e.StateDelta[MetadataKeyNodeCustom] = jsonData
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

// formatNodeAuthor returns nodeID if non-empty; otherwise returns fallback.
func formatNodeAuthor(nodeID, fallbackAuthor string) string {
	if nodeID != "" {
		return nodeID
	}
	return fallbackAuthor
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
	ModelInput   string
	Error        string
	// Retry metadata (optional)
	Attempt     int
	MaxAttempts int
	NextDelay   time.Duration
	Retrying    bool
}

// NodeEventOption is a function that configures node event options.
type NodeEventOption func(*NodeEventOptions)

// ToolEventOptions contains options for creating tool events.
type ToolEventOptions struct {
	InvocationID string
	ToolName     string
	ToolID       string
	ResponseID   string
	Phase        ToolExecutionPhase
	StartTime    time.Time
	EndTime      time.Time
	Input        string
	Output       string
	Error        error
	// NodeID is optional. When provided, author becomes node-scoped.
	NodeID string
	// IncludeResponse controls whether NewToolExecutionEvent should attach a
	// tool.response payload in addition to metadata.
	IncludeResponse bool
}

// ToolEventOption is a function that configures tool event options.
type ToolEventOption func(*ToolEventOptions)

// WithToolEventNodeID sets the node ID for tool events.
func WithToolEventNodeID(nodeID string) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.NodeID = nodeID
	}
}

// ModelEventOptions contains options for creating model events.
type ModelEventOptions struct {
	InvocationID string
	ModelName    string
	NodeID       string
	ResponseID   string
	Phase        ModelExecutionPhase
	StartTime    time.Time
	EndTime      time.Time
	Input        string
	Output       string
	Error        error
	StepNumber   int
}

// ModelEventOption is a function that configures model event options.
type ModelEventOption func(*ModelEventOptions)

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

// WithNodeEventModelInput sets the model input for node events.
func WithNodeEventModelInput(modelInput string) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.ModelInput = modelInput
	}
}

// WithNodeEventError sets the error message for node events.
func WithNodeEventError(errMsg string) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.Error = errMsg
	}
}

// WithNodeEventAttempt sets the current attempt number (1-based).
func WithNodeEventAttempt(attempt int) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.Attempt = attempt
	}
}

// WithNodeEventMaxAttempts sets the maximum attempts.
func WithNodeEventMaxAttempts(maxAttempts int) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.MaxAttempts = maxAttempts
	}
}

// WithNodeEventNextDelay sets the planned next delay before retry.
func WithNodeEventNextDelay(delay time.Duration) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.NextDelay = delay
	}
}

// WithNodeEventRetrying indicates whether a retry will be performed.
func WithNodeEventRetrying(retrying bool) NodeEventOption {
	return func(opts *NodeEventOptions) {
		opts.Retrying = retrying
	}
}

// WithToolEventInvocationID sets the invocation ID for tool events.
func WithToolEventInvocationID(invocationID string) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithToolEventToolName sets the tool name for tool events.
func WithToolEventToolName(toolName string) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.ToolName = toolName
	}
}

// WithToolEventToolID sets the tool ID for tool events.
func WithToolEventToolID(toolID string) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.ToolID = toolID
	}
}

// WithToolEventResponseID sets the parent response ID for tool events.
func WithToolEventResponseID(responseID string) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.ResponseID = responseID
	}
}

// WithToolEventPhase sets the phase for tool events.
func WithToolEventPhase(phase ToolExecutionPhase) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.Phase = phase
	}
}

// WithToolEventStartTime sets the start time for tool events.
func WithToolEventStartTime(startTime time.Time) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.StartTime = startTime
	}
}

// WithToolEventEndTime sets the end time for tool events.
func WithToolEventEndTime(endTime time.Time) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.EndTime = endTime
	}
}

// WithToolEventInput sets the input for tool events.
func WithToolEventInput(input string) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.Input = input
	}
}

// WithToolEventOutput sets the output for tool events.
func WithToolEventOutput(output string) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.Output = output
	}
}

// WithToolEventError sets the error for tool events.
func WithToolEventError(err error) ToolEventOption {
	return func(opts *ToolEventOptions) {
		if err != nil {
			opts.Error = err
		}
	}
}

// WithToolEventIncludeResponse controls whether the resulting event should
// embed a tool.response payload in addition to metadata.
func WithToolEventIncludeResponse(include bool) ToolEventOption {
	return func(opts *ToolEventOptions) {
		opts.IncludeResponse = include
	}
}

// WithModelEventResponseID sets the response ID for model events.
func WithModelEventResponseID(responseID string) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.ResponseID = responseID
	}
}

// WithModelEventInvocationID sets the invocation ID for model events.
func WithModelEventInvocationID(invocationID string) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithModelEventModelName sets the model name for model events.
func WithModelEventModelName(modelName string) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.ModelName = modelName
	}
}

// WithModelEventNodeID sets the node ID for model events.
func WithModelEventNodeID(nodeID string) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.NodeID = nodeID
	}
}

// WithModelEventPhase sets the phase for model events.
func WithModelEventPhase(phase ModelExecutionPhase) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.Phase = phase
	}
}

// WithModelEventStartTime sets the start time for model events.
func WithModelEventStartTime(startTime time.Time) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.StartTime = startTime
	}
}

// WithModelEventEndTime sets the end time for model events.
func WithModelEventEndTime(endTime time.Time) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.EndTime = endTime
	}
}

// WithModelEventInput sets the input for model events.
func WithModelEventInput(input string) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.Input = input
	}
}

// WithModelEventOutput sets the output for model events.
func WithModelEventOutput(output string) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.Output = output
	}
}

// WithModelEventError sets the error for model events.
func WithModelEventError(err error) ModelEventOption {
	return func(opts *ModelEventOptions) {
		if err != nil {
			opts.Error = err
		}
	}
}

// WithModelEventStepNumber sets the step number for model events.
func WithModelEventStepNumber(stepNumber int) ModelEventOption {
	return func(opts *ModelEventOptions) {
		opts.StepNumber = stepNumber
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
	NodeID          string
	InterruptValue  any
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

// WithPregelEventNodeID sets the node ID for Pregel events.
func WithPregelEventNodeID(nodeID string) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.NodeID = nodeID
	}
}

// WithPregelEventInterruptValue sets the interrupt value for Pregel events.
func WithPregelEventInterruptValue(value any) PregelEventOption {
	return func(opts *PregelEventOptions) {
		opts.InterruptValue = value
	}
}

// ChannelEventOptions contains options for creating channel events.
type ChannelEventOptions struct {
	InvocationID   string
	ChannelName    string
	ChannelType    channel.Behavior
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
func WithChannelEventChannelType(channelType channel.Behavior) ChannelEventOption {
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
		NodeID:      options.NodeID,
		NodeType:    options.NodeType,
		Phase:       ExecutionPhaseStart,
		StartTime:   options.StartTime,
		InputKeys:   options.InputKeys,
		ModelName:   options.ModelName,
		ModelInput:  options.ModelInput,
		StepNumber:  options.StepNumber,
		Attempt:     options.Attempt,
		MaxAttempts: options.MaxAttempts,
	}
	return NewGraphEvent(options.InvocationID,
		formatNodeAuthor(options.NodeID, AuthorGraphNode),
		ObjectTypeGraphNodeStart,
		WithNodeMetadata(metadata))
}

// NewNodeCompleteEvent creates a new node completion event.
func NewNodeCompleteEvent(opts ...NodeEventOption) *event.Event {
	options := &NodeEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := NodeExecutionMetadata{
		NodeID:      options.NodeID,
		NodeType:    options.NodeType,
		Phase:       ExecutionPhaseComplete,
		StartTime:   options.StartTime,
		EndTime:     options.EndTime,
		Duration:    options.EndTime.Sub(options.StartTime),
		OutputKeys:  options.OutputKeys,
		ToolCalls:   options.ToolCalls,
		ModelName:   options.ModelName,
		StepNumber:  options.StepNumber,
		Attempt:     options.Attempt,
		MaxAttempts: options.MaxAttempts,
	}
	return NewGraphEvent(options.InvocationID,
		formatNodeAuthor(options.NodeID, AuthorGraphNode),
		ObjectTypeGraphNodeComplete,
		WithNodeMetadata(metadata))
}

// NewNodeErrorEvent creates a new node error event.
func NewNodeErrorEvent(opts ...NodeEventOption) *event.Event {
	options := &NodeEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := NodeExecutionMetadata{
		NodeID:      options.NodeID,
		NodeType:    options.NodeType,
		Phase:       ExecutionPhaseError,
		StartTime:   options.StartTime,
		EndTime:     options.EndTime,
		Duration:    options.EndTime.Sub(options.StartTime),
		Error:       options.Error,
		StepNumber:  options.StepNumber,
		Attempt:     options.Attempt,
		MaxAttempts: options.MaxAttempts,
		NextDelay:   options.NextDelay,
		Retrying:    options.Retrying,
	}
	graphEvent := NewGraphEvent(options.InvocationID,
		formatNodeAuthor(options.NodeID, AuthorGraphNode),
		ObjectTypeGraphNodeError,
		WithNodeMetadata(metadata))
	if options.Error != "" {
		graphEvent.Response.Object = model.ObjectTypeError
		graphEvent.Response.Error = &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: options.Error,
		}
		graphEvent.Object = graphEvent.Response.Object
	}
	return graphEvent
}

// NewToolExecutionEvent creates a new tool execution event.
func NewToolExecutionEvent(opts ...ToolEventOption) *event.Event {
	options := &ToolEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	var errorMsg string
	if options.Error != nil {
		errorMsg = options.Error.Error()
	}

	metadata := ToolExecutionMetadata{
		ToolName:     options.ToolName,
		ToolID:       options.ToolID,
		ResponseID:   options.ResponseID,
		Phase:        options.Phase,
		StartTime:    options.StartTime,
		EndTime:      options.EndTime,
		Duration:     options.EndTime.Sub(options.StartTime),
		Input:        options.Input,
		Output:       options.Output,
		Error:        errorMsg,
		InvocationID: options.InvocationID,
	}
	evt := NewGraphEvent(options.InvocationID,
		formatNodeAuthor(options.NodeID, AuthorGraphNode),
		ObjectTypeGraphNodeExecution,
		WithToolMetadata(metadata))

	if options.IncludeResponse {
		toolMessage := model.NewToolMessage(options.ToolID, options.ToolName, options.Output)
		resp := &model.Response{
			Object:    model.ObjectTypeToolResponse,
			Created:   options.EndTime.Unix(),
			Choices:   []model.Choice{{Index: 0, Message: toolMessage}},
			Timestamp: options.EndTime,
			Done:      true,
		}
		if options.Error != nil {
			resp.Error = &model.ResponseError{
				Type:    model.ErrorTypeFlowError,
				Message: options.Error.Error(),
			}
		}
		if resp.Timestamp.IsZero() {
			resp.Timestamp = time.Now()
			resp.Created = resp.Timestamp.Unix()
		}
		evt.Response = resp
		evt.Object = resp.Object
	}
	return evt
}

// NewModelExecutionEvent creates a new model execution event.
func NewModelExecutionEvent(opts ...ModelEventOption) *event.Event {
	options := &ModelEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	var errorMsg string
	if options.Error != nil {
		errorMsg = options.Error.Error()
	}

	metadata := ModelExecutionMetadata{
		ModelName:    options.ModelName,
		NodeID:       options.NodeID,
		ResponseID:   options.ResponseID,
		Phase:        options.Phase,
		StartTime:    options.StartTime,
		EndTime:      options.EndTime,
		Duration:     options.EndTime.Sub(options.StartTime),
		Input:        options.Input,
		Output:       options.Output,
		Error:        errorMsg,
		InvocationID: options.InvocationID,
		StepNumber:   options.StepNumber,
	}
	return NewGraphEvent(options.InvocationID,
		formatNodeAuthor(options.NodeID, AuthorGraphNode),
		ObjectTypeGraphNodeExecution,
		WithModelMetadata(metadata))
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
	// Build base graph event with metadata.
	ge := NewGraphEvent(options.InvocationID, AuthorGraphPregel, ObjectTypeGraphPregelStep,
		WithPregelMetadata(metadata))
	// Mirror error to Event.Error for easier consumption by clients that
	// only check event.Error, while keeping object as graph.pregel.step
	// for compatibility with existing consumers.
	if options.Error != "" {
		ge.Response.Error = &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: options.Error,
		}
	}
	return ge
}

// NewPregelInterruptEvent creates a new Pregel interrupt event.
func NewPregelInterruptEvent(opts ...PregelEventOption) *event.Event {
	options := &PregelEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := PregelStepMetadata{
		StepNumber:     options.StepNumber,
		Phase:          options.Phase,
		StartTime:      options.StartTime,
		EndTime:        options.EndTime,
		Duration:       options.EndTime.Sub(options.StartTime),
		NodeID:         options.NodeID,
		InterruptValue: options.InterruptValue,
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
	finalResponse := extractFinalResponse(options.FinalState)

	e := NewGraphEvent(options.InvocationID, AuthorGraphExecutor, ObjectTypeGraphExecution)
	e.Response.Done = true
	// Always initialize StateDelta to a non-nil map to ensure consumers can rely on it.
	ensureStateDelta(e)
	if finalResponse != "" {
		e.Response.Choices = buildFinalChoices(finalResponse)
	}

	// Add completion metadata to StateDelta
	addCompletionMetadata(e, options)
	// Also include a serialized snapshot of the final state itself so downstream
	// consumers (including tests) can reconstruct state without additional logic.
	serializeFinalState(e, options.FinalState)
	return e
}

// ensureStateDelta initializes StateDelta if nil.
func ensureStateDelta(e *event.Event) {
	if e.StateDelta == nil {
		e.StateDelta = make(map[string][]byte)
	}
}

// extractFinalResponse fetches the last response text from state.
func extractFinalResponse(state State) string {
	if v, ok := state[StateKeyLastResponse].(string); ok {
		return v
	}
	return ""
}

// buildFinalChoices constructs the terminal assistant message choice.
func buildFinalChoices(text string) []model.Choice {
	return []model.Choice{{
		Index: 0,
		Message: model.Message{
			Role:    model.RoleAssistant,
			Content: text,
		},
	}}
}

// addCompletionMetadata attaches completion metadata to StateDelta.
func addCompletionMetadata(e *event.Event, options *CompletionEventOptions) {
	completionMetadata := CompletionMetadata{
		TotalSteps:     options.TotalSteps,
		TotalDuration:  options.TotalDuration,
		FinalStateKeys: len(extractStateKeys(options.FinalState)),
	}
	if jsonData, err := json.Marshal(completionMetadata); err == nil {
		e.StateDelta[MetadataKeyCompletion] = jsonData
	}
}

// serializeFinalState writes serializable final state keys into StateDelta.
func serializeFinalState(e *event.Event, state State) {
	if state == nil {
		return
	}
	for key, value := range state {
		// Skip internal/ephemeral keys that are not JSON-serializable or can race
		// due to concurrent updates (e.g., execution context and callbacks).
		if isInternalStateKey(key) {
			continue
		}

		// Marshal a deep-copied snapshot to avoid racing on shared references.
		snapshot := deepCopyAny(value)

		// Special case: when users put JSON bytes into graph state (e.g.,
		// json.Marshal output), encoding/json would base64 it if we marshal the
		// []byte again. If it's already valid JSON, keep it as-is so downstream
		// consumers can json.Unmarshal it directly.
		if raw, ok := snapshot.([]byte); ok && json.Valid(raw) {
			e.StateDelta[key] = raw
			continue
		}

		if jsonData, err := json.Marshal(snapshot); err == nil {
			e.StateDelta[key] = jsonData
		}
	}
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

// CheckpointEventOptions contains options for creating checkpoint events.
type CheckpointEventOptions struct {
	InvocationID   string
	CheckpointID   string
	Source         string
	Step           int
	Duration       time.Duration
	Bytes          int64
	WritesCount    int
	ResumeReplay   bool
	InterruptValue any
}

// CheckpointEventOption is a function that configures checkpoint event options.
type CheckpointEventOption func(*CheckpointEventOptions)

// WithCheckpointEventInvocationID sets the invocation ID.
func WithCheckpointEventInvocationID(invocationID string) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithCheckpointEventCheckpointID sets the checkpoint ID.
func WithCheckpointEventCheckpointID(checkpointID string) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.CheckpointID = checkpointID
	}
}

// WithCheckpointEventSource sets the checkpoint source.
func WithCheckpointEventSource(source string) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.Source = source
	}
}

// WithCheckpointEventStep sets the step number.
func WithCheckpointEventStep(step int) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.Step = step
	}
}

// WithCheckpointEventDuration sets the duration.
func WithCheckpointEventDuration(duration time.Duration) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.Duration = duration
	}
}

// WithCheckpointEventBytes sets the bytes written.
func WithCheckpointEventBytes(bytes int64) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.Bytes = bytes
	}
}

// WithCheckpointEventWritesCount sets the writes count.
func WithCheckpointEventWritesCount(count int) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.WritesCount = count
	}
}

// WithCheckpointEventResumeReplay sets the resume replay flag.
func WithCheckpointEventResumeReplay(replay bool) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.ResumeReplay = replay
	}
}

// WithCheckpointEventInterruptValue sets the interrupt value.
func WithCheckpointEventInterruptValue(value any) CheckpointEventOption {
	return func(opts *CheckpointEventOptions) {
		opts.InterruptValue = value
	}
}

// NewCheckpointCreatedEvent creates a new checkpoint created event.
func NewCheckpointCreatedEvent(opts ...CheckpointEventOption) *event.Event {
	options := &CheckpointEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := map[string]any{
		CfgKeyCheckpointID:  options.CheckpointID,
		EventKeySource:      options.Source,
		EventKeyStep:        options.Step,
		EventKeyDuration:    options.Duration,
		EventKeyBytes:       options.Bytes,
		EventKeyWritesCount: options.WritesCount,
	}

	e := NewGraphEvent(options.InvocationID, AuthorGraphExecutor, ObjectTypeGraphCheckpointCreated)
	if e.StateDelta == nil {
		e.StateDelta = make(map[string][]byte)
	}
	if jsonData, err := json.Marshal(metadata); err == nil {
		e.StateDelta[MetadataKeyCheckpoint] = jsonData
	}

	return e
}

// NewCheckpointCommittedEvent creates a new checkpoint committed event.
func NewCheckpointCommittedEvent(opts ...CheckpointEventOption) *event.Event {
	options := &CheckpointEventOptions{}
	for _, opt := range opts {
		opt(options)
	}

	metadata := map[string]any{
		CfgKeyCheckpointID:  options.CheckpointID,
		EventKeySource:      options.Source,
		EventKeyStep:        options.Step,
		EventKeyDuration:    options.Duration,
		EventKeyBytes:       options.Bytes,
		EventKeyWritesCount: options.WritesCount,
	}

	e := NewGraphEvent(options.InvocationID, AuthorGraphExecutor, ObjectTypeGraphCheckpointCommitted)
	if e.StateDelta == nil {
		e.StateDelta = make(map[string][]byte)
	}
	if jsonData, err := json.Marshal(metadata); err == nil {
		e.StateDelta[MetadataKeyCheckpoint] = jsonData
	}

	return e
}

// NodeCustomEventOptions contains options for creating node custom events.
type NodeCustomEventOptions struct {
	InvocationID string
	NodeID       string
	EventType    string
	Category     NodeCustomEventCategory
	StepNumber   int
	Payload      any
	Progress     float64
	Message      string
	Branch       string
}

// NodeCustomEventOption is a function that configures node custom event options.
type NodeCustomEventOption func(*NodeCustomEventOptions)

// WithNodeCustomEventInvocationID sets the invocation ID for node custom events.
func WithNodeCustomEventInvocationID(invocationID string) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.InvocationID = invocationID
	}
}

// WithNodeCustomEventNodeID sets the node ID for node custom events.
func WithNodeCustomEventNodeID(nodeID string) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.NodeID = nodeID
	}
}

// WithNodeCustomEventEventType sets the event type for node custom events.
func WithNodeCustomEventEventType(eventType string) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.EventType = eventType
	}
}

// WithNodeCustomEventCategory sets the category for node custom events.
func WithNodeCustomEventCategory(category NodeCustomEventCategory) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.Category = category
	}
}

// WithNodeCustomEventStepNumber sets the step number for node custom events.
func WithNodeCustomEventStepNumber(stepNumber int) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.StepNumber = stepNumber
	}
}

// WithNodeCustomEventPayload sets the payload for node custom events.
func WithNodeCustomEventPayload(payload any) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.Payload = payload
	}
}

// WithNodeCustomEventProgress sets the progress for node custom events.
func WithNodeCustomEventProgress(progress float64) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.Progress = progress
	}
}

// WithNodeCustomEventMessage sets the message for node custom events.
func WithNodeCustomEventMessage(message string) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.Message = message
	}
}

// WithNodeCustomEventBranch sets the branch for node custom events.
func WithNodeCustomEventBranch(branch string) NodeCustomEventOption {
	return func(opts *NodeCustomEventOptions) {
		opts.Branch = branch
	}
}

// NewNodeCustomEvent creates a new node custom event.
// This function is used for creating general custom events emitted by NodeFunc.
func NewNodeCustomEvent(opts ...NodeCustomEventOption) *event.Event {
	options := &NodeCustomEventOptions{
		Category: NodeCustomEventCategoryCustom,
	}
	for _, opt := range opts {
		opt(options)
	}

	metadata := NodeCustomEventMetadata{
		EventType:    options.EventType,
		Category:     options.Category,
		NodeID:       options.NodeID,
		InvocationID: options.InvocationID,
		StepNumber:   options.StepNumber,
		Timestamp:    time.Now(),
		Payload:      options.Payload,
		Progress:     options.Progress,
		Message:      options.Message,
	}

	evt := NewGraphEvent(
		options.InvocationID,
		formatNodeAuthor(options.NodeID, AuthorGraphNode),
		ObjectTypeGraphNodeCustom,
		WithNodeCustomMetadata(metadata),
	)
	if options.Branch != "" {
		evt.Branch = options.Branch
	}
	return evt
}

// NewNodeProgressEvent creates a new progress event for node execution.
// Progress should be a value between 0 and 100.
func NewNodeProgressEvent(opts ...NodeCustomEventOption) *event.Event {
	options := &NodeCustomEventOptions{
		EventType: "progress",
		Category:  NodeCustomEventCategoryProgress,
	}
	for _, opt := range opts {
		opt(options)
	}

	// Ensure category and event type are set for progress events
	options.Category = NodeCustomEventCategoryProgress
	if options.EventType == "" {
		options.EventType = "progress"
	}

	// Clamp progress to 0-100
	if options.Progress < 0 {
		options.Progress = 0
	}
	if options.Progress > 100 {
		options.Progress = 100
	}

	metadata := NodeCustomEventMetadata{
		EventType:    options.EventType,
		Category:     options.Category,
		NodeID:       options.NodeID,
		InvocationID: options.InvocationID,
		StepNumber:   options.StepNumber,
		Timestamp:    time.Now(),
		Progress:     options.Progress,
		Message:      options.Message,
	}

	evt := NewGraphEvent(
		options.InvocationID,
		formatNodeAuthor(options.NodeID, AuthorGraphNode),
		ObjectTypeGraphNodeCustom,
		WithNodeCustomMetadata(metadata),
	)
	if options.Branch != "" {
		evt.Branch = options.Branch
	}
	return evt
}

// NewNodeTextEvent creates a new streaming text event for node execution.
// This is useful for streaming intermediate text output from a node.
func NewNodeTextEvent(opts ...NodeCustomEventOption) *event.Event {
	options := &NodeCustomEventOptions{
		EventType: "text",
		Category:  NodeCustomEventCategoryText,
	}
	for _, opt := range opts {
		opt(options)
	}

	// Ensure category and event type are set for text events
	options.Category = NodeCustomEventCategoryText
	if options.EventType == "" {
		options.EventType = "text"
	}

	metadata := NodeCustomEventMetadata{
		EventType:    options.EventType,
		Category:     options.Category,
		NodeID:       options.NodeID,
		InvocationID: options.InvocationID,
		StepNumber:   options.StepNumber,
		Timestamp:    time.Now(),
		Message:      options.Message,
	}

	evt := NewGraphEvent(
		options.InvocationID,
		formatNodeAuthor(options.NodeID, AuthorGraphNode),
		ObjectTypeGraphNodeCustom,
		WithNodeCustomMetadata(metadata),
	)
	if options.Branch != "" {
		evt.Branch = options.Branch
	}
	return evt
}
