// Package memory provides interfaces and implementations for agent memory systems.
package memory

import (
	"context"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
)

// Memory is the interface that wraps the basic operations a memory system should support.
type Memory interface {
	// Store adds a message to the memory.
	Store(ctx context.Context, msg *message.Message) error

	// Retrieve retrieves all messages from the memory.
	Retrieve(ctx context.Context) ([]*message.Message, error)

	// Search searches for messages that match the given query.
	// How search is implemented depends on the memory implementation.
	Search(ctx context.Context, query string) ([]*message.Message, error)

	// FilterByMetadata searches for messages with metadata matching the given criteria.
	FilterByMetadata(ctx context.Context, metadata map[string]interface{}) ([]*message.Message, error)

	// Clear empties the memory.
	Clear(ctx context.Context) error

	// Size returns the number of messages in the memory.
	Size(ctx context.Context) (int, error)
}

// BaseMemory provides a basic in-memory implementation of the Memory interface.
// It can be embedded in other memory implementations.
type BaseMemory struct {
	messages []*message.Message
	mutex    sync.RWMutex
}

// NewBaseMemory creates a new BaseMemory.
func NewBaseMemory() *BaseMemory {
	return &BaseMemory{
		messages: make([]*message.Message, 0),
	}
}

// Store adds a message to the memory.
func (m *BaseMemory) Store(ctx context.Context, msg *message.Message) error {
	if msg == nil {
		return nil
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.messages = append(m.messages, msg)
	return nil
}

// Retrieve retrieves all messages from the memory.
func (m *BaseMemory) Retrieve(ctx context.Context) ([]*message.Message, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// Make a copy to avoid race conditions
	messages := make([]*message.Message, len(m.messages))
	copy(messages, m.messages)
	return messages, nil
}

// Search searches for messages that contain the given query in their content.
func (m *BaseMemory) Search(ctx context.Context, query string) ([]*message.Message, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var result []*message.Message
	for _, msg := range m.messages {
		if msg.Content != "" && contains(msg.Content, query) {
			result = append(result, msg)
		}
	}
	return result, nil
}

// contains is a simple string containment check.
// This could be replaced with more sophisticated search in subclasses.
func contains(content, query string) bool {
	return content != "" && query != "" && len(content) >= len(query) && strings.Contains(content, query)
}

// FilterByMetadata searches for messages with metadata matching the given criteria.
func (m *BaseMemory) FilterByMetadata(ctx context.Context, metadata map[string]interface{}) ([]*message.Message, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var result []*message.Message
	for _, msg := range m.messages {
		if matchesMetadata(msg, metadata) {
			result = append(result, msg)
		}
	}
	return result, nil
}

// matchesMetadata checks if a message's metadata matches the given criteria.
func matchesMetadata(msg *message.Message, criteria map[string]interface{}) bool {
	for key, value := range criteria {
		msgValue, exists := msg.Metadata[key]
		if !exists || msgValue != value {
			return false
		}
	}
	return true
}

// Clear empties the memory.
func (m *BaseMemory) Clear(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.messages = make([]*message.Message, 0)
	return nil
}

// Size returns the number of messages in the memory.
func (m *BaseMemory) Size(ctx context.Context) (int, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return len(m.messages), nil
}

// Session represents a conversation session with history tracking.
type Session interface {
	// ID returns the session identifier.
	ID() string

	// AddMessage adds a message to the session.
	AddMessage(ctx context.Context, msg *message.Message) error

	// GetMessages returns all messages in the session.
	GetMessages(ctx context.Context) ([]*message.Message, error)

	// GetRecentMessages returns the most recent n messages.
	GetRecentMessages(ctx context.Context, n int) ([]*message.Message, error)

	// GetMemory returns the memory system used by this session.
	GetMemory() Memory

	// SetMetadata sets session metadata.
	SetMetadata(key string, value interface{})

	// GetMetadata retrieves session metadata.
	GetMetadata(key string) (interface{}, bool)

	// Clear empties the session history.
	Clear(ctx context.Context) error

	// LastUpdated returns when the session was last updated.
	LastUpdated() time.Time
}

// BaseSession provides a basic implementation of the Session interface.
type BaseSession struct {
	id          string
	memory      Memory
	metadata    map[string]interface{}
	lastUpdated time.Time
	mutex       sync.RWMutex
}

// NewBaseSession creates a new BaseSession with the given ID and memory system.
func NewBaseSession(id string, memory Memory) *BaseSession {
	if memory == nil {
		memory = NewBaseMemory()
	}
	return &BaseSession{
		id:          id,
		memory:      memory,
		metadata:    make(map[string]interface{}),
		lastUpdated: time.Now(),
	}
}

// ID returns the session identifier.
func (s *BaseSession) ID() string {
	return s.id
}

// AddMessage adds a message to the session and updates the last updated time.
func (s *BaseSession) AddMessage(ctx context.Context, msg *message.Message) error {
	if msg == nil {
		return nil
	}

	err := s.memory.Store(ctx, msg)
	if err != nil {
		return err
	}

	s.mutex.Lock()
	s.lastUpdated = time.Now()
	s.mutex.Unlock()

	return nil
}

// GetMessages returns all messages in the session.
func (s *BaseSession) GetMessages(ctx context.Context) ([]*message.Message, error) {
	return s.memory.Retrieve(ctx)
}

// GetRecentMessages returns the most recent n messages.
func (s *BaseSession) GetRecentMessages(ctx context.Context, n int) ([]*message.Message, error) {
	messages, err := s.memory.Retrieve(ctx)
	if err != nil {
		return nil, err
	}

	if n <= 0 || n >= len(messages) {
		return messages, nil
	}

	// Return the last n messages
	return messages[len(messages)-n:], nil
}

// GetMemory returns the memory system used by this session.
func (s *BaseSession) GetMemory() Memory {
	return s.memory
}

// SetMetadata sets session metadata.
func (s *BaseSession) SetMetadata(key string, value interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.metadata[key] = value
	s.lastUpdated = time.Now()
}

// GetMetadata retrieves session metadata.
func (s *BaseSession) GetMetadata(key string) (interface{}, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	value, exists := s.metadata[key]
	return value, exists
}

// Clear empties the session history.
func (s *BaseSession) Clear(ctx context.Context) error {
	err := s.memory.Clear(ctx)
	if err != nil {
		return err
	}

	s.mutex.Lock()
	s.metadata = make(map[string]interface{})
	s.lastUpdated = time.Now()
	s.mutex.Unlock()

	return nil
}

// LastUpdated returns when the session was last updated.
func (s *BaseSession) LastUpdated() time.Time {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.lastUpdated
}
