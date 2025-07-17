//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	defaultSessionEventLimit = 100
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

// serviceOpts is the options for session service.
type serviceOpts struct {
	// sessionEventLimit is the limit of events in a session.
	sessionEventLimit int
}

// SessionService provides an in-memory implementation of SessionService.
type SessionService struct {
	mu   sync.RWMutex
	apps map[string]*appSessions
	opts serviceOpts
}

// ServiceOpt is the option for the in-memory session service.
type ServiceOpt func(*serviceOpts)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.sessionEventLimit = limit
	}
}

// NewSessionService creates a new in-memory session service.
func NewSessionService(options ...ServiceOpt) *SessionService {
	opts := serviceOpts{
		sessionEventLimit: defaultSessionEventLimit,
	}
	for _, option := range options {
		option(&opts)
	}
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
		app.sessions[key.UserID] = make(map[string]*session.Session)
	}

	if app.userState[key.UserID] == nil {
		app.userState[key.UserID] = make(session.StateMap)
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
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
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
	applyGetSessionOptions(copiedSess, opt)
	return mergeState(app.appState, app.userState[key.UserID], copiedSess), nil
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
	opt := applyOptions(opts...)
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
		applyGetSessionOptions(copiedSess, opt)
		sessList = append(sessList, mergeState(app.appState, app.userState[userKey.UserID], copiedSess))
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
		app.appState[k] = copiedValue
	}
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
	delete(app.appState, key)
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

	copiedState := make(session.StateMap)
	for k, v := range app.appState {
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
		app.userState[userKey.UserID] = make(session.StateMap)
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
		app.userState[userKey.UserID][k] = copiedValue
	}
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
	delete(app.userState[userKey.UserID], key)

	if len(app.userState[userKey.UserID]) == 0 {
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
	userState, ok := app.userState[userKey.UserID]
	if !ok {
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

// Close closes the service.
func (s *SessionService) Close() error {
	return nil
}

func (s *SessionService) updateSessionState(sess *session.Session, event *event.Event) {
	sess.Events = append(sess.Events, *event)
	if s.opts.sessionEventLimit > 0 && len(sess.Events) > s.opts.sessionEventLimit {
		sess.Events = sess.Events[len(sess.Events)-s.opts.sessionEventLimit:]
	}
	sess.UpdatedAt = time.Now()
}

// copySession creates a  copy of a session.
func copySession(sess *session.Session) *session.Session {
	copiedSess := &session.Session{
		ID:        sess.ID,
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		State:     make(session.StateMap), // Create new state to avoid reference sharing
		Events:    make([]event.Event, len(sess.Events)),
		UpdatedAt: sess.UpdatedAt,
		CreatedAt: sess.CreatedAt, // Add missing CreatedAt field
	}

	// copy state
	if sess.State != nil {
		for k, v := range sess.State {
			copiedValue := make([]byte, len(v))
			copy(copiedValue, v)
			copiedSess.State[k] = copiedValue
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
		sess.State[session.StateAppPrefix+k] = v
	}
	for k, v := range userState {
		sess.State[session.StateUserPrefix+k] = v
	}
	return sess
}

func applyOptions(opts ...session.Option) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}
