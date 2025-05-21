package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/memory"
)

// MemoryManager is an in-memory implementation of the Manager interface.
type MemoryManager struct {
	sessions   map[string]memory.Session
	options    Options
	expiration map[string]time.Time
	mutex      sync.RWMutex
}

// NewMemoryManager creates a new MemoryManager with the given options.
func NewMemoryManager(options ...Option) *MemoryManager {
	// Default options
	opts := Options{
		IDGenerator: generateID,
		Expiration:  24 * time.Hour, // Default 24-hour expiration
	}

	// Apply provided options
	for _, option := range options {
		option(&opts)
	}

	return &MemoryManager{
		sessions:   make(map[string]memory.Session),
		expiration: make(map[string]time.Time),
		options:    opts,
	}
}

// Get retrieves a session by ID, creating a new one if it doesn't exist.
func (m *MemoryManager) Get(ctx context.Context, id string) (memory.Session, error) {
	if id == "" {
		return m.Create(ctx)
	}

	m.mutex.RLock()
	session, exists := m.sessions[id]
	expiration, _ := m.expiration[id]
	m.mutex.RUnlock()

	// Check if session exists and is not expired
	if exists {
		if expiration.IsZero() || time.Now().Before(expiration) {
			// Update expiration on access if not expired
			if !expiration.IsZero() {
				m.mutex.Lock()
				m.expiration[id] = time.Now().Add(m.options.Expiration)
				m.mutex.Unlock()
			}
			return session, nil
		}
		// Session is expired, delete it
		m.Delete(ctx, id)
	}

	return m.Create(ctx, WithIDGenerator(func() string { return id }))
}

// Create creates a new session.
func (m *MemoryManager) Create(ctx context.Context, options ...Option) (memory.Session, error) {
	// Start with the manager's options
	opts := m.options

	// Apply any session-specific options
	for _, option := range options {
		option(&opts)
	}

	// Generate session ID
	id := opts.IDGenerator()
	if id == "" {
		return nil, ErrInvalidSessionID
	}

	// Create a new session
	session := memory.NewBaseSession(id, opts.Memory)

	// Store the session
	m.mutex.Lock()
	m.sessions[id] = session
	if opts.Expiration > 0 {
		m.expiration[id] = time.Now().Add(opts.Expiration)
	}
	m.mutex.Unlock()

	return session, nil
}

// Delete removes a session.
func (m *MemoryManager) Delete(ctx context.Context, id string) error {
	m.mutex.Lock()
	delete(m.sessions, id)
	delete(m.expiration, id)
	m.mutex.Unlock()
	return nil
}

// ListIDs returns a list of all active session IDs.
func (m *MemoryManager) ListIDs(ctx context.Context) ([]string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids, nil
}

// CleanExpired removes expired sessions.
func (m *MemoryManager) CleanExpired(ctx context.Context) (int, error) {
	now := time.Now()
	var expiredIDs []string

	// Find expired sessions
	m.mutex.RLock()
	for id, expiry := range m.expiration {
		if !expiry.IsZero() && now.After(expiry) {
			expiredIDs = append(expiredIDs, id)
		}
	}
	m.mutex.RUnlock()

	// Delete expired sessions
	if len(expiredIDs) > 0 {
		m.mutex.Lock()
		for _, id := range expiredIDs {
			delete(m.sessions, id)
			delete(m.expiration, id)
		}
		m.mutex.Unlock()
	}

	return len(expiredIDs), nil
}

// generateID creates a secure random session ID.
func generateID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// Fall back to timestamp-based ID in the unlikely case of failure
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}
