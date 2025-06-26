// Package inmemory provides in-memory session service implementation.
package inmemory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

var _ session.Service = (*SessionService)(nil)

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

// ServiceOpts is the options for session service.
type ServiceOpts struct {
	// SessionEventLimit is the limit of events in a session.
	SessionEventLimit int
}

// SessionService provides an in-memory implementation of SessionService.
type SessionService struct {
	mu   sync.RWMutex
	apps map[string]*appSessions
	opts ServiceOpts
}

// NewSessionService creates a new in-memory session service.
func NewSessionService(opts ServiceOpts) *SessionService {
	return &SessionService{
		apps: make(map[string]*appSessions),
		opts: opts,
	}
}

func (s *SessionService) getAppSessions(appName string) (*appSessions, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, ok := s.apps[appName]
	return app, ok
}

func (s *SessionService) getOrCreateAppSessions(appName string) *appSessions {
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
	key session.Key,
	state session.StateMap,
	opts *session.Options,
) (*session.Session, error) {
	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}

	app := s.getOrCreateAppSessions(key.AppName)

	// Generate session ID if not provided
	if key.SessionID == "" {
		key.SessionID = uuid.New().String()
	}

	// Create the session with new State
	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     session.NewState(), // Initialize with provided state
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Set initial state if provided
	for k, v := range state {
		sess.State.Set(k, v)
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.sessions[key.UserID] == nil {
		app.sessions[key.UserID] = make(map[string]*session.Session)
	}

	// Store the session
	app.sessions[key.UserID][key.SessionID] = sess

	// Create a copy and merge state for return
	copiedSess := copySession(sess)
	return mergeState(app.appState, app.userState[key.UserID], copiedSess), nil
}

// GetSession retrieves a session by app name, user ID, and session ID.
func (s *SessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts *session.Options,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	app, ok := s.getAppSessions(key.AppName)
	if !ok {
		return nil, nil
	}

	app.mu.RLock()
	defer app.mu.RUnlock()
	if _, ok := app.sessions[key.UserID]; !ok {
		return nil, nil
	}
	sess, ok := app.sessions[key.UserID][key.SessionID]
	if !ok {
		return nil, nil
	}

	copiedSess := copySession(sess)

	// apply filtering options if provided
	if opts != nil {
		applyGetSessionOptions(copiedSess, opts)
	}
	return mergeState(app.appState, app.userState[key.UserID], copiedSess), nil
}

// ListSessions returns all sessions for a given app and user.
func (s *SessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts *session.Options,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	app, ok := s.getAppSessions(userKey.AppName)
	if !ok {
		return []*session.Session{}, nil
	}

	app.mu.RLock()
	defer app.mu.RUnlock()

	if _, ok := app.sessions[userKey.UserID]; !ok {
		return []*session.Session{}, nil
	}

	sessList := make([]*session.Session, 0, len(app.sessions[userKey.UserID]))
	for _, s := range app.sessions[userKey.UserID] {
		copiedSess := copySession(s)
		if opts != nil {
			applyGetSessionOptions(copiedSess, opts)
		}
		sessList = append(sessList, mergeState(app.appState, app.userState[userKey.UserID], copiedSess))
	}
	return sessList, nil
}

// DeleteSession removes a session from storage.
func (s *SessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	opts *session.Options,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	app, ok := s.getAppSessions(key.AppName)
	if !ok {
		return nil
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if _, ok := app.sessions[key.UserID][key.SessionID]; !ok {
		return nil
	}

	// Delete the session
	delete(app.sessions[key.UserID], key.SessionID)

	// Clean up empty user sessions map
	if len(app.sessions[key.UserID]) == 0 {
		delete(app.sessions, key.UserID)
	}

	if len(app.sessions[key.UserID]) == 0 {
		delete(app.userState, key.UserID)
	}

	return nil
}

// AppendEvent appends an event to a session.
func (s *SessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	event *event.Event,
	opts *session.Options,
) error {
	s.updateSessionState(sess, event)
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	app, ok := s.getAppSessions(key.AppName)
	if !ok {
		return fmt.Errorf("app not found: %s", key.AppName)
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	// Check if user exists first to prevent panic
	userSessions, ok := app.sessions[key.UserID]
	if !ok {
		return fmt.Errorf("user not found: %s", key.UserID)
	}

	storedSession, ok := userSessions[key.SessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", key.SessionID)
	}
	s.updateSessionState(storedSession, event)
	return nil
}

func (s *SessionService) updateSessionState(sess *session.Session, event *event.Event) {
	sess.Events = append(sess.Events, *event)
	if s.opts.SessionEventLimit > 0 && len(sess.Events) > s.opts.SessionEventLimit {
		sess.Events = sess.Events[len(sess.Events)-s.opts.SessionEventLimit:]
	}
	sess.UpdatedAt = time.Now()
}

// copySession creates a deep copy of a session.
// shallow copy
func copySession(sess *session.Session) *session.Session {
	copiedSess := &session.Session{
		ID:        sess.ID,
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		State:     session.NewState(), // Create new state to avoid reference sharing
		Events:    make([]event.Event, len(sess.Events)),
		UpdatedAt: sess.UpdatedAt,
		CreatedAt: sess.CreatedAt, // Add missing CreatedAt field
	}

	// copy state
	if sess.State != nil {
		for k, v := range sess.State.Value {
			copiedSess.State.Set(k, v)
		}
	}
	copy(copiedSess.Events, sess.Events)
	return copiedSess
}

// applyGetSessionOptions applies filtering options to the session.
func applyGetSessionOptions(sess *session.Session, opts *session.Options) {
	if opts.EventNum > 0 && len(sess.Events) > opts.EventNum {
		sess.Events = sess.Events[len(sess.Events)-opts.EventNum:]
	}

	if !opts.EventTime.IsZero() {
		var filteredEvents []event.Event
		for _, e := range sess.Events {
			// Include events that are after or equal to the specified time
			// This matches the Python implementation: timestamp >= after_timestamp
			if e.Timestamp.After(opts.EventTime) || e.Timestamp.Equal(opts.EventTime) {
				filteredEvents = append(filteredEvents, e)
			}
		}
		sess.Events = filteredEvents
	}
}

// mergeState merges app-level and user-level state into the session state.
func mergeState(appState, userState session.StateMap, sess *session.Session) *session.Session {
	for k, v := range appState {
		sess.State.Set(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.State.Set(session.StateUserPrefix+k, v)
	}
	return sess
}
