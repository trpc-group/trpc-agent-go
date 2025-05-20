// Package context provides session context for agent applications.
package context

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/message"
)

// Context is a session-aware context that extends Go's context.Context.
// It encapsulates session data while following the context.Context pattern.
type Context interface {
	context.Context

	// SessionID returns the unique identifier for this session.
	SessionID() string

	// History returns the message history associated with this session.
	History() []*message.Message

	// State returns a map of session state variables.
	State() map[string]interface{}

	// WithState returns a new session context with the given key-value pair added to state.
	WithState(key string, value interface{}) Context

	// WithStateMap returns a new session context with the given state map merged with existing state.
	WithStateMap(state map[string]interface{}) Context
}

// sessionContext implements the Context interface.
type sessionContext struct {
	context.Context
	sessionID string
	history   []*message.Message
	state     map[string]interface{}
}

// NewContext creates a new session context with the specified session ID and history.
func NewContext(parent context.Context, sessionID string, history []*message.Message) Context {
	return &sessionContext{
		Context:   parent,
		sessionID: sessionID,
		history:   history,
		state:     make(map[string]interface{}),
	}
}

// NewContextWithState creates a new session context with the specified session ID, history, and state.
func NewContextWithState(parent context.Context, sessionID string, history []*message.Message, state map[string]interface{}) Context {
	return &sessionContext{
		Context:   parent,
		sessionID: sessionID,
		history:   history,
		state:     state,
	}
}

// SessionID returns the session ID.
func (c *sessionContext) SessionID() string {
	return c.sessionID
}

// History returns the message history.
func (c *sessionContext) History() []*message.Message {
	return c.history
}

// State returns the session state.
func (c *sessionContext) State() map[string]interface{} {
	return c.state
}

// WithState returns a new session context with the given key-value pair added to state.
func (c *sessionContext) WithState(key string, value interface{}) Context {
	newState := make(map[string]interface{}, len(c.state)+1)
	for k, v := range c.state {
		newState[k] = v
	}
	newState[key] = value

	return &sessionContext{
		Context:   c.Context,
		sessionID: c.sessionID,
		history:   c.history,
		state:     newState,
	}
}

// WithStateMap returns a new session context with the given state map merged with existing state.
func (c *sessionContext) WithStateMap(state map[string]interface{}) Context {
	newState := make(map[string]interface{}, len(c.state)+len(state))
	for k, v := range c.state {
		newState[k] = v
	}
	for k, v := range state {
		newState[k] = v
	}

	return &sessionContext{
		Context:   c.Context,
		sessionID: c.sessionID,
		history:   c.history,
		state:     newState,
	}
}

// FromContext retrieves a session context from a Go context.
// If the context is not a session context, it returns nil.
func FromContext(ctx context.Context) Context {
	if sc, ok := ctx.(Context); ok {
		return sc
	}
	return nil
}

// SessionIDFromContext retrieves the session ID from a context.
// If the context is not a session context, it returns an empty string.
func SessionIDFromContext(ctx context.Context) string {
	if sc := FromContext(ctx); sc != nil {
		return sc.SessionID()
	}
	return ""
}

// HistoryFromContext retrieves message history from a context.
// If the context is not a session context, it returns nil.
func HistoryFromContext(ctx context.Context) []*message.Message {
	if sc := FromContext(ctx); sc != nil {
		return sc.History()
	}
	return nil
}

// StateFromContext retrieves session state from a context.
// If the context is not a session context, it returns nil.
func StateFromContext(ctx context.Context) map[string]interface{} {
	if sc := FromContext(ctx); sc != nil {
		return sc.State()
	}
	return nil
}

// ValueFromState retrieves a specific value from session state.
// If the context is not a session context or the key doesn't exist, it returns nil.
func ValueFromState(ctx context.Context, key string) interface{} {
	if state := StateFromContext(ctx); state != nil {
		return state[key]
	}
	return nil
} 