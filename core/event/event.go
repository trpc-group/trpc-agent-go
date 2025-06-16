// Package event provides the event system for agent communication.
package event

import (
	"time"

	"github.com/google/uuid"
)

// Event represents an event in conversation between agents and users.
// This is a complete redesign with no backward compatibility.
type Event struct {
	// Core identification
	ID        string  `json:"id"`
	Timestamp float64 `json:"timestamp"`

	// Content-first design for rich message structure
	Author  string   `json:"author"`  // "user" | agent name
	Content *Content `json:"content"` // Required - all events have content

	// Minimal metadata for flexibility
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Event creation (simplified)

// NewEvent creates a new event with the given author and content.
func NewEvent(author string, content *Content) *Event {
	return &Event{
		ID:        uuid.New().String(),
		Timestamp: float64(time.Now().Unix()),
		Author:    author,
		Content:   content,
		Metadata:  make(map[string]interface{}),
	}
}

// Convenience constructors

// NewTextEvent creates a new event with text content.
func NewTextEvent(author, text string) *Event {
	return NewEvent(author, NewTextContent(text))
}

// NewToolCallEvent creates a new event with a function call.
func NewToolCallEvent(author string, functionCall *FunctionCall) *Event {
	content := &Content{
		Parts: []*Part{
			{FunctionCall: functionCall},
		},
	}
	return NewEvent(author, content)
}

// NewToolResponseEvent creates a new event with a function response.
func NewToolResponseEvent(author string, functionResponse *FunctionResponse) *Event {
	content := &Content{
		Parts: []*Part{
			{FunctionResponse: functionResponse},
		},
	}
	return NewEvent(author, content)
}

// Content extraction and analysis

// GetContent returns the content of the event.
func (e *Event) GetContent() *Content {
	return e.Content
}

// HasFunctionCalls returns true if the event contains function calls.
func (e *Event) HasFunctionCalls() bool {
	if e.Content == nil {
		return false
	}
	return e.Content.HasFunctionCalls()
}

// HasText returns true if the event contains text.
func (e *Event) HasText() bool {
	if e.Content == nil {
		return false
	}
	return e.Content.HasText()
}

// GetText returns all text from the event content.
func (e *Event) GetText() string {
	if e.Content == nil {
		return ""
	}
	return e.Content.GetText()
}

// GetFunctionCalls returns all function calls from the event content.
func (e *Event) GetFunctionCalls() []*FunctionCall {
	if e.Content == nil {
		return nil
	}
	return e.Content.GetFunctionCalls()
}

// GetFunctionResponses returns all function responses from the event content.
func (e *Event) GetFunctionResponses() []*FunctionResponse {
	if e.Content == nil {
		return nil
	}
	return e.Content.GetFunctionResponses()
}

// Metadata management

// SetMetadata sets a metadata value.
func (e *Event) SetMetadata(key string, value interface{}) {
	if e.Metadata == nil {
		e.Metadata = make(map[string]interface{})
	}
	e.Metadata[key] = value
}

// GetMetadata gets a metadata value.
func (e *Event) GetMetadata(key string) (interface{}, bool) {
	if e.Metadata == nil {
		return nil, false
	}
	val, ok := e.Metadata[key]
	return val, ok
}

// GetMetadataString gets a metadata value as a string.
func (e *Event) GetMetadataString(key string) (string, bool) {
	val, ok := e.GetMetadata(key)
	if !ok {
		return "", false
	}
	str, ok := val.(string)
	return str, ok
}

// GetMetadataInt gets a metadata value as an int.
func (e *Event) GetMetadataInt(key string) (int, bool) {
	val, ok := e.GetMetadata(key)
	if !ok {
		return 0, false
	}

	switch v := val.(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}
