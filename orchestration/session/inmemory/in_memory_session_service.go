// Package inmemory provides in-memory session service implementation.
package inmemory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

var _ session.Service = (*SessionService)(nil)
var (
	errAppNameRequired   = errors.New("appName is required")
	errUserIDRequired    = errors.New("userID is required")
	errSessionIDRequired = errors.New("sessionID is required")
)

// appSessions is a map of userID to sessions, it store sessions of one app.
type appSessions struct {
	mu        sync.RWMutex
	sessions  map[string]map[string]*session.Session
	userState map[string]session.StateMap
	appState  session.StateMap
}

// newAppSessions creates a new memory sessions map of one app.
func newAppSessions() *appSessions {
	return &appSessions{
		sessions:  make(map[string]map[string]*session.Session),
		userState: make(map[string]session.StateMap),
		appState:  make(session.StateMap),
	}
}

// SessionService provides an in-memory implementation of SessionService.
type SessionService struct {
	mu   sync.RWMutex
	apps map[string]*appSessions
}

// NewSessionService creates a new in-memory session service.
func NewSessionService() *SessionService {
	return &SessionService{
		apps: make(map[string]*appSessions),
	}
}

func (s *SessionService) getApp(appName string) (*appSessions, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, ok := s.apps[appName]
	return app, ok
}

func (s *SessionService) getOrCreateApp(appName string) *appSessions {
	s.mu.RLock()
	app, ok := s.apps[appName]
	if ok {
		s.mu.RUnlock()
		return app
	}
	s.mu.RUnlock()

	s.mu.Lock()
	app, ok = s.apps[appName]
	if ok {
		s.mu.Unlock()
		return app
	}
	app = newAppSessions()
	s.apps[appName] = app
	s.mu.Unlock()
	return app
}

// CreateSession creates a new session with the given parameters.
func (s *SessionService) CreateSession(
	ctx context.Context,
	appName, userID string,
	state session.StateMap,
	sessionID string,
) (*session.Session, error) {
	if appName == "" {
		return nil, errAppNameRequired
	}
	if userID == "" {
		return nil, errUserIDRequired
	}

	app := s.getOrCreateApp(appName)

	// Generate session ID if not provided
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Create the sess with new State
	sess := &session.Session{
		ID:        sessionID,
		AppName:   appName,
		UserID:    userID,
		State:     session.NewState(), // Initialize with provided state
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	for key, value := range state {
		sess.State.Set(key, value)
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.sessions[userID] == nil {
		app.sessions[userID] = make(map[string]*session.Session)
	}

	// Store the session
	app.sessions[userID][sessionID] = sess

	// Create a copy and merge state for return
	copySess := copySession(sess)
	return mergeState(app.appState, app.userState[userID], copySess), nil
}

// GetSession retrieves a session by app name, user ID, and session ID.
func (s *SessionService) GetSession(
	ctx context.Context,
	appName string,
	userID, sessionID string,
	opts *session.GetSessionOpts,
) (*session.Session, error) {
	if appName == "" {
		return nil, errAppNameRequired
	}
	if userID == "" {
		return nil, errUserIDRequired
	}
	if sessionID == "" {
		return nil, errSessionIDRequired
	}

	app, ok := s.getApp(appName)
	if !ok {
		return nil, nil
	}

	app.mu.RLock()
	defer app.mu.RUnlock()

	if _, ok := app.sessions[userID]; !ok {
		return nil, nil
	}

	session, ok := app.sessions[userID][sessionID]
	if !ok {
		return nil, nil
	}

	copySess := copySession(session)

	// Apply filtering options if provided
	if opts != nil {
		applyGetSessionOptions(copySess, opts)
	}
	return mergeState(app.appState, app.userState[userID], copySess), nil
}

// ListSessions returns all sessions for a given app and user.
func (s *SessionService) ListSessions(
	ctx context.Context,
	appName, userID string,
) ([]*session.Session, error) {
	if appName == "" {
		return nil, errAppNameRequired
	}
	if userID == "" {
		return nil, errUserIDRequired
	}

	app, ok := s.getApp(appName)
	if !ok {
		return []*session.Session{}, nil
	}

	app.mu.RLock()
	defer app.mu.RUnlock()

	if _, ok := app.sessions[userID]; !ok {
		return []*session.Session{}, nil
	}

	sessions := make([]*session.Session, 0, len(app.sessions[userID]))
	for _, sess := range app.sessions[userID] {
		copySess := copySession(sess)
		sessions = append(sessions, copySess)
	}
	return sessions, nil
}

// DeleteSession removes a session from storage.
func (s *SessionService) DeleteSession(
	ctx context.Context,
	appName, userID, sessionID string,
) error {
	if appName == "" {
		return errAppNameRequired
	}
	if userID == "" {
		return errUserIDRequired
	}
	if sessionID == "" {
		return errSessionIDRequired
	}

	app, ok := s.getApp(appName)
	if !ok {
		return nil
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if _, ok := app.sessions[userID][sessionID]; !ok {
		return nil // Session doesn't exist, consider it already deleted
	}

	delete(app.sessions[userID], sessionID)
	return nil
}

// copySession creates a deep copy of a session.
func copySession(sess *session.Session) *session.Session {
	copySess := &session.Session{
		ID:        sess.ID,
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		State:     sess.State,
		Events:    make([]event.Event, len(sess.Events)),
		UpdatedAt: sess.UpdatedAt,
	}

	if sess.State != nil {
		for key, value := range sess.State.Value {
			copySess.State.Set(key, value)
		}
	}

	copy(copySess.Events, sess.Events)

	return copySess
}

// applyGetSessionOptions applies filtering options to the session.
func applyGetSessionOptions(sess *session.Session, opts *session.GetSessionOpts) {
	if opts.NumRecentEvents > 0 && len(sess.Events) > opts.NumRecentEvents {
		sess.Events = sess.Events[len(sess.Events)-opts.NumRecentEvents:]
	}

	if !opts.AfterTime.IsZero() {
		var filteredEvents []event.Event
		for _, ev := range sess.Events {
			// Include events that are after or equal to the specified time
			// This matches the Python implementation: timestamp >= after_timestamp
			if ev.Timestamp.After(opts.AfterTime) || ev.Timestamp.Equal(opts.AfterTime) {
				filteredEvents = append(filteredEvents, ev)
			}
		}
		sess.Events = filteredEvents
	}
}

// mergeState merges app-level and user-level state into the session state.
func mergeState(appState, userState session.StateMap, sess *session.Session) *session.Session {
	for key, value := range appState {
		sess.State.Set(session.StateAppPrefix+key, value)
	}
	for key, value := range userState {
		sess.State.Set(session.StateUserPrefix+key, value)
	}
	return sess
}
