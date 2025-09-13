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
	"trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// InitVersion is the initial version of the event format.
	InitVersion int = iota // 0

	// CurrentVersion is the current version of the event format.
	CurrentVersion // 1

	// EmitWithoutTimeout is the default timeout for emitting events.
	EmitWithoutTimeout = 0 * time.Second
)

// Event represents an event in conversation between agents and users.
type Event struct {
	// Response is the base struct for all LLM response functionality.
	*model.Response

	// InvocationID is the invocation ID of the event.
	InvocationID string `json:"invocationId"`

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
	filterKey string

	// version for handling version compatibility issues.
	version int
}

// eventJSON is a temporary struct to hold the JSON representation of an event.
// Used only in Event serialization/deserialization scenarios.
type eventJSON struct {
	// Event
	Event
	// Version is used to handle version compatibility issues.
	Version int `json:"version"`

	// FilterKey is used to handle hierarchical event filtering.
	FilterKey string `json:"filterKey"`
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
	clone.filterKey = e.GetFilterKey()
	clone.version = CurrentVersion
	clone.Branch = e.Branch
	clone.Tag = e.Tag
	clone.ID = uuid.NewString()
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

// GetFilterKey returns the filter key for the event.
func (e *Event) GetFilterKey() string {
	if e == nil {
		return ""
	}

	if e.version != CurrentVersion {
		return e.Branch
	}

	return e.filterKey
}

// Filter checks if the event matches the specified filter key.
func (e *Event) Filter(filterKey string) bool {
	if e == nil {
		return true
	}

	eFilterKey := e.filterKey
	if e.version != CurrentVersion {
		eFilterKey = e.Branch
	}

	if filterKey == "" || eFilterKey == "" {
		return true
	}

	filterKey += "/"
	eFilterKey = eFilterKey + "/"
	return strings.HasPrefix(filterKey, eFilterKey) || strings.HasPrefix(eFilterKey, filterKey)
}

// Marshal serializes the event to JSON with error
func (e *Event) Marshal() ([]byte, error) {
	if e == nil {
		return json.Marshal(e)
	}

	eJSON := eventJSON{
		Event:     *e,
		Version:   e.version,
		FilterKey: e.filterKey,
	}

	return json.Marshal(eJSON)
}

// Unmarshal deserializes the event from JSON with error
func (e *Event) Unmarshal(data []byte) error {
	if e == nil {
		return nil
	}

	eJSON := eventJSON{}
	if err := json.Unmarshal(data, &eJSON); err != nil {
		return err
	}
	*e = eJSON.Event
	e.version = eJSON.Version
	e.filterKey = eJSON.FilterKey

	return nil
}

// New creates a new Event with generated ID and timestamp.
func New(invocationID, author string, opts ...Option) *Event {
	e := &Event{
		Response:     &model.Response{},
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		InvocationID: invocationID,
		Author:       author,
		version:      CurrentVersion,
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
	return New(
		invocationID, author,
		opts...,
	)
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

// EmitEventToChannel sends an event to the channel without timeout.
func EmitEventToChannel(ctx context.Context, ch chan<- *Event, e *Event) error {
	return EmitEventToChannelWithTimeout(ctx, ch, e, EmitWithoutTimeout)
}

// EmitEventToChannelWithTimeout sends an event to the channel with optional timeout.
func EmitEventToChannelWithTimeout(ctx context.Context, ch chan<- *Event,
	e *Event, timeout time.Duration) error {
	if e == nil {
		return nil
	}

	if timeout == EmitWithoutTimeout {
		select {
		case ch <- e:
			log.Debugf("EmitEventToChannelWithTimeout: event sent, event: %+v", *e)
		case <-ctx.Done():
			log.Warnf("EmitEventToChannelWithTimeout: context cancelled, event: %+v", *e)
			return ctx.Err()
		}
		return nil
	}

	select {
	case ch <- e:
		log.Debugf("EmitEventToChannelWithTimeout: event sent, event: %+v", *e)
	case <-ctx.Done():
		log.Warnf("EmitEventToChannelWithTimeout: context cancelled, event: %+v", *e)
		return ctx.Err()
	case <-time.After(timeout):
		log.Warnf("EmitEventToChannelWithTimeout: timeout, event: %+v", *e)
		return DefaultEmitTimeoutErr
	}
	return nil
}
