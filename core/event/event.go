// Package event provides the event system for agent communication.
package event

import (
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
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
			Object: "error",
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
