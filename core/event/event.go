// Package event provides the event system for agent communication.
package event

import (
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

// Event represents an event in conversation between agents and users.
type Event struct {
	// Embed model.Response for all LLM response functionality.
	model.Response

	// Event-specific fields
	InvocationID string `json:"invocationId"`
	Author       string `json:"author"`

	// Override the ID and Timestamp with Event-specific behavior
	ID        string  `json:"id"`
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
