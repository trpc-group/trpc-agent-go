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
	model.Response

	// InvocationID is the invocation ID of the event.
	InvocationID string `json:"invocationId"`

	// Author is the author of the event.
	Author string `json:"author"`

	// ID is the unique identifier of the event.
	ID string `json:"id"`

	// Timestamp is the timestamp of the event.
	Timestamp float64 `json:"timestamp"`
}

// Option is a function that can be used to configure the Event.
type Option func(*Event)

// NewEvent creates a new Event with generated ID and timestamp.
func NewEvent(invocationID, author string, opts ...Option) *Event {
	e := &Event{
		ID:           uuid.New().String(),
		Timestamp:    float64(time.Now().Unix()),
		InvocationID: invocationID,
		Author:       author,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}
