//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// WaitNoticeWithoutTimeout is the timeout duration for waiting without timeout
	WaitNoticeWithoutTimeout = 0 * time.Second

	// AppendEventNoticeKeyPrefix is the prefix for append event notice keys
	AppendEventNoticeKeyPrefix = "append_event:"

	// BranchDelimiter is the delimiter for branch
	BranchDelimiter = "/"

	// EventFilterKeyDelimiter is the delimiter for event filter key
	EventFilterKeyDelimiter = "/"
)

// TransferInfo contains information about a pending agent transfer.
type TransferInfo struct {
	// TargetAgentName is the name of the agent to transfer control to.
	TargetAgentName string
	// Message is the message to send to the target agent.
	Message string
}

// Invocation represents the context for a flow execution.
type Invocation struct {
	// Agent is the agent that is being invoked.
	Agent Agent
	// AgentName is the name of the agent that is being invoked.
	AgentName string
	// InvocationID is the ID of the invocation.
	InvocationID string
	// Branch records agent execution chain information.
	// In multi-agent mode, this is useful for tracing agent execution trajectories.
	Branch string
	// EndInvocation is a flag that indicates if the invocation is complete.
	EndInvocation bool
	// Session is the session that is being used for the invocation.
	Session *session.Session
	// Model is the model that is being used for the invocation.
	Model model.Model
	// Message is the message that is being sent to the agent.
	Message model.Message
	// RunOptions is the options for the Run method.
	RunOptions RunOptions
	// TransferInfo contains information about a pending agent transfer.
	TransferInfo *TransferInfo

	// StructuredOutput defines how the model should produce structured output for this invocation.
	StructuredOutput *model.StructuredOutput
	// StructuredOutputType is the Go type to unmarshal the final JSON into.
	StructuredOutputType reflect.Type

	// MemoryService is the service for managing memory.
	MemoryService memory.Service
	// ArtifactService is the service for managing artifacts.
	ArtifactService artifact.Service

	// noticeChanMap is used to signal when events are written to the session.
	noticeChanMap map[string]chan any
	noticeMu      *sync.Mutex

	// eventFilterKey is used to filter events for flow or agent
	eventFilterKey string

	// parent is the parent invocation, if any
	parent *Invocation

	// state stores invocation-scoped state data (lazy initialized).
	// Can be used by callbacks, middleware, or any invocation-scoped logic.
	state   map[string]any
	stateMu sync.RWMutex
}

// DefaultWaitNoticeTimeoutErr is the default error returned when a wait notice times out.
var DefaultWaitNoticeTimeoutErr = NewWaitNoticeTimeoutError("wait notice timeout.")

// WaitNoticeTimeoutError represents an error that signals the wait notice timeout.
type WaitNoticeTimeoutError struct {
	// Message contains the stop reason
	Message string
}

// Error implements the error interface.
func (e *WaitNoticeTimeoutError) Error() string {
	return e.Message
}

// AsWaitNoticeTimeoutError checks if an error is a AsWaitNoticeTimeoutError using errors.As.
func AsWaitNoticeTimeoutError(err error) (*WaitNoticeTimeoutError, bool) {
	var waitNoticeTimeoutErr *WaitNoticeTimeoutError
	ok := errors.As(err, &waitNoticeTimeoutErr)
	return waitNoticeTimeoutErr, ok
}

// NewWaitNoticeTimeoutError creates a new AsWaitNoticeTimeoutError with the given message.
func NewWaitNoticeTimeoutError(message string) *WaitNoticeTimeoutError {
	return &WaitNoticeTimeoutError{Message: message}
}

// RunOption is a function that configures a RunOptions.
type RunOption func(*RunOptions)

// WithRuntimeState sets the runtime state for the RunOptions.
func WithRuntimeState(state map[string]any) RunOption {
	return func(opts *RunOptions) {
		opts.RuntimeState = state
	}
}

// WithKnowledgeFilter sets the metadata filter for the RunOptions.
func WithKnowledgeFilter(filter map[string]any) RunOption {
	return func(opts *RunOptions) {
		opts.KnowledgeFilter = filter
	}
}

