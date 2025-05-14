package session

import (
	"context"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// PersistentManager is a session manager that persists sessions using a store provider.
type PersistentManager struct {
	cache       map[string]memory.Session // In-memory cache of sessions
	store       StoreProvider             // Persistent storage provider
	options     Options                   // Manager options
	mutex       sync.RWMutex              // Mutex for thread safety
	lastCleanup time.Time                 // Last time expired sessions were cleaned up
}

// NewPersistentManager creates a new persistent session manager.
func NewPersistentManager(store StoreProvider, options ...Option) (*PersistentManager, error) {
	if store == nil {
		return nil, ErrInvalidOptions
	}

	// Default options
	opts := Options{
		IDGenerator: generateID,
		Expiration:  24 * time.Hour, // Default 24-hour expiration
	}

	// Apply provided options
	for _, option := range options {
		option(&opts)
	}

	return &PersistentManager{
		cache:       make(map[string]memory.Session),
		store:       store,
		options:     opts,
		lastCleanup: time.Now(),
	}, nil
}

// Get retrieves a session by ID, creating a new one if it doesn't exist.
func (m *PersistentManager) Get(ctx context.Context, id string) (memory.Session, error) {
	// Check if we should clean expired sessions (once per hour)
	if time.Since(m.lastCleanup) > time.Hour {
		go m.CleanExpired(ctx) // Run cleanup in background
	}

	// For empty ID, always create a new session
	if id == "" {
		return m.Create(ctx)
	}

	// First check the cache
	m.mutex.RLock()
	session, exists := m.cache[id]
	m.mutex.RUnlock()

	if exists {
		// Check if the session is expired
		expiry, hasExpiry := session.GetMetadata("expiration")
		if hasExpiry {
			if expTime, ok := expiry.(time.Time); ok && time.Now().After(expTime) {
				// Session is expired, delete it
				m.Delete(ctx, id)
				return m.Create(ctx, WithIDGenerator(func() string { return id }))
			}
		}
		return session, nil
	}

	// Not in cache, try to load from store
	session, err := m.store.Load(ctx, id)
	if err != nil {
		if err == ErrSessionNotFound {
			// Session doesn't exist, create a new one with the requested ID
			return m.Create(ctx, WithIDGenerator(func() string { return id }))
		}
		return nil, err
	}

	// Check if the loaded session is expired
	expiry, hasExpiry := session.GetMetadata("expiration")
	if hasExpiry {
		if expTime, ok := expiry.(time.Time); ok && time.Now().After(expTime) {
			// Session is expired, delete it
			m.Delete(ctx, id)
			return m.Create(ctx, WithIDGenerator(func() string { return id }))
		}
	}

	// Add to cache
	m.mutex.Lock()
	m.cache[id] = session
	m.mutex.Unlock()

	return session, nil
}

// Create creates a new session.
func (m *PersistentManager) Create(ctx context.Context, options ...Option) (memory.Session, error) {
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

	// Set expiration if specified
	if opts.Expiration > 0 {
		expiry := time.Now().Add(opts.Expiration)
		session.SetMetadata("expiration", expiry)
	}

	// Store in both cache and persistent store
	m.mutex.Lock()
	m.cache[id] = session
	m.mutex.Unlock()

	// Persist the session
	if err := m.store.Save(ctx, session); err != nil {
		// Remove from cache if persistence fails
		m.mutex.Lock()
		delete(m.cache, id)
		m.mutex.Unlock()
		return nil, err
	}

	return session, nil
}

// Delete removes a session.
func (m *PersistentManager) Delete(ctx context.Context, id string) error {
	// Remove from cache
	m.mutex.Lock()
	delete(m.cache, id)
	m.mutex.Unlock()

	// Remove from persistent store
	return m.store.Delete(ctx, id)
}

// ListIDs returns a list of all active session IDs.
func (m *PersistentManager) ListIDs(ctx context.Context) ([]string, error) {
	// Get IDs from persistent store
	return m.store.ListIDs(ctx)
}

// CleanExpired removes expired sessions.
func (m *PersistentManager) CleanExpired(ctx context.Context) (int, error) {
	// Update last cleanup time
	m.mutex.Lock()
	m.lastCleanup = time.Now()
	m.mutex.Unlock()

	// Get all session IDs
	ids, err := m.ListIDs(ctx)
	if err != nil {
		return 0, err
	}

	var expiredCount int
	now := time.Now()

	// Check each session for expiration
	for _, id := range ids {
		// Load the session
		session, err := m.store.Load(ctx, id)
		if err != nil {
			continue // Skip if can't load
		}

		// Check expiration
		expiry, hasExpiry := session.GetMetadata("expiration")
		if hasExpiry {
			if expTime, ok := expiry.(time.Time); ok && now.After(expTime) {
				// Delete expired session
				if err := m.Delete(ctx, id); err == nil {
					expiredCount++
				}
			}
		}
	}

	return expiredCount, nil
}
