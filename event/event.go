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
//

// Package event provides the event system for agent communication.
package event

import (
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"
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

	// Branch is the branch identifier for hierarchical event filtering.
	Branch string `json:"branch,omitempty"`

	// RequiresCompletion indicates if this event needs completion signaling.
	RequiresCompletion bool `json:"requiresCompletion,omitempty"`

	// CompletionID is used for completion signaling of this event.
	CompletionID string `json:"completionId,omitempty"`

	// LongRunningToolIDs is the Set of ids of the long running function calls.
	// Agent client will know from this field about which function call is long running.
	// only valid for function call event
	LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`
}

// Option is a function that can be used to configure the Event.
type Option func(*Event)

// New creates a new Event with generated ID and timestamp.
func New(invocationID, author string, opts ...Option) *Event {
	e := &Event{
		Response:     &model.Response{},
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		InvocationID: invocationID,
		Author:       author,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// NewErrorEvent creates a new error Event with the specified error details.
// This provides a clean way to create error events without manual field assignment.
func NewErrorEvent(invocationID, author, errorType, errorMessage string) *Event {
	return &Event{
		Response: &model.Response{
			Object: model.ObjectTypeError,
			Done:   true,
			Error: &model.ResponseError{
				Type:    errorType,
				Message: errorMessage,
			},
		},
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		InvocationID: invocationID,
		Author:       author,
	}
}

// NewResponseEvent creates a new Event from a model Response.
func NewResponseEvent(invocationID, author string, response *model.Response) *Event {
	return &Event{
		Response:     response,
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		InvocationID: invocationID,
		Author:       author,
	}
}
