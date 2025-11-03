//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package postgres provides the postgres session service.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spaolacci/murmur3"
	"trpc.group/trpc-go/trpc-agent-go/event"
	isession "trpc.group/trpc-go/trpc-agent-go/internal/session"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

var _ session.Service = (*Service)(nil)

const (
	defaultSessionEventLimit     = 1000
	defaultChanBufferSize        = 100
	defaultAsyncPersisterNum     = 10
	defaultCleanupIntervalSecond = 300 // 5 min

	defaultAsyncSummaryNum  = 3
	defaultSummaryQueueSize = 256

	defaultHost     = "localhost"
	defaultPort     = 5432
	defaultDatabase = "trpc-agent-go-pgsession"
	defaultSSLMode  = "disable"
)

// SessionState is the state of a session.
type SessionState struct {
	ID        string           `json:"id"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// Service is the postgres session service.
type Service struct {
	opts            ServiceOpts
	pgClient        storage.Client
	sessionTTL      time.Duration            // TTL for session state and event list
	appStateTTL     time.Duration            // TTL for app state
	userStateTTL    time.Duration            // TTL for user state
	eventPairChans  []chan *sessionEventPair // channel for session events to persistence
	summaryJobChans []chan *summaryJob       // channel for summary jobs to processing
	cleanupTicker   *time.Ticker             // ticker for automatic cleanup
	cleanupDone     chan struct{}            // signal to stop cleanup routine
	cleanupOnce     sync.Once                // ensure cleanup routine is stopped only once
	once            sync.Once
}

type sessionEventPair struct {
	key   session.Key
	event *event.Event
}

// summaryJob represents a summary job to be processed asynchronously.
type summaryJob struct {
	sessionKey session.Key
	filterKey  string
	force      bool
	session    *session.Session
}

// buildConnString builds a PostgreSQL connection string from options.
func buildConnString(opts ServiceOpts) string {
	// Default values
	host := opts.host
	if host == "" {
		host = defaultHost
	}
	port := opts.port
	if port == 0 {
		port = defaultPort
	}
	database := opts.database
	if database == "" {
		database = defaultDatabase
	}
	sslMode := opts.sslMode
	if sslMode == "" {
		sslMode = defaultSSLMode
	}

	// Build connection string
	connString := fmt.Sprintf("host=%s port=%d dbname=%s sslmode=%s",
		host, port, database, sslMode)

	if opts.user != "" {
		connString += fmt.Sprintf(" user=%s", opts.user)
	}
	if opts.password != "" {
		connString += fmt.Sprintf(" password=%s", opts.password)
	}

	return connString
}

// NewService creates a new postgres session service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := ServiceOpts{
		sessionEventLimit:  defaultSessionEventLimit,
		sessionTTL:         0,
		appStateTTL:        0,
		userStateTTL:       0,
		asyncPersisterNum:  defaultAsyncPersisterNum,
		enableAsyncPersist: false,
		asyncSummaryNum:    defaultAsyncSummaryNum,
		summaryQueueSize:   defaultSummaryQueueSize,
		summaryJobTimeout:  30 * time.Second,
		softDelete:         true, // Enable soft delete by default
		cleanupInterval:    0,
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

	var pgClient storage.Client
	var err error
	builder := storage.GetClientBuilder()

	// Priority: direct connection settings > instance name
	// If direct connection settings are provided, use them
	if opts.host != "" {
		connString := buildConnString(opts)
		pgClient, err = builder(
			context.Background(),
			storage.WithClientConnString(connString),
			storage.WithExtraOptions(opts.extraOptions...),
		)
		if err != nil {
			return nil, fmt.Errorf("create postgres client from connection settings failed: %w", err)
		}
	} else if opts.instanceName != "" {
		// Otherwise, use instance name if provided
		builderOpts, ok := storage.GetPostgresInstance(opts.instanceName)
		if !ok {
			return nil, fmt.Errorf("postgres instance %s not found", opts.instanceName)
		}
		pgClient, err = builder(context.Background(), builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create postgres client from instance name failed: %w", err)
		}
	} else {
		return nil, fmt.Errorf("either connection settings (host, port, etc.) or instance name must be provided")
	}

	s := &Service{
		opts:         opts,
		pgClient:     pgClient,
		sessionTTL:   opts.sessionTTL,
		appStateTTL:  opts.appStateTTL,
		userStateTTL: opts.userStateTTL,
		cleanupDone:  make(chan struct{}),
	}

	// Initialize database schema
	if err := s.initDB(context.Background()); err != nil {
		return nil, fmt.Errorf("initialize database schema failed: %w", err)
	}

	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}
	// Always start async summary workers by default.
	s.startAsyncSummaryWorker()

	// Start automatic cleanup if cleanup interval is configured
	if opts.cleanupInterval > 0 {
		s.startCleanupRoutine()
	}

	return s, nil
}

// CreateSession creates a new session.
func (s *Service) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}
	if key.SessionID == "" {
		key.SessionID = uuid.New().String()
	}

	now := time.Now().UTC()
	sessState := &SessionState{
		ID:        key.SessionID,
		State:     make(session.StateMap),
		UpdatedAt: now,
		CreatedAt: now,
	}
	for k, v := range state {
		sessState.State[k] = v
	}

	sessBytes, err := json.Marshal(sessState)
	if err != nil {
		return nil, fmt.Errorf("marshal session failed: %w", err)
	}

	// Calculate expires_at based on TTL
	var expiresAt *time.Time
	if s.sessionTTL > 0 {
		t := now.Add(s.sessionTTL)
		expiresAt = &t
	}

	// Delete existing session if any (to avoid unique constraint violation on expired sessions)
	if err := s.deleteSessionState(ctx, key); err != nil {
		log.Infof("delete existing session before create: %v", err)
	}

	// Insert session state
	_, err = s.pgClient.ExecContext(ctx,
		`INSERT INTO session_states (app_name, user_id, session_id, state, created_at, updated_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		key.AppName, key.UserID, key.SessionID, sessBytes, sessState.CreatedAt, sessState.UpdatedAt, expiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create session failed: %w", err)
	}

	// Query app and user states
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, fmt.Errorf("list app states failed: %w", err)
	}

	userState, err := s.ListUserStates(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	if err != nil {
		return nil, fmt.Errorf("list user states failed: %w", err)
	}

	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     sessState.State,
		Events:    []event.Event{},
		Summaries: make(map[string]*session.Summary),
		UpdatedAt: sessState.UpdatedAt,
		CreatedAt: sessState.CreatedAt,
	}

	return mergeState(appState, userState, sess), nil
}