// WithKnowledgeConditionedFilter sets the complex condition filter for the RunOptions.
func WithKnowledgeConditionedFilter(filter *searchfilter.UniversalFilterCondition) RunOption {
	return func(opts *RunOptions) {
		opts.KnowledgeConditionedFilter = filter
	}
}

// WithMessages sets the caller-supplied conversation history for this run.
// Runner uses this history to auto-seed an empty Session (once) and to
// populate `invocation.Message` via RunWithMessages for compatibility. The
// content processor itself does not read this field; it derives messages from
// Session events (and may fall back to a single `invocation.Message` when the
// Session is empty).
func WithMessages(messages []model.Message) RunOption {
	return func(opts *RunOptions) {
		opts.Messages = messages
	}
}

// WithRequestID sets the request id for the RunOptions.
func WithRequestID(requestID string) RunOption {
	return func(opts *RunOptions) {
		opts.RequestID = requestID
	}
}

// WithModel sets the model for this specific run.
// This allows temporarily switching the model for a single request without
// affecting other requests or the agent's default model configuration.
//
// Example:
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithModel(customModel),
//	)
func WithModel(m model.Model) RunOption {
	return func(opts *RunOptions) {
		opts.Model = m
	}
}

// WithModelName sets the model name for this specific run.
// The agent will look up the model by name from its registered models.
// This is useful when the agent has multiple models registered via WithModels.
//
// Example:
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithModelName("gpt-4"),
//	)
func WithModelName(name string) RunOption {
	return func(opts *RunOptions) {
		opts.ModelName = name
	}
}

