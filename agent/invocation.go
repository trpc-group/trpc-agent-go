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
	"trpc.group/trpc-go/trpc-agent-go/internal/util"
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

	// flusherStateKey is the invocation state key used by flush.Attach.
	flusherStateKey = "__flush_session__"
	// barrierStateKey is the invocation state key used by internal barrier flag.
	barrierStateKey = "__graph_barrier__"
	// appenderStateKey is the invocation state key used by internal appender
	// attachment (see internal/state/appender).
	appenderStateKey = "__append_event__"
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

	// noticeChannels is used to signal when events are written to the session.
	noticeChannels map[string]chan any
	noticeMu       *sync.Mutex

	// eventFilterKey is used to filter events for flow or agent
	eventFilterKey string

	// parent is the parent invocation, if any
	parent *Invocation

	// state stores invocation-scoped state data (lazy initialized).
	// Can be used by callbacks, middleware, or any invocation-scoped logic.
	state   map[string]any
	stateMu sync.RWMutex

	// MaxLLMCalls is an optional upper bound on the number of LLM calls
	// allowed for this invocation. When the value is:
	//   - > 0: the limit is enforced for this invocation.
	//   - <= 0: no limit is applied (default, preserves existing behavior).
	//
	// Typical usage:
	//   - LLMAgent copies its per-agent limits into these fields in setupInvocation.
	//   - Other agent implementations may set them explicitly when constructing
	//     invocations. If left at zero, IncLLMCallCount/IncToolIteration are no-ops.
	MaxLLMCalls int

	// MaxToolIterations is an optional upper bound on how many tool-call
	// iterations are allowed for this invocation. A "tool iteration" is defined
	// as an assistant response that contains tool calls and triggers the
	// FunctionCallResponseProcessor. When the value is:
	//   - > 0: the limit is enforced for this invocation.
	//   - <= 0: no limit is applied (default, preserves existing behavior).
	MaxToolIterations int

	// timingInfo stores timing information for the first LLM call in this invocation.
	timingInfo *model.TimingInfo

	// llmCallCount tracks how many LLM calls have been made in this invocation.
	// This is used together with MaxLLMCalls to enforce a per-invocation limit.
	// Note: counters are invocation-scoped. When child invocations are created
	// via Clone (for example, in transfer_to_agent or AgentTool), the counters
	// start from zero for each invocation.
	llmCallCount int

	// toolIterationCount tracks how many tool call iterations have been processed
	// in this invocation. This is used together with MaxToolIterations
	// to guard against unbounded tool_call -> LLM -> tool_call loops.
	toolIterationCount int
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

// WithAgent sets the agent instance for this run only.
func WithAgent(a Agent) RunOption {
	return func(opts *RunOptions) {
		opts.Agent = a
	}
}

// WithAgentByName sets the agent name that should be resolved for this run.
func WithAgentByName(name string) RunOption {
	return func(opts *RunOptions) {
		opts.AgentByName = name
	}
}