// GetSession gets a session.
func (s *Service) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	sess, err := s.getSession(ctx, key, opt.EventNum, opt.EventTime)
	if err != nil {
		return nil, fmt.Errorf("postgres session service get session state failed: %w", err)
	}
	return sess, nil
}

// ListSessions lists all sessions by user scope of session key.
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	sessList, err := s.listSessions(ctx, userKey, opt.EventNum, opt.EventTime)
	if err != nil {
		return nil, fmt.Errorf("postgres session service get session list failed: %w", err)
	}
	return sessList, nil
}

// DeleteSession deletes a session.
func (s *Service) DeleteSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	if err := s.deleteSessionState(ctx, key); err != nil {
		return fmt.Errorf("postgres session service delete session state failed: %w", err)
	}
	return nil
}

// UpdateAppState updates the state by target scope and key.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	now := time.Now().UTC()
	var expiresAt *time.Time
	if s.appStateTTL > 0 {
		t := now.Add(s.appStateTTL)
		expiresAt = &t
	}

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		_, err := s.pgClient.ExecContext(ctx,
			`INSERT INTO app_states (app_name, key, value, updated_at, expires_at)
			 VALUES ($1, $2, $3, $4, $5)`,
			appName, k, v, now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf("postgres session service update app state failed: %w", err)
		}
	}
	return nil
}

// ListAppStates gets the app states.
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}

	appStateMap := make(session.StateMap)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var key string
			var value []byte
			if err := rows.Scan(&key, &value); err != nil {
				return err
			}
			appStateMap[key] = value
		}
		return nil
	}, `SELECT key, value FROM app_states
		WHERE app_name = $1
		AND (expires_at IS NULL OR expires_at > $2)
		AND deleted_at IS NULL`,
		appName, time.Now().UTC())

	if err != nil {
		return nil, fmt.Errorf("postgres session service list app states failed: %w", err)
	}
	return appStateMap, nil
}

