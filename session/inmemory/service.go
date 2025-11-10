//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides in-memory session service implementation.
package inmemory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	isession "trpc.group/trpc-go/trpc-agent-go/internal/session"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	defaultSessionEventLimit     = 1000
	defaultCleanupIntervalSecond = 300 // 5 min

	defaultAsyncSummaryNum  = 3
	defaultSummaryQueueSize = 100
)

// stateWithTTL wraps state data with expiration time.
type stateWithTTL struct {
	data      session.StateMap
	expiredAt time.Time
}

// sessionWithTTL wraps session with expiration time.
type sessionWithTTL struct {
	session   *session.Session
	expiredAt time.Time
}

var _ session.Service = (*SessionService)(nil)

// isExpired checks if the given time has passed.
func isExpired(expiredAt time.Time) bool {
	return !expiredAt.IsZero() && time.Now().After(expiredAt)
}

// calculateExpiredAt calculates expiration time based on TTL.
func calculateExpiredAt(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{} // Zero time means no expiration
	}
	return time.Now().Add(ttl)
}

// getValidState returns state data if not expired, nil otherwise.
func getValidState(stateWithTTL *stateWithTTL) session.StateMap {
	if stateWithTTL == nil || isExpired(stateWithTTL.expiredAt) {
		return nil
	}
	return stateWithTTL.data
}

// getValidSession returns session if not expired, nil otherwise.
func getValidSession(sessionWithTTL *sessionWithTTL) *session.Session {
	if sessionWithTTL == nil || isExpired(sessionWithTTL.expiredAt) {
		return nil
	}
	return sessionWithTTL.session
}

// appSessions is a map of userID to sessions, it store sessions of one app.
type appSessions struct {
	mu        sync.RWMutex
	sessions  map[string]map[string]*sessionWithTTL
	userState map[string]*stateWithTTL
	appState  *stateWithTTL
}

// newAppSessions creates a new memory sessions map of one app.
func newAppSessions() *appSessions {
	return &appSessions{
		sessions:  make(map[string]map[string]*sessionWithTTL),
		userState: make(map[string]*stateWithTTL),
		appState:  &stateWithTTL{data: make(session.StateMap)},
	}
}

// SessionService provides an in-memory implementation of SessionService.
type SessionService struct {
	mu              sync.RWMutex
	apps            map[string]*appSessions
	opts            serviceOpts
	cleanupTicker   *time.Ticker
	cleanupDone     chan struct{}
	cleanupOnce     sync.Once
	summaryJobChans []chan *summaryJob // channel for summary jobs to processing
}

// summaryJob represents a summary job to be processed asynchronously
type summaryJob struct {
	sessionKey session.Key
	filterKey  string
	force      bool
	session    *session.Session
}

