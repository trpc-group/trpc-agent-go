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
)

// TransferInfo contains information about a pending agent transfer.
type TransferInfo struct {
	// TargetAgentName is the name of the agent to transfer control to.
	TargetAgentName string
	// Message is the message to send to the target agent.
	Message string
	// EndInvocation indicates whether to end the current invocation after transfer.
	EndInvocation bool
}

// Invocation represents the context for a flow execution.
type Invocation struct {
	// Agent is the agent that is being invoked.
	Agent Agent
	// AgentName is the name of the agent that is being invoked.
	AgentName string
	// InvocationID is the ID of the invocation.
	InvocationID string
	// Branch is the branch identifier for hierarchical event filtering.
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
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *Callbacks
	// ModelCallbacks contains callbacks for model operations.
	ModelCallbacks *model.Callbacks
	// ToolCallbacks contains callbacks for tool operations.
	ToolCallbacks *tool.Callbacks

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
}

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

// WithMessages sets the initial conversation history for this run.
// When provided, the content processor will prefer these messages and
// will not derive messages from session events or the single
// `invocation.Message` to prevent duplication. The messages should be
// in chronological order (system -> user/assistant alternating).
func WithMessages(messages []model.Message) RunOption {
	return func(opts *RunOptions) {
		opts.Messages = messages
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

	// Messages allows callers to provide a full conversation history
	// directly to the agent invocation without relying on the session
	// service. When provided, the content processor will prefer these
	// messages and skip deriving content from session events or the
	// single `invocation.Message` to avoid duplication.
	Messages []model.Message
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

	return inv
}

// Clone clone a new invocation
func (inv *Invocation) Clone(invocationOpts ...InvocationOptions) *Invocation {
	newInv := &Invocation{
		InvocationID:    inv.InvocationID,
		Branch:          inv.Branch,
		Session:         inv.Session,
		Message:         inv.Message,
		RunOptions:      inv.RunOptions,
		ArtifactService: inv.ArtifactService,
		noticeMu:        inv.noticeMu,
		noticeChanMap:   inv.noticeChanMap,
	}

	for _, opt := range invocationOpts {
		opt(newInv)
	}

	return newInv
}

// AddNoticeChannelAndWait add notice channel and wait it complete
func (inv *Invocation) AddNoticeChannelAndWait(ctx context.Context, key string, timeout time.Duration) error {
	if timeout == WaitNoticeWithoutTimeout {
		// no timeout, maybe wait for ever
		select {
		case <-inv.AddNoticeChannel(ctx, key):
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	select {
	case <-inv.AddNoticeChannel(ctx, key):
	case <-time.After(timeout):
		return NewWaitNoticeTimeoutError(fmt.Sprintf("Timeout waiting for completion of event %s", key))
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// AddNoticeChannel add a new notice channel
func (inv *Invocation) AddNoticeChannel(ctx context.Context, key string) chan any {
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
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	ch, ok := inv.noticeChanMap[key]
	if !ok {
		return fmt.Errorf("notice channel not found for %s.", key)
	}

	close(ch)
	delete(inv.noticeChanMap, key)

	return nil
}

// CleanupNotice cleanup all notice channel
// The 'Invocation' instance created via the NewInvocation method ​​should be disposed​​
// upon completion to prevent resource leaks.
func (inv *Invocation) CleanupNotice(ctx context.Context) {
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	for _, ch := range inv.noticeChanMap {
		close(ch)
	}
	inv.noticeChanMap = nil
}