// DeleteAppState deletes the state by target scope and key.
func (s *Service) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	var err error
	if s.opts.softDelete {
		// Soft delete: set deleted_at timestamp
		_, err = s.pgClient.ExecContext(ctx,
			`UPDATE app_states SET deleted_at = $1
			 WHERE app_name = $2 AND key = $3 AND deleted_at IS NULL`,
			time.Now().UTC(), appName, key)
	} else {
		// Hard delete: permanently remove record
		_, err = s.pgClient.ExecContext(ctx,
			`DELETE FROM app_states
			 WHERE app_name = $1 AND key = $2`,
			appName, key)
	}

	if err != nil {
		return fmt.Errorf("postgres session service delete app state failed: %w", err)
	}
	return nil
}

// UpdateUserState updates the state by target scope and key.
func (s *Service) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	now := time.Now().UTC()
	var expiresAt *time.Time
	if s.userStateTTL > 0 {
		t := now.Add(s.userStateTTL)
		expiresAt = &t
	}

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		_, err := s.pgClient.ExecContext(ctx,
			`INSERT INTO user_states (app_name, user_id, key, value, updated_at, expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			userKey.AppName, userKey.UserID, k, v, now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf("postgres session service update user state failed: %w", err)
		}
	}
	return nil
}

// ListUserStates lists the state by target scope and key.
func (s *Service) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	userStateMap := make(session.StateMap)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var key string
			var value []byte
			if err := rows.Scan(&key, &value); err != nil {
				return err
			}
			userStateMap[key] = value
		}
		return nil
	}, `SELECT key, value FROM user_states
		WHERE app_name = $1 AND user_id = $2
		AND (expires_at IS NULL OR expires_at > $3)
		AND deleted_at IS NULL`,
		userKey.AppName, userKey.UserID, time.Now().UTC())

	if err != nil {
		return nil, fmt.Errorf("postgres session service list user states failed: %w", err)
	}
	return userStateMap, nil
}

// DeleteUserState deletes the state by target scope and key.
func (s *Service) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	var err error
	if s.opts.softDelete {
		_, err = s.pgClient.ExecContext(ctx,
			`UPDATE user_states SET deleted_at = $1
			 WHERE app_name = $2 AND user_id = $3 AND key = $4 AND deleted_at IS NULL`,
			time.Now().UTC(), userKey.AppName, userKey.UserID, key)
	} else {
		_, err = s.pgClient.ExecContext(ctx,
			`DELETE FROM user_states
			 WHERE app_name = $1 AND user_id = $2 AND key = $3`,
			userKey.AppName, userKey.UserID, key)
	}
	if err != nil {
		return fmt.Errorf("postgres session service delete user state failed: %w", err)
	}
	return nil
}

// AppendEvent appends an event to a session.
func (s *Service) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	event *event.Event,
	opts ...session.Option,
) error {
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	// update user session with the given event
	isession.UpdateUserSession(sess, event, opts...)

	// persist event to postgres asynchronously
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && err.Error() == "send on closed channel" {
					log.Errorf("postgres session service append event failed: %v", r)
					return
				}
				panic(r)
			}
		}()

		// TODO: Init hash index at session creation to prevent duplicate computation.
		hKey := fmt.Sprintf("%s:%s:%s", key.AppName, key.UserID, key.SessionID)
		n := len(s.eventPairChans)
		index := int(murmur3.Sum32([]byte(hKey))) % n
		select {
		case s.eventPairChans[index] <- &sessionEventPair{key: key, event: event}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	if err := s.addEvent(ctx, key, event); err != nil {
		return fmt.Errorf("postgres session service append event failed: %w", err)
	}

	return nil
}

// Close closes the service.
func (s *Service) Close() error {
	s.once.Do(func() {
		// Stop cleanup routine
		s.stopCleanupRoutine()

		// close postgres connection
		if s.pgClient != nil {
			s.pgClient.Close()
		}

		for _, ch := range s.eventPairChans {
			close(ch)
		}

		for _, ch := range s.summaryJobChans {
			close(ch)
		}
	})

	return nil
}

func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	// Query session state (always filter deleted records)
	var sessState *SessionState
	stateQuery := `SELECT state, created_at, updated_at FROM session_states
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND (expires_at IS NULL OR expires_at > $4)
		AND deleted_at IS NULL`
	stateArgs := []interface{}{key.AppName, key.UserID, key.SessionID, time.Now().UTC()}

	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			var stateBytes []byte
			var createdAt, updatedAt time.Time
			if err := rows.Scan(&stateBytes, &createdAt, &updatedAt); err != nil {
				return err
			}
			sessState = &SessionState{}
			if err := json.Unmarshal(stateBytes, sessState); err != nil {
				return fmt.Errorf("unmarshal session state failed: %w", err)
			}
			sessState.CreatedAt = createdAt
			sessState.UpdatedAt = updatedAt
		}
		return nil
	}, stateQuery, stateArgs...)

	if err != nil {
		return nil, fmt.Errorf("get session state failed: %w", err)
	}
	if sessState == nil {
		return nil, nil
	}

	// Query app state
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}

	// Query user state
	userState, err := s.ListUserStates(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	if err != nil {
		return nil, err
	}

	// Query events (always filter deleted records)
	events := []event.Event{}
	var eventQuery string
	var eventArgs []interface{}

	now := time.Now().UTC()
	if limit > 0 {
		eventQuery = `SELECT event FROM session_events
			WHERE app_name = $1 AND user_id = $2 AND session_id = $3
			AND (expires_at IS NULL OR expires_at > $4)
			AND created_at > $5
			AND deleted_at IS NULL
			ORDER BY created_at DESC
			LIMIT $6`
		eventArgs = []interface{}{key.AppName, key.UserID, key.SessionID, now, afterTime, limit}
	} else {
		eventQuery = `SELECT event FROM session_events
			WHERE app_name = $1 AND user_id = $2 AND session_id = $3
			AND (expires_at IS NULL OR expires_at > $4)
			AND created_at > $5
			AND deleted_at IS NULL
			ORDER BY created_at DESC`
		eventArgs = []interface{}{key.AppName, key.UserID, key.SessionID, now, afterTime}
	}

	err = s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var eventBytes []byte
			if err := rows.Scan(&eventBytes); err != nil {
				return err
			}
			var evt event.Event
			if err := json.Unmarshal(eventBytes, &evt); err != nil {
				return fmt.Errorf("unmarshal event failed: %w", err)
			}
			events = append(events, evt)
		}
		return nil
	}, eventQuery, eventArgs...)

	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}

	// Reverse events to get chronological order
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	// Query summaries (always filter deleted records)
	summaries := make(map[string]*session.Summary)
	summaryQuery := `SELECT filter_key, summary FROM session_summaries
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND (expires_at IS NULL OR expires_at > $4)
		AND deleted_at IS NULL`
	summaryArgs := []interface{}{key.AppName, key.UserID, key.SessionID, time.Now()}

	err = s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var filterKey string
			var summaryBytes []byte
			if err := rows.Scan(&filterKey, &summaryBytes); err != nil {
				return err
			}
			var sum session.Summary
			if err := json.Unmarshal(summaryBytes, &sum); err != nil {
				return fmt.Errorf("unmarshal summary failed: %w", err)
			}
			summaries[filterKey] = &sum
		}
		return nil
	}, summaryQuery, summaryArgs...)

	if err != nil {
		return nil, fmt.Errorf("get summaries failed: %w", err)
	}

	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     sessState.State,
		Events:    events,
		Summaries: summaries,
		UpdatedAt: sessState.UpdatedAt,
		CreatedAt: sessState.CreatedAt,
	}

	return mergeState(appState, userState, sess), nil
}

func (s *Service) listSessions(
	ctx context.Context,
	key session.UserKey,
	limit int,
	afterTime time.Time,
) ([]*session.Session, error) {
	// Query app state
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}

	// Query user state
	userState, err := s.ListUserStates(ctx, key)
	if err != nil {
		return nil, err
	}

	// Query all session states for this user (always filter deleted records)
	var sessStates []*SessionState
	listQuery := `SELECT session_id, state, created_at, updated_at FROM session_states
		WHERE app_name = $1 AND user_id = $2
		AND (expires_at IS NULL OR expires_at > $3)
		AND deleted_at IS NULL
		ORDER BY updated_at DESC`
	listArgs := []interface{}{key.AppName, key.UserID, time.Now().UTC()}

	err = s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var sessionID string
			var stateBytes []byte
			var createdAt, updatedAt time.Time
			if err := rows.Scan(&sessionID, &stateBytes, &createdAt, &updatedAt); err != nil {
				return err
			}
			var state SessionState
			if err := json.Unmarshal(stateBytes, &state); err != nil {
				return fmt.Errorf("unmarshal session state failed: %w", err)
			}
			state.ID = sessionID
			state.CreatedAt = createdAt
			state.UpdatedAt = updatedAt
			sessStates = append(sessStates, &state)
		}
		return nil
	}, listQuery, listArgs...)

	if err != nil {
		return nil, fmt.Errorf("list session states failed: %w", err)
	}

	// Build session keys for batch loading events and summaries
	sessionKeys := make([]session.Key, 0, len(sessStates))
	for _, sessState := range sessStates {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: sessState.ID,
		})
	}

	// Batch load events for all sessions
	eventsList, err := s.getEventsList(ctx, sessionKeys, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events list failed: %w", err)
	}

	// Batch load summaries for all sessions
	summariesList, err := s.getSummariesList(ctx, sessionKeys)
	if err != nil {
		return nil, fmt.Errorf("get summaries list failed: %w", err)
	}

	sessions := make([]*session.Session, 0, len(sessStates))
	for i, sessState := range sessStates {
		sess := &session.Session{
			ID:        sessState.ID,
			AppName:   key.AppName,
			UserID:    key.UserID,
			State:     sessState.State,
			Events:    eventsList[i],
			Summaries: summariesList[i],
			UpdatedAt: sessState.UpdatedAt,
			CreatedAt: sessState.CreatedAt,
		}
		sessions = append(sessions, mergeState(appState, userState, sess))
	}

	return sessions, nil
}

func (s *Service) addEvent(ctx context.Context, key session.Key, event *event.Event) error {
	now := time.Now().UTC()

	// Get current session state (always filter deleted records, but allow expired sessions)
	var sessState *SessionState
	var currentExpiresAt *time.Time
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			var stateBytes []byte
			if err := rows.Scan(&stateBytes, &currentExpiresAt); err != nil {
				return err
			}
			sessState = &SessionState{}
			if err := json.Unmarshal(stateBytes, sessState); err != nil {
				return fmt.Errorf("unmarshal session state failed: %w", err)
			}
		}
		return nil
	}, `SELECT state, expires_at FROM session_states
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND deleted_at IS NULL`,
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}
	if sessState == nil {
		return fmt.Errorf("session not found")
	}

	// Check if session is expired, log info if so
	if currentExpiresAt != nil && currentExpiresAt.Before(now) {
		log.Infof("appending event to expired session (app=%s, user=%s, session=%s), will extend expires_at",
			key.AppName, key.UserID, key.SessionID)
	}

	sessState.UpdatedAt = now
	if sessState.State == nil {
		sessState.State = make(session.StateMap)
	}
	isession.ApplyEventStateDeltaMap(sessState.State, event)
	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session state failed: %w", err)
	}

	eventBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event failed: %w", err)
	}

	var expiresAt *time.Time
	if s.sessionTTL > 0 {
		t := now.Add(s.sessionTTL)
		expiresAt = &t
	}

	// Use transaction to update session state and insert event
	err = s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		// Update session state
		_, err := tx.ExecContext(ctx,
			`UPDATE session_states SET state = $1, updated_at = $2, expires_at = $3
			 WHERE app_name = $4 AND user_id = $5 AND session_id = $6 AND deleted_at IS NULL`,
			updatedStateBytes, sessState.UpdatedAt, expiresAt,
			key.AppName, key.UserID, key.SessionID)
		if err != nil {
			return fmt.Errorf("update session state failed: %w", err)
		}

		// Insert event if it has response and is not partial
		if event.Response != nil && !event.IsPartial && event.IsValidContent() {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO session_events (app_name, user_id, session_id, event, created_at, expires_at)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				key.AppName, key.UserID, key.SessionID, eventBytes, now, expiresAt)
			if err != nil {
				return fmt.Errorf("insert event failed: %w", err)
			}

			// Delete old events if limit is set
			if s.opts.sessionEventLimit > 0 {
				if s.opts.softDelete {
					// Soft delete: mark older events as deleted to enforce retention
					_, err = tx.ExecContext(ctx,
						`UPDATE session_events SET deleted_at = $4
						 WHERE app_name = $1 AND user_id = $2 AND session_id = $3
						 AND deleted_at IS NULL
						 AND id NOT IN (
							 SELECT id FROM session_events
							 WHERE app_name = $1 AND user_id = $2 AND session_id = $3
							 AND deleted_at IS NULL
							 ORDER BY created_at DESC
							 LIMIT $5
						 )`,
						key.AppName, key.UserID, key.SessionID, now.UTC(), s.opts.sessionEventLimit)
				} else {
					// Hard delete: physically remove older events
					_, err = tx.ExecContext(ctx,
						`DELETE FROM session_events
						 WHERE app_name = $1 AND user_id = $2 AND session_id = $3
						 AND deleted_at IS NULL
						 AND id NOT IN (
							 SELECT id FROM session_events
							 WHERE app_name = $1 AND user_id = $2 AND session_id = $3
							 AND deleted_at IS NULL
							 ORDER BY created_at DESC
							 LIMIT $4
						 )`,
						key.AppName, key.UserID, key.SessionID, s.opts.sessionEventLimit)
				}
				if err != nil {
					return fmt.Errorf("delete old events failed: %w", err)
				}
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("store event failed: %w", err)
	}
	return nil
}

func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	err := s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		if s.opts.softDelete {
			// Soft delete: set deleted_at timestamp
			now := time.Now().UTC()

			// Soft delete session state
			_, err := tx.ExecContext(ctx,
				`UPDATE session_states SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`,
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session summaries
			_, err = tx.ExecContext(ctx,
				`UPDATE session_summaries SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`,
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session events
			_, err = tx.ExecContext(ctx,
				`UPDATE session_events SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`,
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}
		} else {
			// Hard delete: permanently remove records

			// Delete session state
			_, err := tx.ExecContext(ctx,
				`DELETE FROM session_states
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`,
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session summaries
			_, err = tx.ExecContext(ctx,
				`DELETE FROM session_summaries
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`,
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session events
			_, err = tx.ExecContext(ctx,
				`DELETE FROM session_events
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`,
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("postgres session service delete session state failed: %w", err)
	}
	return nil
}

func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	// init event pair chan
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(chan *sessionEventPair, defaultChanBufferSize)
	}

	for _, eventPairChan := range s.eventPairChans {
		go func(eventPairChan chan *sessionEventPair) {
			for pair := range eventPairChan {
				ctx := context.Background()
				if err := s.addEvent(ctx, pair.key, pair.event); err != nil {
					log.Errorf("postgres session service async persist event failed: %v", err)
				}
			}
		}(eventPairChan)
	}
}

func (s *Service) startAsyncSummaryWorker() {
	if s.opts.summarizer == nil {
		return
	}

	summaryNum := s.opts.asyncSummaryNum
	queueSize := s.opts.summaryQueueSize

	// init summary job chan
	s.summaryJobChans = make([]chan *summaryJob, summaryNum)
	for i := 0; i < summaryNum; i++ {
		s.summaryJobChans[i] = make(chan *summaryJob, queueSize)
	}

	for _, summaryJobChan := range s.summaryJobChans {
		go func(jobChan chan *summaryJob) {
			for job := range jobChan {
				ctx, cancel := context.WithTimeout(context.Background(), s.opts.summaryJobTimeout)
				err := s.CreateSessionSummary(ctx, job.session, job.filterKey, job.force)
				if err != nil {
					log.Errorf("postgres session service async summary failed: %v", err)
				}
				cancel()
			}
		}(summaryJobChan)
	}
}

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

// getEventsList batch loads events for multiple sessions.
func (s *Service) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	limit int,
	afterTime time.Time,
) ([][]event.Event, error) {
	if len(sessionKeys) == 0 {
		return [][]event.Event{}, nil
	}

	// Build session IDs array
	sessionIDs := make([]string, len(sessionKeys))
	for i, key := range sessionKeys {
		sessionIDs[i] = key.SessionID
	}

	// Build query for all sessions (always filter deleted records)
	var query string
	var args []interface{}

	if limit > 0 {
		// With limit: use LIMIT clause for each session
		// Use LATERAL join to get limited events per session
		query = `
			SELECT s.session_id, e.event
			FROM (SELECT UNNEST($1::varchar[]) as session_id) s
			LEFT JOIN LATERAL (
				SELECT event FROM session_events
				WHERE app_name = $2 AND user_id = $3 AND session_id = s.session_id
				AND (expires_at IS NULL OR expires_at > $4)
				AND created_at > $5
				AND deleted_at IS NULL
				ORDER BY created_at DESC
				LIMIT $6
			) e ON true
			ORDER BY s.session_id, e.event`

		args = []interface{}{sessionIDs, sessionKeys[0].AppName, sessionKeys[0].UserID, time.Now().UTC(), afterTime, limit}
	} else {
		// Without limit: return all events for each session
		query = `
			SELECT s.session_id, e.event
			FROM (SELECT UNNEST($1::varchar[]) as session_id) s
			LEFT JOIN LATERAL (
				SELECT event FROM session_events
				WHERE app_name = $2 AND user_id = $3 AND session_id = s.session_id
				AND (expires_at IS NULL OR expires_at > $4)
				AND created_at > $5
				AND deleted_at IS NULL
				ORDER BY created_at DESC
			) e ON true
			ORDER BY s.session_id, e.event`

		args = []interface{}{sessionIDs, sessionKeys[0].AppName, sessionKeys[0].UserID, time.Now().UTC(), afterTime}
	}

	// Execute query and group events by session
	eventsMap := make(map[string][]event.Event)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var sessionID string
			var eventBytes []byte
			if err := rows.Scan(&sessionID, &eventBytes); err != nil {
				return err
			}

			// Skip null events (from LEFT JOIN when no events exist)
			if eventBytes == nil {
				continue
			}

			var evt event.Event
			if err := json.Unmarshal(eventBytes, &evt); err != nil {
				return fmt.Errorf("unmarshal event failed: %w", err)
			}
			eventsMap[sessionID] = append(eventsMap[sessionID], evt)
		}
		return nil
	}, query, args...)

	if err != nil {
		return nil, fmt.Errorf("query events failed: %w", err)
	}

	// Build result list in the same order as sessionKeys
	result := make([][]event.Event, len(sessionKeys))
	for i, key := range sessionKeys {
		events := eventsMap[key.SessionID]
		if events == nil {
			events = []event.Event{}
		}
		// Reverse events to get chronological order (oldest first)
		for j, k := 0, len(events)-1; j < k; j, k = j+1, k-1 {
			events[j], events[k] = events[k], events[j]
		}
		result[i] = events
	}

	return result, nil
}

// getSummariesList batch loads summaries for multiple sessions.
func (s *Service) getSummariesList(
	ctx context.Context,
	sessionKeys []session.Key,
) ([]map[string]*session.Summary, error) {
	if len(sessionKeys) == 0 {
		return []map[string]*session.Summary{}, nil
	}

	// Build session IDs array
	sessionIDs := make([]string, len(sessionKeys))
	for i, key := range sessionKeys {
		sessionIDs[i] = key.SessionID
	}

	// Query all summaries for all sessions (always filter deleted records)
	summaryQuery := `SELECT session_id, filter_key, summary FROM session_summaries
		WHERE app_name = $1 AND user_id = $2 AND session_id = ANY($3)
		AND (expires_at IS NULL OR expires_at > $4)
		AND deleted_at IS NULL`

	// Query all summaries for all sessions
	summariesMap := make(map[string]map[string]*session.Summary)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var sessionID, filterKey string
			var summaryBytes []byte
			if err := rows.Scan(&sessionID, &filterKey, &summaryBytes); err != nil {
				return err
			}

			var sum session.Summary
			if err := json.Unmarshal(summaryBytes, &sum); err != nil {
				return fmt.Errorf("unmarshal summary failed: %w", err)
			}

			if summariesMap[sessionID] == nil {
				summariesMap[sessionID] = make(map[string]*session.Summary)
			}
			summariesMap[sessionID][filterKey] = &sum
		}
		return nil
	}, summaryQuery, sessionKeys[0].AppName, sessionKeys[0].UserID, sessionIDs, time.Now().UTC())

	if err != nil {
		return nil, fmt.Errorf("query summaries failed: %w", err)
	}

	// Build result list in the same order as sessionKeys
	result := make([]map[string]*session.Summary, len(sessionKeys))
	for i, key := range sessionKeys {
		summaries := summariesMap[key.SessionID]
		if summaries == nil {
			summaries = make(map[string]*session.Summary)
		}
		result[i] = summaries
	}

	return result, nil
}

// cleanupExpired removes or soft-deletes all expired sessions and states.
func (s *Service) cleanupExpired() {
	ctx := context.Background()

	if s.opts.softDelete {
		// Soft delete expired data
		s.softDeleteExpired(ctx)
	} else {
		// Hard delete expired data
		s.hardDeleteExpired(ctx)
	}
}

// softDeleteExpired marks expired data as deleted.
func (s *Service) softDeleteExpired(ctx context.Context) {
	now := time.Now().UTC()

	// Soft delete expired session states
	if s.sessionTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`UPDATE session_states SET deleted_at = $1
			 WHERE expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`,
			now)
		if err != nil {
			log.Errorf("soft delete expired session states failed: %v", err)
		}
	}

	// Soft delete expired session events
	if s.sessionTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`UPDATE session_events SET deleted_at = $1
			 WHERE expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`,
			now)
		if err != nil {
			log.Errorf("soft delete expired session events failed: %v", err)
		}
	}

	// Soft delete expired session summaries
	if s.sessionTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`UPDATE session_summaries SET deleted_at = $1
			 WHERE expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`,
			now)
		if err != nil {
			log.Errorf("soft delete expired session summaries failed: %v", err)
		}
	}

	// Soft delete expired app states
	if s.appStateTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`UPDATE app_states SET deleted_at = $1
			 WHERE expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`,
			now)
		if err != nil {
			log.Errorf("soft delete expired app states failed: %v", err)
		}
	}

	// Soft delete expired user states
	if s.userStateTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`UPDATE user_states SET deleted_at = $1
			 WHERE expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`,
			now)
		if err != nil {
			log.Errorf("soft delete expired user states failed: %v", err)
		}
	}
}

// hardDeleteExpired physically removes expired data.
func (s *Service) hardDeleteExpired(ctx context.Context) {
	now := time.Now().UTC()

	// Hard delete expired session states
	if s.sessionTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`DELETE FROM session_states
			 WHERE expires_at IS NOT NULL AND expires_at <= $1`,
			now)
		if err != nil {
			log.Errorf("hard delete expired session states failed: %v", err)
		}
	}

	// Hard delete expired session events
	if s.sessionTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`DELETE FROM session_events
			 WHERE expires_at IS NOT NULL AND expires_at <= $1`,
			now)
		if err != nil {
			log.Errorf("hard delete expired session events failed: %v", err)
		}
	}

	// Hard delete expired session summaries
	if s.sessionTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`DELETE FROM session_summaries
			 WHERE expires_at IS NOT NULL AND expires_at <= $1`,
			now)
		if err != nil {
			log.Errorf("hard delete expired session summaries failed: %v", err)
		}
	}

	// Hard delete expired app states
	if s.appStateTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`DELETE FROM app_states
			 WHERE expires_at IS NOT NULL AND expires_at <= $1`,
			now)
		if err != nil {
			log.Errorf("hard delete expired app states failed: %v", err)
		}
	}

	// Hard delete expired user states
	if s.userStateTTL > 0 {
		_, err := s.pgClient.ExecContext(ctx,
			`DELETE FROM user_states
			 WHERE expires_at IS NOT NULL AND expires_at <= $1`,
			now)
		if err != nil {
			log.Errorf("hard delete expired user states failed: %v", err)
		}
	}
}

// startCleanupRoutine starts the background cleanup routine.
func (s *Service) startCleanupRoutine() {
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
func (s *Service) stopCleanupRoutine() {
	s.cleanupOnce.Do(func() {
		if s.cleanupTicker != nil {
			close(s.cleanupDone)
			s.cleanupTicker = nil
		}
	})
}
