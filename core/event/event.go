// Package event provides the event system for agent communication.
package event

import (
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// Type represents the type of an event.
type Type string

// Name represents the name of an event, used for custom event types.
type Name string

// Predefined event types.
const (
	TypeMessage    Type = "message"
	TypeTool       Type = "tool"
	TypeError      Type = "error"
	TypeAgent      Type = "agent"
	TypeSystem     Type = "system"
	TypeEvaluation Type = "evaluation"
	TypeCustom     Type = "custom"
)

// Additional event types for specialized agents
const (
	TypeStream           = "stream"
	TypeLoopIteration    = "loop_iteration"
	TypeAgentStart       = "agent_start"
	TypeAgentEnd         = "agent_end"
	TypeStreamStart      = "stream_start"
	TypeStreamChunk      = "stream_chunk"
	TypeStreamToolCall   = "stream_tool_call"
	TypeStreamToolResult = "stream_tool_result"
	TypeStreamEnd        = "stream_end"
)

// Event represents an event in the ADK system.
type Event struct {
	ID        string                 `json:"id"`
	Type      Type                   `json:"type"`
	Name      Name                   `json:"name,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	Data      interface{}            `json:"data,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// MessageEvent represents a message event.
type MessageEvent struct {
	Event
	Message *message.Message `json:"message"`
}

// ErrorEvent represents an error event.
type ErrorEvent struct {
	Event
	Error     string `json:"error"`
	ErrorCode int    `json:"error_code,omitempty"`
}

// StreamEvent is an event that contains streaming content.
type StreamEvent struct {
	*Event
	Content string
}

// LoopIterationEvent is an event that signals a loop iteration.
type LoopIterationEvent struct {
	*Event
	Iteration int
}

// AgentStartEvent is an event that signals an agent has started.
type AgentStartEvent struct {
	*Event
	AgentName string
	Index     int
}

// AgentEndEvent is an event that signals an agent has ended.
type AgentEndEvent struct {
	*Event
	AgentName string
	Index     int
}

// NewEvent creates a new event.
func NewEvent(eventType Type, data interface{}) *Event {
	return &Event{
		ID:        uuid.New().String(),
		Type:      eventType,
		CreatedAt: time.Now(),
		Data:      data,
		Metadata:  make(map[string]interface{}),
	}
}

// NewCustomEvent creates a new custom event with a specific name and data.
func NewCustomEvent(name string, data interface{}) *Event {
	evt := NewEvent(TypeCustom, data)
	evt.Name = Name(name)
	return evt
}

// NewMessageEvent creates a new message event.
func NewMessageEvent(msg *message.Message) *Event {
	evt := NewEvent(TypeMessage, msg)
	evt.SetMetadata("message_role", msg.Role)
	return evt
}

// NewErrorEvent creates a new error event.
func NewErrorEvent(err error, errorCode int) *Event {
	var errStr string
	if err != nil {
		errStr = err.Error()
	}

	evt := NewEvent(TypeError, nil)
	evt.SetMetadata("error", errStr)
	evt.SetMetadata("error_code", errorCode)
	return evt
}

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

// NewStreamEvent creates a new stream event.
func NewStreamEvent(content string) *Event {
	event := NewEvent(TypeStream, nil)
	event.SetMetadata("content", content)
	return event
}

// NewLoopIterationEvent creates a new loop iteration event.
func NewLoopIterationEvent(iteration int) *Event {
	event := NewEvent(TypeLoopIteration, nil)
	event.SetMetadata("iteration", iteration)
	return event
}

// NewAgentStartEvent creates a new agent start event.
func NewAgentStartEvent(agentName string, index int) *Event {
	event := NewEvent(TypeAgentStart, nil)
	event.SetMetadata("agent_name", agentName)
	event.SetMetadata("index", index)
	return event
}

// NewAgentEndEvent creates a new agent end event.
func NewAgentEndEvent(agentName string, index int) *Event {
	event := NewEvent(TypeAgentEnd, nil)
	event.SetMetadata("agent_name", agentName)
	event.SetMetadata("index", index)
	return event
}

// NewStreamStartEvent creates a new stream start event.
func NewStreamStartEvent(sessionID string) *Event {
	event := NewEvent(TypeStreamStart, nil)
	event.SetMetadata("session_id", sessionID)
	return event
}

// NewStreamChunkEvent creates a new stream chunk event.
func NewStreamChunkEvent(content string, sequence int) *Event {
	event := NewEvent(TypeStreamChunk, nil)
	event.SetMetadata("content", content)
	event.SetMetadata("sequence", sequence)
	return event
}

// NewStreamToolCallEvent creates a new stream tool call event.
func NewStreamToolCallEvent(name string, arguments string, id string) *Event {
	event := NewEvent(TypeStreamToolCall, map[string]interface{}{
		"name":      name,
		"arguments": arguments,
		"id":        id,
	})
	return event
}

// NewStreamToolResultEvent creates a new stream tool result event.
func NewStreamToolResultEvent(name string, result interface{}, err error) *Event {
	var errStr string
	if err != nil {
		errStr = err.Error()
	}

	event := NewEvent(TypeStreamToolResult, map[string]interface{}{
		"name":   name,
		"result": result,
		"error":  errStr,
	})
	return event
}

// NewStreamEndEvent creates a new stream end event.
func NewStreamEndEvent(completeText string) *Event {
	event := NewEvent(TypeStreamEnd, map[string]interface{}{
		"complete_text": completeText,
	})
	return event
}