// NewSessionService creates a new in-memory session service.
func NewSessionService(options ...ServiceOpt) *SessionService {
	opts := serviceOpts{
		sessionEventLimit: defaultSessionEventLimit,
		cleanupInterval:   0,
		asyncSummaryNum:   defaultAsyncSummaryNum,
		summaryQueueSize:  defaultSummaryQueueSize,
		summaryJobTimeout: 30 * time.Second,
	}
	for _, option := range options {
		option(&opts)
	}

	// Set default cleanup interval if any TTL is configured and auto cleanup is not disabled
	if opts.cleanupInterval <= 0 {
		if opts.sessionTTL > 0 || opts.appStateTTL > 0 || opts.userStateTTL > 0 {
			opts.cleanupInterval = defaultCleanupIntervalSecond * time.Second
		}
	}

	s := &SessionService{
		apps:        make(map[string]*appSessions),
		opts:        opts,
		cleanupDone: make(chan struct{}),
	}

	// Start automatic cleanup if cleanup interval is configured and auto cleanup is not disabled
	if opts.cleanupInterval > 0 {
		s.startCleanupRoutine()
	}

	// Always start async summary workers by default
	s.startAsyncSummaryWorker()

	return s
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
	opts ...session.Option,
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
		State:     make(session.StateMap), // Initialize with provided state
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Set initial state if provided
	for k, v := range state {
		sess.State[k] = v
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.sessions[key.UserID] == nil {
		app.sessions[key.UserID] = make(map[string]*sessionWithTTL)
	}

	if app.userState[key.UserID] == nil {
		app.userState[key.UserID] = &stateWithTTL{
			data:      make(session.StateMap),
			expiredAt: calculateExpiredAt(s.opts.userStateTTL),
		}
	}

	// Store the session with TTL
	app.sessions[key.UserID][key.SessionID] = &sessionWithTTL{
		session:   sess,
		expiredAt: calculateExpiredAt(s.opts.sessionTTL),
	}

	// Create a copy and merge state for return
	copiedSess := copySession(sess)
	appState := getValidState(app.appState)
	userState := getValidState(app.userState[key.UserID])
	if appState == nil {
		appState = make(session.StateMap)
	}
	if userState == nil {
		userState = make(session.StateMap)
	}
	return mergeState(appState, userState, copiedSess), nil
}

// GetSession retrieves a session by app name, user ID, and session ID.
func (s *SessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	app, ok := s.getAppSessions(key.AppName)
	if !ok {
		return nil, nil
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if _, ok := app.sessions[key.UserID]; !ok {
		return nil, nil
	}
	sessWithTTL, ok := app.sessions[key.UserID][key.SessionID]
	if !ok {
		return nil, nil
	}

	// Check if session is expired
	sess := getValidSession(sessWithTTL)
	if sess == nil {
		return nil, nil
	}

	// Refresh TTL on access
	sessWithTTL.expiredAt = calculateExpiredAt(s.opts.sessionTTL)

	copiedSess := copySession(sess)

	// apply filtering options if provided
	isession.ApplyEventFiltering(copiedSess, opts...)
	// filter events to ensure they start with RoleUser
	isession.EnsureEventStartWithUser(copiedSess)

	appState := getValidState(app.appState)
	userState := getValidState(app.userState[key.UserID])
	if appState == nil {
		appState = make(session.StateMap)
	}
	if userState == nil {
		userState = make(session.StateMap)
	}
	return mergeState(appState, userState, copiedSess), nil
}

// ListSessions returns all sessions for a given app and user.
func (s *SessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
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
	for _, sWithTTL := range app.sessions[userKey.UserID] {
		// Check if session is expired
		s := getValidSession(sWithTTL)
		if s == nil {
			continue // Skip expired sessions
		}
		copiedSess := copySession(s)
		isession.ApplyEventFiltering(copiedSess, opts...)
		// filter events to ensure they start with RoleUser
		isession.EnsureEventStartWithUser(copiedSess)

		appState := getValidState(app.appState)
		userState := getValidState(app.userState[userKey.UserID])
		if appState == nil {
			appState = make(session.StateMap)
		}
		if userState == nil {
			userState = make(session.StateMap)
		}
		sessList = append(sessList, mergeState(appState, userState, copiedSess))
	}
	return sessList, nil
}

// DeleteSession removes a session from storage.
func (s *SessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
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

	if _, ok := app.sessions[key.UserID]; !ok {
		return nil
	}
	if _, ok := app.sessions[key.UserID][key.SessionID]; !ok {
		return nil
	}

	// Delete the session
	delete(app.sessions[key.UserID], key.SessionID)

	// Clean up empty user sessions map
	if len(app.sessions[key.UserID]) == 0 {
		delete(app.sessions, key.UserID)
	}

	return nil
}

// UpdateAppState updates the app state.
func (s *SessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	// if app not found, create a new one
	app := s.getOrCreateAppSessions(appName)

	app.mu.Lock()
	defer app.mu.Unlock()

	for k, v := range state {
		copiedValue := make([]byte, len(v))
		copy(copiedValue, v)
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		app.appState.data[k] = copiedValue
	}
	// Update expiration time
	app.appState.expiredAt = calculateExpiredAt(s.opts.appStateTTL)
	return nil
}

// DeleteAppState deletes the app state.
func (s *SessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	// if app not found, return nil
	app, ok := s.getAppSessions(appName)
	if !ok {
		return nil
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	key = strings.TrimPrefix(key, session.StateAppPrefix)
	delete(app.appState.data, key)
	return nil
}

// ListAppStates gets the app states.
func (s *SessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}

	// if app not found, return empty state map
	app, ok := s.getAppSessions(appName)
	if !ok {
		return make(session.StateMap), nil
	}

	app.mu.RLock()
	defer app.mu.RUnlock()

	// Get valid app state (check expiration)
	appState := getValidState(app.appState)
	if appState == nil {
		return make(session.StateMap), nil
	}

	copiedState := make(session.StateMap)
	for k, v := range appState {
		copiedValue := make([]byte, len(v))
		copy(copiedValue, v)
		copiedState[k] = copiedValue
	}
	return copiedState, nil
}

// UpdateUserState updates the user state.
func (s *SessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// if app not found, create a new one
	app := s.getOrCreateAppSessions(userKey.AppName)

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.userState[userKey.UserID] == nil {
		app.userState[userKey.UserID] = &stateWithTTL{
			data:      make(session.StateMap),
			expiredAt: calculateExpiredAt(s.opts.userStateTTL),
		}
	}

	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf("memory session service update user state failed: %s is not allowed", k)
		}
		if strings.HasPrefix(k, session.StateTempPrefix) {
			return fmt.Errorf("memory session service update user state failed: %s is not allowed", k)
		}
	}

	for k, v := range state {
		copiedValue := make([]byte, len(v))
		copy(copiedValue, v)
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		app.userState[userKey.UserID].data[k] = copiedValue
	}
	// Update expiration time
	app.userState[userKey.UserID].expiredAt = calculateExpiredAt(s.opts.userStateTTL)
	return nil
}