// GetRuntimeStateValue retrieves a typed value from the runtime state.
//
// Returns the typed value and true if the key exists and the type matches,
// or the zero value and false otherwise.
//
// Example:
//
//	if userID, ok := GetRuntimeStateValue[string](&inv.RunOptions, "user_id"); ok {
//	    log.Printf("User ID: %s", userID)
//	}
//	if roomID, ok := GetRuntimeStateValue[int](&inv.RunOptions, "room_id"); ok {
//	    log.Printf("Room ID: %d", roomID)
//	}
func GetRuntimeStateValue[T any](opts *RunOptions, key string) (T, bool) {
	var zero T
	if opts == nil || opts.RuntimeState == nil {
		return zero, false
	}
	val, ok := opts.RuntimeState[key]
	if !ok {
		return zero, false
	}
	typedVal, ok := val.(T)
	if !ok {
		return zero, false
	}
	return typedVal, true
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

// WithResume enables or disables resume mode for this run.
// When enabled, flows like llmflow may inspect the existing Session history
// and resume unfinished work (for example, executing pending tool calls)
// before issuing a new model call.
func WithResume(enabled bool) RunOption {
	return func(opts *RunOptions) {
		opts.Resume = enabled
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

	// Resume indicates whether this run should attempt to resume from existing
	// session context before making a new model call. When true, flows may
	// inspect the latest session events (for example, assistant messages with
	// pending tool calls) and complete unfinished work prior to issuing a new
	// LLM request.
	Resume bool

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

	// Agent overrides the runner's default agent for this run.
	Agent Agent

	// AgentByName instructs the runner to resolve an agent by name for this run.
	AgentByName string

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
		InvocationID:   uuid.NewString(),
		noticeMu:       &sync.Mutex{},
		noticeChannels: make(map[string]chan any),
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
		noticeChannels:  inv.noticeChannels,
		eventFilterKey:  inv.eventFilterKey,
		parent:          inv,
		state:           inv.cloneState(),
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

func (inv *Invocation) cloneState() map[string]any {
	if inv == nil || inv.state == nil {
		return nil
	}
	inv.stateMu.RLock()
	defer inv.stateMu.RUnlock()
	copied := make(map[string]any)
	if holder, ok := inv.state[flusherStateKey]; ok {
		copied[flusherStateKey] = holder
	}
	if barrier, ok := inv.state[barrierStateKey]; ok {
		copied[barrierStateKey] = barrier
	}
	if holder, ok := inv.state[appenderStateKey]; ok {
		copied[appenderStateKey] = holder
	}
	return copied
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
	log.Tracef(
		"[agent.EmitEvent]queue monitoring:RequestID: %s channel capacity: "+
			"%d, current length: %d, branch: %s, agent name:%s",
		requestID,
		cap(ch),
		len(ch),
		e.Branch,
		agentName,
	)
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

// GetStateValue retrieves a typed value from the invocation state.
//
// Returns the typed value and true if the key exists and the type matches,
// or the zero value and false otherwise.
//
// Example:
//
//	if startTime, ok := GetStateValue[time.Time](inv, "agent:start_time"); ok {
//	    duration := time.Since(startTime)
//	}
//	if requestID, ok := GetStateValue[string](inv, "middleware:request_id"); ok {
//	    log.Printf("Request ID: %s", requestID)
//	}
func GetStateValue[T any](inv *Invocation, key string) (T, bool) {
	var zero T
	if inv == nil {
		return zero, false
	}
	inv.stateMu.RLock()
	defer inv.stateMu.RUnlock()

	return util.GetMapValue[string, T](inv.state, key)
}

// GetOrCreateTimingInfo gets or creates timing info for this invocation.
// Only the first LLM call will create and populate timing info; subsequent calls reuse it.
// This ensures timing metrics only reflect the first LLM call in scenarios with multiple calls (e.g., tool calls).
func (inv *Invocation) GetOrCreateTimingInfo() *model.TimingInfo {
	if inv == nil {
		return nil
	}
	if inv.timingInfo == nil {
		inv.timingInfo = &model.TimingInfo{}
	}
	return inv.timingInfo
}

// IncLLMCallCount increments the LLM call counter for this invocation and
// enforces the optional MaxLLMCalls limit. When the limit is not set or
// non-positive, no restriction is applied. When the limit is exceeded, a
// StopError is returned so callers can terminate the flow early.
func (inv *Invocation) IncLLMCallCount() error {
	if inv == nil {
		return nil
	}
	limit := inv.MaxLLMCalls
	if limit <= 0 {
		// No limit configured, preserve existing behavior.
		return nil
	}
	inv.llmCallCount++
	if inv.llmCallCount > limit {
		return NewStopError(
			fmt.Sprintf("max LLM calls (%d) exceeded", limit),
		)
	}
	return nil
}

// IncToolIteration increments the tool iteration counter and reports whether
// the MaxToolIterations limit has been exceeded. A "tool iteration" is
// defined as an assistant response that contains tool calls and triggers the
// FunctionCallResponseProcessor. When the limit is not set or non-positive,
// this method always returns false, preserving existing behavior.
func (inv *Invocation) IncToolIteration() bool {
	if inv == nil {
		return false
	}
	limit := inv.MaxToolIterations
	if limit <= 0 {
		// No limit configured, preserve existing behavior.
		return false
	}
	inv.toolIterationCount++
	return inv.toolIterationCount > limit
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
		log.InfofContext(
			ctx,
			"[AddNoticeChannelAndWait]: Wait for notification message "+
				"timeout. key: %s, timeout: %d(s)",
			key,
			int64(timeout/time.Second),
		)
		return NewWaitNoticeTimeoutError(fmt.Sprintf("Timeout waiting for completion of event %s", key))
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// AddNoticeChannel add a new notice channel
func (inv *Invocation) AddNoticeChannel(ctx context.Context, key string) chan any {
	if inv == nil || inv.noticeMu == nil {
		log.ErrorContext(
			ctx,
			"noticeMu is uninitialized, please use agent.NewInvocation or "+
				"Clone method to create Invocation",
		)
		return nil
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	if ch, ok := inv.noticeChannels[key]; ok {
		return ch
	}

	ch := make(chan any)
	if inv.noticeChannels == nil {
		inv.noticeChannels = make(map[string]chan any)
	}
	inv.noticeChannels[key] = ch

	return ch
}

// NotifyCompletion notify completion signal to waiting task
func (inv *Invocation) NotifyCompletion(ctx context.Context, key string) error {
	if inv == nil || inv.noticeMu == nil {
		log.ErrorContext(
			ctx,
			"noticeMu is uninitialized, please use agent.NewInvocation or "+
				"Clone method to create Invocation",
		)
		return fmt.Errorf(
			"noticeMu is uninitialized, please use agent.NewInvocation or "+
				"Clone method to create Invocation key:%s",
			key,
		)
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	ch, ok := inv.noticeChannels[key]
	// channel not found, create a new one and close it.
	// May involve notification followed by waiting.
	if !ok {
		ch = make(chan any)
		if inv.noticeChannels == nil {
			inv.noticeChannels = make(map[string]chan any)
		}
		inv.noticeChannels[key] = ch
		close(ch)
		return nil
	}

	// channel found, close it if it's not closed
	select {
	case _, isOpen := <-ch:
		if isOpen {
			close(ch)
		}
	default:
		close(ch)
	}

	return nil
}

// CleanupNotice cleanup all notice channel
// The 'Invocation' instance created via the NewInvocation method ​​should be disposed​​
// upon completion to prevent resource leaks.
func (inv *Invocation) CleanupNotice(ctx context.Context) {
	if inv == nil || inv.noticeMu == nil {
		log.ErrorContext(
			ctx,
			"noticeMu is uninitialized, please use agent.NewInvocation or "+
				"Clone method to create Invocation",
		)
		return
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	for _, ch := range inv.noticeChannels {
		select {
		case _, isOpen := <-ch:
			if isOpen {
				close(ch)
			}
		default:
			close(ch)
		}
	}
	inv.noticeChannels = nil
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