// WithToolFilter sets a custom tool filter function for this specific run.
// The filter function receives a context and a tool, and returns true if the tool should be included.
//
// This is useful for:
//   - Permission control: restrict tool access based on user roles or runtime conditions
//   - Cost optimization: reduce token usage by limiting tool descriptions
//   - Feature isolation: limit capabilities for specific use cases
//   - Dynamic filtering: filter tools based on runtime state, session data, etc.
//
// Example - Simple name-based filtering:
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithToolFilter(tool.NewIncludeToolNamesFilter("calculator", "time_tool")),
//	)
//
// Example - Custom logic with runtime state:
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithToolFilter(func(ctx context.Context, t tool.Tool) bool {
//	        // Access invocation from context if needed
//	        inv, _ := agent.InvocationFromContext(ctx)
//	        userLevel, _ := inv.Session.Get("user_level").(string)
//
//	        // Premium users get all tools
//	        if userLevel == "premium" {
//	            return true
//	        }
//
//	        // Free users only get basic tools
//	        toolName := t.Declaration().Name
//	        return toolName == "calculator" || toolName == "time_tool"
//	    }),
//	)
//
// Note: Framework tools (knowledge_search, transfer_to_agent) are never filtered
// and will always be available regardless of the filter function.
//
// Note: This is a "soft" constraint. Tools should still implement their own
// authorization logic for security.
func WithToolFilter(filter tool.FilterFunc) RunOption {
	return func(opts *RunOptions) {
		opts.ToolFilter = filter
	}
}

// WithA2ARequestOptions sets the A2A request options for the RunOptions.
// These options will be passed to A2A agent's SendMessage and StreamMessage calls.
// This allows passing dynamic HTTP headers or other request-specific options for each run.
func WithA2ARequestOptions(opts ...any) RunOption {
	return func(runOpts *RunOptions) {
		runOpts.A2ARequestOptions = append(runOpts.A2ARequestOptions, opts...)
	}

}

// WithCustomAgentConfigs sets custom agent configurations.
// This allows passing agent-specific configurations at runtime without modifying the agent implementation.
//
// Parameters:
//   - configs: A map where the key is the agent type identifier and the value is the agent-specific config.
//     It's recommended to use the agent's defined RunOptionKey constant as the key and a typed options struct as the value.
//
// Usage:
//
//	// Example: Configure a custom LLM agent using its defined key and options struct
//	import customllm "your.module/agents/customllm"
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithCustomAgentConfigs(map[string]any{
//	        customllm.RunOptionKey: customllm.RunOptions{
//	            "custom-context": "context",
//	        },
//	    }),
//	)
//
//
//	// In your custom agent implementation, retrieve the config:
//	func (a *CustomLLMAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
//	    config := inv.GetCustomAgentConfig(RunOptionKey)
//	    if opts, ok := config.(RunOptions); ok {
//	        client := NewLLMClient(opts.APIKey, opts.Model, opts.Temperature)
//	        // Use the configuration...
//	    }
//	    // ...
//	}
//
// Note:
//   - This function creates a shallow copy of the configs map to prevent external modifications.
//   - The stored configuration should be treated as read-only. Do not modify it after retrieval.
func WithCustomAgentConfigs(configs map[string]any) RunOption {
	return func(opts *RunOptions) {
		if configs == nil {
			opts.CustomAgentConfigs = nil
			return
		}
		// Create a shallow copy to prevent external modifications
		copied := make(map[string]any, len(configs))
		for k, v := range configs {
			copied[k] = v
		}
		opts.CustomAgentConfigs = copied
	}
}

// RunOptions is the options for the Run method.
type RunOptions struct {
	// RuntimeState contains key-value pairs that will be merged into the initial state
	// for this specific run. This allows callers to pass dynamic parameters
	// (e.g., room ID, user context) without modifying the agent's base initial state.
	RuntimeState map[string]any

	// KnowledgeFilter contains metadata key-value pairs for the knowledge filter
	KnowledgeFilter map[string]any

	// KnowledgeConditionedFilter contains complex condition filter for the knowledge search
	KnowledgeConditionedFilter *searchfilter.UniversalFilterCondition

	// Messages allows callers to provide a full conversation history to Runner.
	// Runner will seed an empty Session with this history automatically and
	// then rely on Session events for subsequent turns. The content processor
	// ignores this field and reads only from Session events (or falls back to
	// `invocation.Message` when no events exist).
	Messages []model.Message

	// RequestID is the request id of the request.
	RequestID string

	// A2ARequestOptions contains A2A client request options that will be passed to
	// A2A agent's SendMessage and StreamMessage calls. This allows callers to pass
	// dynamic HTTP headers or other request-specific options for each run.
	//
	// Note: This field uses any type to avoid direct dependency on trpc-a2a-go/client package.
	// Users should pass client.RequestOption values (e.g., client.WithRequestHeader).
	// The a2aagent package will validate the option types at runtime.
	A2ARequestOptions []any

	// CustomAgentConfigs stores configurations for custom agents.
	// Key: agent type, Value: agent-specific config.
	CustomAgentConfigs map[string]any

	// Model is the model to use for this specific run.
	// If set, it temporarily overrides the agent's default model for this request only.
	// This allows per-request model switching without affecting other concurrent requests.
	Model model.Model

	// ModelName is the name of the model to use for this specific run.
	// The agent will look up the model by name from its registered models.
	// If both Model and ModelName are set, Model takes precedence.
	ModelName string

	// ToolFilter is a custom function to filter tools for this run.
	// If set, only tools for which the filter returns true will be available to the model.
	// If nil, all registered tools will be available (default behavior).
	//
	// The filter function receives:
	//   - ctx: The context with invocation information (use agent.InvocationFromContext)
	//   - tool: The tool being filtered
	//
	// This filtering happens at the request preparation stage, before sending to the model.
	// The model will only see the tool descriptions for tools that pass the filter.
	//
	// Note: Framework tools (knowledge_search, transfer_to_agent) are never filtered
	// and will always be included regardless of the filter function's return value.
	//
	// Example:
	//   agent.WithToolFilter(tool.NewIncludeToolNamesFilter("calculator", "time_tool"))
	//   agent.WithToolFilter(func(ctx context.Context, t tool.Tool) bool {
	//       return t.Declaration().Name == "calculator"
	//   })
	ToolFilter tool.FilterFunc
}

// NewInvocation create a new invocation
func NewInvocation(invocationOpts ...InvocationOptions) *Invocation {
	inv := &Invocation{
		InvocationID:  uuid.NewString(),
		noticeMu:      &sync.Mutex{},
		noticeChanMap: make(map[string]chan any),
	}

	for _, opt := range invocationOpts {
		opt(inv)
	}

	if inv.Branch == "" {
		inv.Branch = inv.AgentName
	}

	if inv.eventFilterKey == "" && inv.AgentName != "" {
		inv.eventFilterKey = inv.AgentName
	}

	return inv
}

// Clone clone a new invocation
func (inv *Invocation) Clone(invocationOpts ...InvocationOptions) *Invocation {
	if inv == nil {
		return nil
	}
	newInv := &Invocation{
		InvocationID:    uuid.NewString(),
		Session:         inv.Session,
		Message:         inv.Message,
		RunOptions:      inv.RunOptions,
		MemoryService:   inv.MemoryService,
		ArtifactService: inv.ArtifactService,
		noticeMu:        inv.noticeMu,
		noticeChanMap:   inv.noticeChanMap,
		eventFilterKey:  inv.eventFilterKey,
		parent:          inv,
	}

	for _, opt := range invocationOpts {
		opt(newInv)
	}

	if newInv.Branch != "" {
		// seted by WithInvocationBranch
	} else if inv.Branch != "" && newInv.AgentName != "" {
		newInv.Branch = inv.Branch + BranchDelimiter + newInv.AgentName
	} else if newInv.AgentName != "" {
		newInv.Branch = newInv.AgentName
	} else {
		newInv.Branch = inv.Branch
	}

	if newInv.eventFilterKey == "" && newInv.AgentName != "" {
		newInv.eventFilterKey = newInv.AgentName
	}

	return newInv
}

// GetEventFilterKey get event filter key.
func (inv *Invocation) GetEventFilterKey() string {
	if inv == nil {
		return ""
	}
	return inv.eventFilterKey
}

// InjectIntoEvent inject invocation information into event.
func InjectIntoEvent(inv *Invocation, e *event.Event) {
	if e == nil || inv == nil {
		return
	}

	e.RequestID = inv.RunOptions.RequestID
	if inv.parent != nil {
		e.ParentInvocationID = inv.parent.InvocationID
	}
	e.InvocationID = inv.InvocationID
	e.Branch = inv.Branch
	e.FilterKey = inv.GetEventFilterKey()
}

// EmitEvent inject invocation information into event and emit it to channel.
func EmitEvent(ctx context.Context, inv *Invocation, ch chan<- *event.Event,
	e *event.Event) error {
	if ch == nil || e == nil {
		return nil
	}
	InjectIntoEvent(inv, e)
	var agentName, requestID string
	if inv != nil {
		agentName = inv.AgentName
		requestID = inv.RunOptions.RequestID
	}
	log.Debugf("[agent.EmitEvent]queue monitoring:RequestID: %s channel capacity: %d, current length: %d, branch: %s, agent name:%s",
		requestID, cap(ch), len(ch), e.Branch, agentName)
	return event.EmitEvent(ctx, ch, e)
}

// GetAppendEventNoticeKey get append event notice key.
func GetAppendEventNoticeKey(eventID string) string {
	return AppendEventNoticeKeyPrefix + eventID
}

// SetState sets a value in the invocation state.
//
// This is a general-purpose key-value store scoped to the invocation lifecycle.
// It can be used by callbacks, middleware, or any invocation-scoped logic.
//
// Recommended key naming conventions:
//   - Agent callbacks: "agent:xxx" (e.g., "agent:start_time")
//   - Model callbacks: "model:xxx" (e.g., "model:start_time")
//   - Tool callbacks: "tool:<toolName>:<toolCallID>:xxx" (e.g., "tool:calculator:call_abc123:start_time")
//   - Middleware: "middleware:xxx" (e.g., "middleware:request_id")
//   - Custom logic: "custom:xxx" (e.g., "custom:user_context")
//
// Note: Tool callbacks should include tool call ID to support concurrent calls.
//
// Example:
//
//	inv.SetState("agent:start_time", time.Now())
//	inv.SetState("model:start_time", time.Now())
//	inv.SetState("tool:calculator:call_abc123:start_time", time.Now())
//	inv.SetState("middleware:request_id", "req-123")
//	inv.SetState("custom:user_context", userCtx)
func (inv *Invocation) SetState(key string, value any) {
	if inv == nil {
		return
	}
	inv.stateMu.Lock()
	defer inv.stateMu.Unlock()

	if inv.state == nil {
		inv.state = make(map[string]any)
	}
	inv.state[key] = value
}

// GetState retrieves a value from the invocation state.
//
// Returns the value and true if the key exists, or nil and false otherwise.
//
// Example:
//
//	if startTime, ok := inv.GetState("agent:start_time"); ok {
//	    duration := time.Since(startTime.(time.Time))
//	}
//	if startTime, ok := inv.GetState("tool:calculator:call_abc123:start_time"); ok {
//	    duration := time.Since(startTime.(time.Time))
//	}
func (inv *Invocation) GetState(key string) (any, bool) {
	if inv == nil {
		return nil, false
	}
	inv.stateMu.RLock()
	defer inv.stateMu.RUnlock()

	if inv.state == nil {
		return nil, false
	}
	value, ok := inv.state[key]
	return value, ok
}

// DeleteState removes a value from the invocation state.
//
// Example:
//
//	inv.DeleteState("agent:start_time")
//	inv.DeleteState("tool:calculator:call_abc123:start_time")
func (inv *Invocation) DeleteState(key string) {
	if inv == nil {
		return
	}
	inv.stateMu.Lock()
	defer inv.stateMu.Unlock()

	if inv.state != nil {
		delete(inv.state, key)
	}
}

// AddNoticeChannelAndWait add notice channel and wait it complete
func (inv *Invocation) AddNoticeChannelAndWait(ctx context.Context, key string, timeout time.Duration) error {
	ch := inv.AddNoticeChannel(ctx, key)
	if ch == nil {
		return fmt.Errorf("notice channel create failed for %s", key)
	}
	if timeout == WaitNoticeWithoutTimeout {
		// no timeout, maybe wait for ever
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	select {
	case <-ch:
	case <-time.After(timeout):
		return NewWaitNoticeTimeoutError(fmt.Sprintf("Timeout waiting for completion of event %s", key))
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// AddNoticeChannel add a new notice channel
func (inv *Invocation) AddNoticeChannel(ctx context.Context, key string) chan any {
	if inv == nil || inv.noticeMu == nil {
		log.Error("noticeMu is uninitialized, please use agent.NewInvocation or Clone method to create Invocation")
		return nil
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	if ch, ok := inv.noticeChanMap[key]; ok {
		return ch
	}

	ch := make(chan any)
	if inv.noticeChanMap == nil {
		inv.noticeChanMap = make(map[string]chan any)
	}
	inv.noticeChanMap[key] = ch

	return ch
}

// NotifyCompletion notify completion signal to waiting task
func (inv *Invocation) NotifyCompletion(ctx context.Context, key string) error {
	if inv == nil || inv.noticeMu == nil {
		log.Error("noticeMu is uninitialized, please use agent.NewInvocation or Clone method to create Invocation")
		return fmt.Errorf("noticeMu is uninitialized, please use agent.NewInvocation or Clone method to create Invocation key:%s", key)
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	ch, ok := inv.noticeChanMap[key]
	if !ok {
		return fmt.Errorf("notice channel not found for %s", key)
	}

	close(ch)
	delete(inv.noticeChanMap, key)

	return nil
}

// CleanupNotice cleanup all notice channel
// The 'Invocation' instance created via the NewInvocation method ​​should be disposed​​
// upon completion to prevent resource leaks.
func (inv *Invocation) CleanupNotice(ctx context.Context) {
	if inv == nil || inv.noticeMu == nil {
		log.Error("noticeMu is uninitialized, please use agent.NewInvocation or Clone method to create Invocation")
		return
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	for _, ch := range inv.noticeChanMap {
		close(ch)
	}
	inv.noticeChanMap = nil
}

// GetCustomAgentConfig retrieves configuration for a specific custom agent type.
//
// Parameters:
//   - agentKey: The agent type identifier (typically the agent's RunOptionKey constant)
//
// Returns:
//   - The configuration value if found, nil otherwise
//
// Usage:
//
//	func (a *CustomLLMAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
//	    config := inv.GetCustomAgentConfig(RunOptionKey)
//	    if opts, ok := config.(RunOptions); ok {
//	        client := NewLLMClient(opts.APIKey, opts.Model)
//	        // ...
//	    }
//	}
//
// Note: The returned config should be treated as read-only. Do not modify it.
func (inv *Invocation) GetCustomAgentConfig(agentKey string) any {
	if inv == nil || inv.RunOptions.CustomAgentConfigs == nil {
		return nil
	}
	return inv.RunOptions.CustomAgentConfigs[agentKey]
}
