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
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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

// WithKnowledgeFilter sets the knowledge filter for the RunOptions.
func WithKnowledgeFilter(filter map[string]any) RunOption {
	return func(opts *RunOptions) {
		opts.KnowledgeFilter = filter
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

	// KnowledgeFilter contains key-value pairs that will be merged into the knowledge filter
	KnowledgeFilter map[string]any

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
