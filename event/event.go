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
	"encoding/json"
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

	// TagDelimiter is the delimiter for event tags.
	TagDelimiter = ";"
)

const (
	// CodeExecutionTag is the tag value for code execution code event.
	CodeExecutionTag = "code_execution_code"

	// CodeExecutionResultTag is the tag value for code execution result event.
	CodeExecutionResultTag = "code_execution_result"

	// TransferTag is the tag for transfer event.
	TransferTag = "transfer"
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

// ContainsTag checks if the event contains the specified tag.
func (e *Event) ContainsTag(tag string) bool {
	if e.Tag == "" {
		return false
	}

	tags := strings.Split(e.Tag, TagDelimiter)
	for _, t := range tags {
		if strings.TrimSpace(t) == strings.TrimSpace(tag) {
			return true
		}
	}
	return false
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

// IsRunnerCompletion reports whether this event is the terminal completion
// event emitted by Runner. It is the most reliable signal that the entire
// run has finished (regardless of the specific Agent implementation), and the
// recommended condition to stop consuming the event stream.
func (e *Event) IsRunnerCompletion() bool {
	if e == nil || e.Response == nil {
		return false
	}
	return e.Done && e.Object == model.ObjectTypeRunnerCompletion
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

	// If the context is already cancelled, prefer returning immediately
	// rather than attempting to send. This avoids a racy select where both
	// the send and the ctx.Done() cases are ready, which could otherwise
	// result in emitting an event after cancellation.
	if err := ctx.Err(); err != nil {
		log.WarnfContext(
			ctx,
			"EmitEventWithTimeout: context cancelled, event: %+v",
			*e,
		)
		return err
	}

	log.Tracef("[EmitEventWithTimeout]queue monitoring: RequestID: %s, channel capacity: %d, current length: %d, branch: %s",
		e.RequestID, cap(ch), len(ch), e.Branch)

	if timeout == EmitWithoutTimeout {
		select {
		case ch <- e:
			log.Tracef("EmitEventWithTimeout: event sent, event: %+v", *e)
		case <-ctx.Done():
			log.WarnfContext(
				ctx,
				"EmitEventWithTimeout: context cancelled, event: %+v",
				*e,
			)
			return ctx.Err()
		}
		return nil
	}

	select {
	case ch <- e:
		log.Tracef("EmitEventWithTimeout: event sent, event: %+v", *e)
	case <-ctx.Done():
		log.WarnfContext(
			ctx,
			"EmitEventWithTimeout: context cancelled, event: %+v",
			*e,
		)
		return ctx.Err()
	case <-time.After(timeout):
		log.WarnfContext(
			ctx,
			"EmitEventWithTimeout: timeout, event: %+v",
			*e,
		)
		return DefaultEmitTimeoutErr
	}
	return nil
}

// MarshalJSON implements json.Marshaler and produces a format that
// preserves legacy flattened fields while also embedding minimal
// response metadata (ID/timestamp) under the dedicated "response" key.
func (e Event) MarshalJSON() ([]byte, error) {
	payload := jsonEvent{
		eventNoMethods: (*eventNoMethods)(&e),
	}
	if e.Response != nil {
		payload.Response = &responseMeta{
			ID:        e.Response.ID,
			Timestamp: e.Response.Timestamp,
		}
	}
	return json.Marshal(payload)
}

// UnmarshalJSON implements json.Unmarshaler by accepting both legacy flattened
// payloads and the new nested-response representation, preferring the nested
// response when present.
func (e *Event) UnmarshalJSON(data []byte) error {
	// First parse the flat structure.
	var flat eventNoMethods
	if err := json.Unmarshal(data, &flat); err != nil {
		return err
	}
	*e = Event(flat)
	// Then try to read nested metadata.
	var nested struct {
		Response *responseMeta `json:"response,omitempty"`
	}
	// Tolerate nested part failure, it does not affect the overall failure, preserve the flat fields.
	if err := json.Unmarshal(data, &nested); err != nil {
		log.Warnf("unmarshal response: %v", err)
		return nil
	}
	if nested.Response != nil {
		if e.Response == nil {
			e.Response = &model.Response{}
		}
		e.Response.ID = nested.Response.ID
		e.Response.Timestamp = nested.Response.Timestamp
	}
	return nil
}

// eventNoMethods is the alias of Event for avoiding recursive calls of custom MarshalJSON/UnmarshalJSON.
type eventNoMethods Event

// responseMeta is the minimal response metadata for JSON nested.
type responseMeta struct {
	ID        string    `json:"id,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// jsonEvent is the final JSON structure to be output/read,
// including flat event fields and nested response metadata.
type jsonEvent struct {
	*eventNoMethods
	Response *responseMeta `json:"response,omitempty"`
}