// DeleteUserState deletes the user state.
func (s *SessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// if app not found, return nil
	app, ok := s.getAppSessions(userKey.AppName)
	if !ok {
		return nil
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.userState[userKey.UserID] == nil {
		return nil
	}

	key = strings.TrimPrefix(key, session.StateUserPrefix)
	delete(app.userState[userKey.UserID].data, key)

	if len(app.userState[userKey.UserID].data) == 0 {
		delete(app.userState, userKey.UserID)
	}

	return nil
}

// ListUserStates gets the user states.
func (s *SessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	app, ok := s.getAppSessions(userKey.AppName)
	if !ok {
		return make(session.StateMap), nil
	}

	app.mu.RLock()
	defer app.mu.RUnlock()
	userStateWithTTL, ok := app.userState[userKey.UserID]
	if !ok {
		return make(session.StateMap), nil
	}

	// Get valid user state (check expiration)
	userState := getValidState(userStateWithTTL)
	if userState == nil {
		return make(session.StateMap), nil
	}

	copiedState := make(session.StateMap)
	for k, v := range userState {
		copiedValue := make([]byte, len(v))
		copy(copiedValue, v)
		copiedState[k] = copiedValue
	}
	return copiedState, nil
}

// AppendEvent appends an event to a session.
func (s *SessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	event *event.Event,
	opts ...session.Option,
) error {
	isession.UpdateUserSession(sess, event, opts...)
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

	storedSessionWithTTL, ok := userSessions[key.SessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", key.SessionID)
	}

	// Check if session is expired
	storedSession := getValidSession(storedSessionWithTTL)
	if storedSession == nil {
		return fmt.Errorf("session expired: %s", key.SessionID)
	}

	// update stored session with the given event
	s.updateStoredSession(storedSession, event)

	// Update the session in the wrapper and refresh TTL
	storedSessionWithTTL.session = storedSession
	storedSessionWithTTL.expiredAt = calculateExpiredAt(s.opts.sessionTTL)
	return nil
}

// cleanupExpired removes all expired sessions and states.
func (s *SessionService) cleanupExpired() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, app := range s.apps {
		app.mu.Lock()
		// Clean expired sessions
		for userID, userSessions := range app.sessions {
			for sessionID, sessionWithTTL := range userSessions {
				if isExpired(sessionWithTTL.expiredAt) {
					delete(userSessions, sessionID)
				}
			}
			// Remove empty user session maps
			if len(userSessions) == 0 {
				delete(app.sessions, userID)
			}
		}

		// Clean expired user states
		for userID, userState := range app.userState {
			if isExpired(userState.expiredAt) {
				delete(app.userState, userID)
			}
		}

		// Clean expired app state
		if isExpired(app.appState.expiredAt) {
			app.appState.data = make(session.StateMap)
			app.appState.expiredAt = time.Time{}
		}
		app.mu.Unlock()
	}
}

