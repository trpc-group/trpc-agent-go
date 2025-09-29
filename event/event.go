//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package event provides the event system for agent communication.
package event

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// InitVersion is the initial version of the event format.
	InitVersion int = iota // 0

	// CurrentVersion is the current version of the event format.
	CurrentVersion
)

const (
	// EmitWithoutTimeout is the default timeout for emitting events.
	EmitWithoutTimeout = 0 * time.Second

	// FilterKeyDelimiter is the delimiter for hierarchical event filtering.
	FilterKeyDelimiter = "/"
)

// Event represents an event in conversation between agents and users.
type Event struct {
	// Response is the base struct for all LLM response functionality.
	*model.Response

	// RequestID is the request ID of the event.
	RequestID string `json:"requestID,omitempty"`

	// InvocationID is the invocation ID of the event.
	InvocationID string `json:"invocationId"`

	// ParentInvocationID is the parent invocation ID of the event.
	ParentInvocationID string `json:"parentInvocationId,omitempty"`

	// Author is the author of the event.
	Author string `json:"author"`

	// ID is the unique identifier of the event.
	ID string `json:"id"`

	// Timestamp is the timestamp of the event.
	Timestamp time.Time `json:"timestamp"`

	// Branch records agent execution chain information.
	// In multi-agent mode, this is useful for tracing agent execution trajectories.
	Branch string `json:"branch,omitempty"`

	// Tag Uses tags to annotate events with business-specific labels.
	Tag string `json:"tag,omitempty"`

	// RequiresCompletion indicates if this event needs completion signaling.
	RequiresCompletion bool `json:"requiresCompletion,omitempty"`

	// LongRunningToolIDs is the Set of ids of the long running function calls.
	// Agent client will know from this field about which function call is long running.
	// only valid for function call event
	LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`

	// StateDelta contains state changes to be applied to the session.
	StateDelta map[string][]byte `json:"stateDelta,omitempty"`

	// StructuredOutput carries a typed, in-memory structured output payload.
	// This is not serialized and is meant for immediate consumer access.
	StructuredOutput any `json:"-"`

	// Actions carry flow-level hints that influence how this event is treated
	// by the runner/flow (e.g., skip summarization after a tool response).
	Actions *EventActions `json:"actions,omitempty"`

	// filterKey is identifier for hierarchical event filtering.
	FilterKey string `json:"filterKey,omitempty"`

	// version for handling version compatibility issues.
	Version int `json:"version,omitempty"`
}

// EventActions represents optional actions/hints attached to an event.
// These are used by the flow to adjust control behavior without
// overloading Response fields.
type EventActions struct {
	// SkipSummarization indicates that the flow should not run an
	// additional summarization step after this event. Commonly used
	// for final tool.response events returned by AgentTool.
	SkipSummarization bool `json:"skipSummarization,omitempty"`
}

// Clone creates a deep copy of the event.
func (e *Event) Clone() *Event {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Response = e.Response.Clone()
	clone.LongRunningToolIDs = make(map[string]struct{})
	clone.Version = CurrentVersion
	clone.ID = uuid.NewString()
	if e.Version != CurrentVersion {
		clone.FilterKey = e.Branch
	}
	for k := range e.LongRunningToolIDs {
		clone.LongRunningToolIDs[k] = struct{}{}
	}
	if e.StateDelta != nil {
		clone.StateDelta = make(map[string][]byte)
		for k, v := range e.StateDelta {
			clone.StateDelta[k] = make([]byte, len(v))
			copy(clone.StateDelta[k], v)
		}
	}
	if e.Actions != nil {
		clone.Actions = &EventActions{
			SkipSummarization: e.Actions.SkipSummarization,
		}
	}
	return &clone
}

// Filter checks if the event matches the specified filter key.
func (e *Event) Filter(filterKey string) bool {
	if e == nil {
		return false
	}

	eFilterKey := e.FilterKey
	if e.Version != CurrentVersion {
		eFilterKey = e.Branch
	}

	if filterKey == "" || eFilterKey == "" {
		return true
	}

	filterKey += FilterKeyDelimiter
	eFilterKey = eFilterKey + FilterKeyDelimiter
	return strings.HasPrefix(filterKey, eFilterKey) || strings.HasPrefix(eFilterKey, filterKey)
}

// New creates a new Event with generated ID and timestamp.
func New(invocationID, author string, opts ...Option) *Event {
	e := &Event{
		Response:     &model.Response{},
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		InvocationID: invocationID,
		Author:       author,
		Version:      CurrentVersion,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// NewErrorEvent creates a new error Event with the specified error details.
// This provides a clean way to create error events without manual field assignment.
func NewErrorEvent(invocationID, author, errorType, errorMessage string,
	opts ...Option) *Event {
	rsp := &model.Response{
		Object: model.ObjectTypeError,
		Done:   true,
		Error: &model.ResponseError{
			Type:    errorType,
			Message: errorMessage,
		},
	}
	opts = append(opts, WithResponse(rsp))
	return New(invocationID, author, opts...)
}

// NewResponseEvent creates a new Event from a model Response.
func NewResponseEvent(invocationID, author string, response *model.Response,
	opts ...Option) *Event {
	opts = append(opts, WithResponse(response))
	return New(invocationID, author, opts...)
}

// DefaultEmitTimeoutErr is the default error returned when a wait notice times out.
var DefaultEmitTimeoutErr = NewEmitEventTimeoutError("emit event timeout.")

// EmitEventTimeoutError represents an error that signals the emit event timeout.
type EmitEventTimeoutError struct {
	// Message contains the stop reason
	Message string
}

// Error implements the error interface.
func (e *EmitEventTimeoutError) Error() string {
	return e.Message
}

// AsEmitEventTimeoutError checks if an error is a EmitEventTimeoutError using errors.As.
func AsEmitEventTimeoutError(err error) (*EmitEventTimeoutError, bool) {
	var waitNoticeTimeoutErr *EmitEventTimeoutError
	ok := errors.As(err, &waitNoticeTimeoutErr)
	return waitNoticeTimeoutErr, ok
}

// NewEmitEventTimeoutError creates a new EmitEventTimeoutError with the given message.
func NewEmitEventTimeoutError(message string) *EmitEventTimeoutError {
	return &EmitEventTimeoutError{Message: message}
}

// EmitEvent sends an event to the channel without timeout.
func EmitEvent(ctx context.Context, ch chan<- *Event, e *Event) error {
	return EmitEventWithTimeout(ctx, ch, e, EmitWithoutTimeout)
}

// EmitEventWithTimeout sends an event to the channel with optional timeout.
func EmitEventWithTimeout(ctx context.Context, ch chan<- *Event,
	e *Event, timeout time.Duration) error {
	if e == nil || ch == nil {
		return nil
	}

	log.Debugf("[EmitEventWithTimeout]queue monitoring: RequestID: %s, channel capacity: %d, current length: %d, branch: %s",
		e.RequestID, cap(ch), len(ch), e.Branch)

	if timeout == EmitWithoutTimeout {
		select {
		case ch <- e:
			log.Debugf("EmitEventWithTimeout: event sent, event: %+v", *e)
		case <-ctx.Done():
			log.Warnf("EmitEventWithTimeout: context cancelled, event: %+v", *e)
			return ctx.Err()
		}
		return nil
	}

	select {
	case ch <- e:
		log.Debugf("EmitEventWithTimeout: event sent, event: %+v", *e)
	case <-ctx.Done():
		log.Warnf("EmitEventWithTimeout: context cancelled, event: %+v", *e)
		return ctx.Err()
	case <-time.After(timeout):
		log.Warnf("EmitEventWithTimeout: timeout, event: %+v", *e)
		return DefaultEmitTimeoutErr
	}
	return nil
}
