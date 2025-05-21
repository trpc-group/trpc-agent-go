// Package session provides session management capabilities for agent applications.
package session

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/memory"
)

// Common errors.
var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrSessionExpired   = errors.New("session expired")
	ErrInvalidSessionID = errors.New("invalid session ID")
	ErrStorageFailed    = errors.New("session storage failed")
	ErrRetrievalFailed  = errors.New("session retrieval failed")
	ErrInvalidOptions   = errors.New("invalid session options")
)

// Manager provides an interface for managing sessions.
// It allows for creating, retrieving, updating, and deleting sessions,
// as well as handling session persistence and expiration.
type Manager interface {
	// Get retrieves a session by ID, creating a new one if it doesn't exist.
	Get(ctx context.Context, id string) (memory.Session, error)

	// Create creates a new session.
	Create(ctx context.Context, options ...Option) (memory.Session, error)

	// Delete removes a session.
	Delete(ctx context.Context, id string) error

	// ListIDs returns a list of all active session IDs.
	ListIDs(ctx context.Context) ([]string, error)

	// CleanExpired removes expired sessions.
	CleanExpired(ctx context.Context) (int, error)
}

// StoreProvider is an interface for session storage backends.
type StoreProvider interface {
	// Save persists a session.
	Save(ctx context.Context, session memory.Session) error

	// Load retrieves a session by ID.
	Load(ctx context.Context, id string) (memory.Session, error)

	// Delete removes a session by ID.
	Delete(ctx context.Context, id string) error

	// ListIDs returns all active session IDs.
	ListIDs(ctx context.Context) ([]string, error)
}

// Options holds configuration options for sessions.
type Options struct {
	// IDGenerator is a function that generates unique session IDs.
	IDGenerator func() string

	// Expiration is the time after which a session expires.
	Expiration time.Duration

	// Memory is the memory system to use for the session.
	Memory memory.Memory
}

// Option is a function that configures a session.
type Option func(*Options)

// WithIDGenerator sets a custom ID generator function.
func WithIDGenerator(generator func() string) Option {
	return func(o *Options) {
		o.IDGenerator = generator
	}
}

// WithExpiration sets the session expiration duration.
func WithExpiration(duration time.Duration) Option {
	return func(o *Options) {
		o.Expiration = duration
	}
}

// WithMemory sets the memory system for the session.
func WithMemory(mem memory.Memory) Option {
	return func(o *Options) {
		o.Memory = mem
	}
}