// startCleanupRoutine starts the background cleanup routine.
func (s *SessionService) startCleanupRoutine() {
	s.cleanupTicker = time.NewTicker(s.opts.cleanupInterval)
	ticker := s.cleanupTicker // Capture ticker to avoid race condition
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.cleanupExpired()
			case <-s.cleanupDone:
				return
			}
		}
	}()
}

// stopCleanupRoutine stops the background cleanup routine.
func (s *SessionService) stopCleanupRoutine() {
	s.cleanupOnce.Do(func() {
		if s.cleanupTicker != nil {
			close(s.cleanupDone)
			s.cleanupTicker = nil
		}
	})
}

// Close closes the service.
func (s *SessionService) Close() error {
	s.stopCleanupRoutine()
	s.stopAsyncSummaryWorker()
	return nil
}

// updateStoredSession updates the stored session with the given event.
func (s *SessionService) updateStoredSession(sess *session.Session, e *event.Event) {
	if e.Response != nil && !e.IsPartial && e.IsValidContent() {
		sess.EventMu.Lock()
		sess.Events = append(sess.Events, *e)
		if s.opts.sessionEventLimit > 0 && len(sess.Events) > s.opts.sessionEventLimit {
			sess.Events = sess.Events[len(sess.Events)-s.opts.sessionEventLimit:]
		}
		sess.EventMu.Unlock()
	}

	sess.UpdatedAt = time.Now()
	// Merge event state delta to session state.
	isession.ApplyEventStateDelta(sess, e)
}

// copySession creates a  copy of a session.
func copySession(sess *session.Session) *session.Session {
	sess.EventMu.RLock()
	copiedSess := &session.Session{
		ID:        sess.ID,
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		State:     make(session.StateMap), // Create new state to avoid reference sharing.
		Events:    make([]event.Event, len(sess.Events)),
		UpdatedAt: sess.UpdatedAt,
		CreatedAt: sess.CreatedAt, // Add missing CreatedAt field.
	}

	// Copy events.
	copy(copiedSess.Events, sess.Events)
	sess.EventMu.RUnlock()

	// Copy state.
	if sess.State != nil {
		for k, v := range sess.State {
			copiedValue := make([]byte, len(v))
			copy(copiedValue, v)
			copiedSess.State[k] = copiedValue
		}
	}

	// Copy summaries.
	sess.SummariesMu.RLock()
	if sess.Summaries != nil {
		copiedSess.Summaries = make(map[string]*session.Summary, len(sess.Summaries))
		for b, sum := range sess.Summaries {
			if sum == nil {
				continue
			}
			// Shallow copy is fine since Summary is immutable after write.
			copied := *sum
			copiedSess.Summaries[b] = &copied
		}
	}
	sess.SummariesMu.RUnlock()
	return copiedSess
}

// mergeState merges app-level and user-level state into the session state.
func mergeState(appState, userState session.StateMap, sess *session.Session) *session.Session {
	for k, v := range appState {
		sess.State[session.StateAppPrefix+k] = v
	}
	for k, v := range userState {
		sess.State[session.StateUserPrefix+k] = v
	}
	return sess
}
