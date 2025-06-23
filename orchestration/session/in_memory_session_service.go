// Package session provides in-memory session service implementation.
package session

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
)

// State prefix constants for different scope levels
const (
	AppPrefix  = "app."
	UserPrefix = "user."
)

var _ Service = (*InMemorySessionService)(nil)

// InMemorySessionBucket is a bucket for storing sessions, it store sessions of one agent/app.
type InMemorySessionBucket struct {
	mu        sync.RWMutex
	sessions  map[string]map[string]*Session
	userState map[string]StateMap
	appState  StateMap
}

// NewInMemorySessionBucket creates a new memory session bucket.
func NewInMemorySessionBucket() *InMemorySessionBucket {
	return &InMemorySessionBucket{
		sessions:  make(map[string]map[string]*Session),
		userState: make(map[string]StateMap),
		appState:  make(StateMap),
	}
}

// InMemorySessionService provides an in-memory implementation of SessionService.
type InMemorySessionService struct {
	mu      sync.RWMutex
	buckets map[string]*InMemorySessionBucket
}

// NewInMemorySessionService creates a new in-memory session service.
func NewInMemorySessionService() *InMemorySessionService {
	service := &InMemorySessionService{
		buckets: make(map[string]*InMemorySessionBucket),
	}

	defaultBucket := &InMemorySessionBucket{
		sessions:  make(map[string]map[string]*Session),
		userState: make(map[string]StateMap),
		appState:  make(StateMap),
	}
	service.buckets[""] = defaultBucket
	return service
}

func (s *InMemorySessionService) checkBucketExists(appName string) (*InMemorySessionBucket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket, ok := s.buckets[appName]
	return bucket, ok
}

func (s *InMemorySessionService) getOrCreateBucket(appName string) *InMemorySessionBucket {
	s.mu.RLock()
	bucket, ok := s.buckets[appName]
	if ok {
		s.mu.RUnlock()
		return bucket
	}
	s.mu.RUnlock()

	s.mu.Lock()
	bucket, ok = s.buckets[appName]
	if ok {
		s.mu.Unlock()
		return bucket
	}
	bucket = NewInMemorySessionBucket()
	s.buckets[appName] = bucket
	s.mu.Unlock()
	return bucket
}

// CreateSession creates a new session with the given parameters.
func (s *InMemorySessionService) CreateSession(
	ctx context.Context,
	appName, userID string,
	state StateMap,
	sessionID string,
) (*Session, error) {
	bucket := s.getOrCreateBucket(appName)

	// Generate session ID if not provided
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Create the session with new State
	session := &Session{
		ID:        sessionID,
		AppName:   appName,
		UserID:    userID,
		State:     NewState(), // Initialize with provided state
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	for key, value := range state {
		session.State.Set(key, value)
	}

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	if bucket.sessions[userID] == nil {
		bucket.sessions[userID] = make(map[string]*Session)
	}

	// Store the session
	bucket.sessions[userID][sessionID] = session

	// Create a copy and merge state for return
	copiedSession := copySession(session)
	return mergeState(bucket.appState, bucket.userState[userID], copiedSession), nil
}

// GetSession retrieves a session by app name, user ID, and session ID.
func (s *InMemorySessionService) GetSession(
	ctx context.Context,
	appName string,
	userID, sessionID string,
	opts *GetSessionOpts,
) (*Session, error) {
	bucket, ok := s.checkBucketExists(appName)
	if !ok {
		return nil, nil // Bucket not found
	}

	bucket.mu.RLock()
	defer bucket.mu.RUnlock()

	if _, ok := bucket.sessions[userID]; !ok {
		return nil, nil // User not found
	}

	session, ok := bucket.sessions[userID][sessionID]
	if !ok {
		return nil, nil // Session not found
	}

	copiedSession := copySession(session)

	// Apply filtering options if provided
	if opts != nil {
		applyGetSessionOptions(copiedSession, opts)
	}
	return mergeState(bucket.appState, bucket.userState[userID], copiedSession), nil
}

// ListSessions returns all sessions for a given app and user.
func (s *InMemorySessionService) ListSessions(
	ctx context.Context,
	appName, userID string,
) ([]*Session, error) {
	bucket, ok := s.checkBucketExists(appName)
	if !ok {
		return []*Session{}, nil // Bucket not found
	}

	bucket.mu.RLock()
	defer bucket.mu.RUnlock()

	if _, ok := bucket.sessions[userID]; !ok {
		return []*Session{}, nil
	}

	var sessions []*Session
	for _, session := range bucket.sessions[userID] {
		copiedSession := copySession(session)
		sessions = append(sessions, copiedSession)
	}

	return sessions, nil
}

// DeleteSession removes a session from storage.
func (s *InMemorySessionService) DeleteSession(
	ctx context.Context,
	appName, userID, sessionID string,
) error {
	bucket, ok := s.checkBucketExists(appName)
	if !ok {
		return nil // Bucket not found
	}

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	if _, ok := bucket.sessions[userID][sessionID]; !ok {
		return nil // Session doesn't exist, consider it already deleted
	}

	delete(bucket.sessions[userID], sessionID)
	return nil
}

// copySession creates a deep copy of a session.
func copySession(session *Session) *Session {
	copied := &Session{
		ID:        session.ID,
		AppName:   session.AppName,
		UserID:    session.UserID,
		State:     NewState(),
		Events:    make([]event.Event, len(session.Events)),
		UpdatedAt: session.UpdatedAt,
	}

	if session.State != nil {
		for key, value := range session.State.value {
			copied.State.Set(key, value)
		}
	}

	copy(copied.Events, session.Events)

	return copied
}

// applyGetSessionOptions applies filtering options to the session.
func applyGetSessionOptions(session *Session, opts *GetSessionOpts) {
	if opts.NumRecentEvents > 0 && len(session.Events) > opts.NumRecentEvents {
		session.Events = session.Events[len(session.Events)-opts.NumRecentEvents:]
	}

	if !opts.AfterTime.IsZero() {
		var filteredEvents []event.Event
		for _, ev := range session.Events {
			// Include events that are after or equal to the specified time
			// This matches the Python implementation: timestamp >= after_timestamp
			if ev.Timestamp.After(opts.AfterTime) || ev.Timestamp.Equal(opts.AfterTime) {
				filteredEvents = append(filteredEvents, ev)
			}
		}
		session.Events = filteredEvents
	}
}

// mergeState merges app-level and user-level state into the session state.
func mergeState(appState, userState StateMap, session *Session) *Session {
	for key, value := range appState {
		session.State.Set(AppPrefix+key, value)
	}

	for key, value := range userState {
		session.State.Set(UserPrefix+key, value)
	}

	return session
}
