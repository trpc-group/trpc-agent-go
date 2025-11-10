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
	defaultTimeout               = 5 * time.Second

	defaultAsyncSummaryNum  = 3
	defaultSummaryQueueSize = 100

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

	// Table names with prefix applied
	tableSessionStates    string
	tableSessionEvents    string
	tableSessionSummaries string
	tableAppStates        string
	tableUserStates       string
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

// buildFullTableName builds a full table name with optional schema and prefix.
// Examples:
// - schema="", prefix="", table="session_states" -> "session_states"
// - schema="myschema", prefix="", table="session_states" -> "myschema.session_states"
// - schema="", prefix="trpc_", table="session_states" -> "trpc_session_states"
// - schema="myschema", prefix="trpc_", table="session_states" -> "myschema.trpc_session_states"
func buildFullTableName(schema, prefix, tableName string) string {
	fullTableName := prefix + tableName
	if schema != "" {
		return schema + "." + fullTableName
	}
	return fullTableName
}

// parseTableName parses a full table name into schema and table components.
// Examples:
// - "session_states" -> ("public", "session_states")
// - "myschema.session_states" -> ("myschema", "session_states")
func parseTableName(fullTableName string) (schema, tableName string) {
	parts := strings.Split(fullTableName, ".")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "public", fullTableName
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

		// Initialize table names with schema and prefix
		tableSessionStates:    buildFullTableName(opts.schema, opts.tablePrefix, "session_states"),
		tableSessionEvents:    buildFullTableName(opts.schema, opts.tablePrefix, "session_events"),
		tableSessionSummaries: buildFullTableName(opts.schema, opts.tablePrefix, "session_summaries"),
		tableAppStates:        buildFullTableName(opts.schema, opts.tablePrefix, "app_states"),
		tableUserStates:       buildFullTableName(opts.schema, opts.tablePrefix, "user_states"),
	}

	// Initialize database schema unless skipped
	if !opts.skipDBInit {
		s.initDB(context.Background())
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

	now := time.Now()
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

	// Check if session already exists
	var sessionExists bool
	var existingExpiresAt sql.NullTime
	err = s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			sessionExists = true
			if err := rows.Scan(&existingExpiresAt); err != nil {
				return err
			}
		}
		return nil
	}, fmt.Sprintf(`SELECT expires_at FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)
	if err != nil {
		return nil, fmt.Errorf("check existing session failed: %w", err)
	}

	if sessionExists {
		if !existingExpiresAt.Valid {
			return nil, fmt.Errorf("session already exists and has not expired")
		}
		if existingExpiresAt.Time.After(now) {
			return nil, fmt.Errorf("session already exists and has not expired")
		}
		log.Infof("found expired session (app=%s,. user=%s, session=%s), triggering cleanup",
			key.AppName, key.UserID, key.SessionID)
		s.cleanupExpiredForUser(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	}

	// Insert session state
	_, err = s.pgClient.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, state, created_at, updated_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID, sessBytes, sessState.CreatedAt, sessState.UpdatedAt, expiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create session failed: %w", err)
	}

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

	// Refresh session TTL if configured and session exists
	if sess != nil && s.sessionTTL > 0 {
		if err := s.refreshSessionTTL(ctx, key); err != nil {
			log.Warnf("failed to refresh session TTL: %v", err)
			// Don't fail the GetSession call, just log the warning
		}
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

	now := time.Now()
	var expiresAt *time.Time
	if s.appStateTTL > 0 {
		t := now.Add(s.appStateTTL)
		expiresAt = &t
	}

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		// Use UPSERT to handle conflicts - update if exists, insert if not
		_, err := s.pgClient.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, key, value, updated_at, expires_at, deleted_at)
			 VALUES ($1, $2, $3, $4, $5, NULL)
			 ON CONFLICT (app_name, key) WHERE deleted_at IS NULL
			 DO UPDATE SET
			   value = EXCLUDED.value,
			   updated_at = EXCLUDED.updated_at,
			   expires_at = EXCLUDED.expires_at`, s.tableAppStates),
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
	}, fmt.Sprintf(`SELECT key, value FROM %s
		WHERE app_name = $1
		AND (expires_at IS NULL OR expires_at > $2)
		AND deleted_at IS NULL`, s.tableAppStates),
		appName, time.Now())

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
			fmt.Sprintf(`UPDATE %s SET deleted_at = $1
			 WHERE app_name = $2 AND key = $3 AND deleted_at IS NULL`, s.tableAppStates),
			time.Now(), appName, key)
	} else {
		// Hard delete: permanently remove record
		_, err = s.pgClient.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s
			 WHERE app_name = $1 AND key = $2`, s.tableAppStates),
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

	now := time.Now()
	var expiresAt *time.Time
	if s.userStateTTL > 0 {
		t := now.Add(s.userStateTTL)
		expiresAt = &t
	}

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		// Use UPSERT to handle conflicts - update if exists, insert if not
		_, err := s.pgClient.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, key, value, updated_at, expires_at, deleted_at)
			 VALUES ($1, $2, $3, $4, $5, $6, NULL)
			 ON CONFLICT (app_name, user_id, key) WHERE deleted_at IS NULL
			 DO UPDATE SET
			   value = EXCLUDED.value,
			   updated_at = EXCLUDED.updated_at,
			   expires_at = EXCLUDED.expires_at`, s.tableUserStates),
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
	}, fmt.Sprintf(`SELECT key, value FROM %s
		WHERE app_name = $1 AND user_id = $2
		AND (expires_at IS NULL OR expires_at > $3)
		AND deleted_at IS NULL`, s.tableUserStates),
		userKey.AppName, userKey.UserID, time.Now())

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
			fmt.Sprintf(`UPDATE %s SET deleted_at = $1
			 WHERE app_name = $2 AND user_id = $3 AND key = $4 AND deleted_at IS NULL`, s.tableUserStates),
			time.Now(), userKey.AppName, userKey.UserID, key)
	} else {
		_, err = s.pgClient.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s
			 WHERE app_name = $1 AND user_id = $2 AND key = $3`, s.tableUserStates),
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
	stateQuery := fmt.Sprintf(`SELECT state, created_at, updated_at FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND (expires_at IS NULL OR expires_at > $4)
		AND deleted_at IS NULL`, s.tableSessionStates)
	stateArgs := []any{key.AppName, key.UserID, key.SessionID, time.Now()}

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
	// Note: limit here only controls how many events to return, not delete from database
	events := []event.Event{}
	now := time.Now()
	var eventQuery string
	var eventArgs []any

	if limit > 0 {
		eventQuery = fmt.Sprintf(`SELECT event FROM %s
			WHERE app_name = $1 AND user_id = $2 AND session_id = $3
			AND (expires_at IS NULL OR expires_at > $4)
			AND created_at > $5
			AND deleted_at IS NULL
			ORDER BY created_at DESC
			LIMIT $6`, s.tableSessionEvents)
		eventArgs = []any{key.AppName, key.UserID, key.SessionID, now, afterTime, limit}
	} else {
		eventQuery = fmt.Sprintf(`SELECT event FROM %s
			WHERE app_name = $1 AND user_id = $2 AND session_id = $3
			AND (expires_at IS NULL OR expires_at > $4)
			AND created_at > $5
			AND deleted_at IS NULL
			ORDER BY created_at DESC`, s.tableSessionEvents)
		eventArgs = []any{key.AppName, key.UserID, key.SessionID, now, afterTime}
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
	summaryQuery := fmt.Sprintf(`SELECT filter_key, summary FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND (expires_at IS NULL OR expires_at > $4)
		AND deleted_at IS NULL`, s.tableSessionSummaries)
	summaryArgs := []any{key.AppName, key.UserID, key.SessionID, time.Now()}

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
	listQuery := fmt.Sprintf(`SELECT session_id, state, created_at, updated_at FROM %s
		WHERE app_name = $1 AND user_id = $2
		AND (expires_at IS NULL OR expires_at > $3)
		AND deleted_at IS NULL
		ORDER BY updated_at DESC`, s.tableSessionStates)
	listArgs := []any{key.AppName, key.UserID, time.Now()}

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
	// Note: limit here only controls how many events to return per session, not delete from database
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
	now := time.Now()

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
	}, fmt.Sprintf(`SELECT state, expires_at FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND deleted_at IS NULL`, s.tableSessionStates),
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
			fmt.Sprintf(`UPDATE %s SET state = $1, updated_at = $2, expires_at = $3
			 WHERE app_name = $4 AND user_id = $5 AND session_id = $6 AND deleted_at IS NULL`, s.tableSessionStates),
			updatedStateBytes, sessState.UpdatedAt, expiresAt,
			key.AppName, key.UserID, key.SessionID)
		if err != nil {
			return fmt.Errorf("update session state failed: %w", err)
		}

		// Insert event if it has response and is not partial
		if event.Response != nil && !event.IsPartial && event.IsValidContent() {
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, event, created_at, updated_at, expires_at)
				 VALUES ($1, $2, $3, $4, $5, $6, $7)`, s.tableSessionEvents),
				key.AppName, key.UserID, key.SessionID, eventBytes, now, now, expiresAt)
			if err != nil {
				return fmt.Errorf("insert event failed: %w", err)
			}

			// Enforce event limit if configured
			if s.opts.sessionEventLimit > 0 {
				if err := s.enforceEventLimit(ctx, tx, key, now); err != nil {
					return err
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

// enforceEventLimit removes old events beyond the configured limit.
// Strategy: Find the Nth newest event's created_at, then delete all events older than that.
func (s *Service) enforceEventLimit(ctx context.Context, tx *sql.Tx, key session.Key, now time.Time) error {
	if s.opts.softDelete {
		// Soft delete: mark events older than the Nth newest event
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s
			SET deleted_at = $4
			WHERE app_name = $1 AND user_id = $2 AND session_id = $3
			AND deleted_at IS NULL
			AND created_at < (
				SELECT created_at
				FROM %s
				WHERE app_name = $1 AND user_id = $2 AND session_id = $3
				AND deleted_at IS NULL
				ORDER BY created_at DESC
				OFFSET $5 LIMIT 1
			)`, s.tableSessionEvents, s.tableSessionEvents),
			key.AppName, key.UserID, key.SessionID, now, s.opts.sessionEventLimit)
		if err != nil {
			return fmt.Errorf("soft delete old events failed: %w", err)
		}
	} else {
		// Hard delete: physically remove events older than the Nth newest event
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s
			WHERE app_name = $1 AND user_id = $2 AND session_id = $3
			AND deleted_at IS NULL
			AND created_at < (
				SELECT created_at
				FROM %s
				WHERE app_name = $1 AND user_id = $2 AND session_id = $3
				AND deleted_at IS NULL
				ORDER BY created_at DESC
				OFFSET $4 LIMIT 1
			)`, s.tableSessionEvents, s.tableSessionEvents),
			key.AppName, key.UserID, key.SessionID, s.opts.sessionEventLimit)
		if err != nil {
			return fmt.Errorf("hard delete old events failed: %w", err)
		}
	}
	return nil
}

// refreshSessionTTL updates the session's updated_at and expires_at timestamps.
// This effectively "renews" the session, extending its lifetime by the configured TTL.
func (s *Service) refreshSessionTTL(ctx context.Context, key session.Key) error {
	now := time.Now()
	expiresAt := now.Add(s.sessionTTL)

	_, err := s.pgClient.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s
		SET updated_at = $1, expires_at = $2
		WHERE app_name = $3 AND user_id = $4 AND session_id = $5
		AND deleted_at IS NULL`, s.tableSessionStates),
		now, expiresAt, key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("refresh session TTL failed: %w", err)
	}
	return nil
}

func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	err := s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		if s.opts.softDelete {
			// Soft delete: set deleted_at timestamp
			now := time.Now()

			// Soft delete session state
			_, err := tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`, s.tableSessionStates),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session summaries
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`, s.tableSessionSummaries),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session events
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`, s.tableSessionEvents),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}
		} else {
			// Hard delete: permanently remove records

			// Delete session state
			_, err := tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`, s.tableSessionStates),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session summaries
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`, s.tableSessionSummaries),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session events
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`, s.tableSessionEvents),
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
				log.Debugf("Session persistence queue monitoring: channel capacity: %d, current length: %d, session key:(app: %s, user: %s, session: %s)",
					cap(eventPairChan), len(eventPairChan), pair.key.AppName, pair.key.UserID, pair.key.SessionID)
				ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
				if err := s.addEvent(ctx, pair.key, pair.event); err != nil {
					log.Errorf("postgres session service async persist event failed: %v", err)
				}
				cancel()
			}
		}(eventPairChan)
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
// Note: limit here only controls how many events to return per session, not delete from database
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

	// Query events for all sessions
	var query string
	var args []any

	if limit > 0 {
		// With limit: use LATERAL JOIN to apply limit per session
		query = fmt.Sprintf(`
			SELECT s.session_id, e.event
			FROM (SELECT UNNEST($1::varchar[]) as session_id) s
			LEFT JOIN LATERAL (
				SELECT event FROM %s
				WHERE app_name = $2 AND user_id = $3 AND session_id = s.session_id
				AND (expires_at IS NULL OR expires_at > $4)
				AND created_at > $5
				AND deleted_at IS NULL
				ORDER BY created_at DESC
				LIMIT $6
			) e ON true
			ORDER BY s.session_id`, s.tableSessionEvents)
		args = []any{sessionIDs, sessionKeys[0].AppName, sessionKeys[0].UserID, time.Now(), afterTime, limit}
	} else {
		// Without limit: simple query with IN clause
		query = fmt.Sprintf(`
			SELECT session_id, event
			FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND session_id = ANY($3::varchar[])
			AND (expires_at IS NULL OR expires_at > $4)
			AND created_at > $5
			AND deleted_at IS NULL
			ORDER BY session_id, created_at DESC`, s.tableSessionEvents)
		args = []any{sessionKeys[0].AppName, sessionKeys[0].UserID, sessionIDs, time.Now(), afterTime}
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
	summaryQuery := fmt.Sprintf(`SELECT session_id, filter_key, summary FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = ANY($3)
		AND (expires_at IS NULL OR expires_at > $4)
		AND deleted_at IS NULL`, s.tableSessionSummaries)

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
	}, summaryQuery, sessionKeys[0].AppName, sessionKeys[0].UserID, sessionIDs, time.Now())

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
	s.cleanupExpiredData(ctx, nil)
}

// cleanupExpiredForUser removes or soft-deletes expired sessions for a specific user.
func (s *Service) cleanupExpiredForUser(ctx context.Context, userKey session.UserKey) {
	s.cleanupExpiredData(ctx, &userKey)
}

// cleanupExpiredData is the unified cleanup function that handles both global and user-scoped cleanup.
// If userKey is nil, it cleans up all expired data globally.
// If userKey is provided, it only cleans up expired data for that specific user.
func (s *Service) cleanupExpiredData(ctx context.Context, userKey *session.UserKey) {
	now := time.Now()

	// Define cleanup tasks: table name, TTL, and whether it's session-scoped
	type cleanupTask struct {
		tableName      string
		ttl            time.Duration
		isSessionScope bool // true for session-related tables, false for app/user states
	}

	tasks := []cleanupTask{
		{s.tableSessionStates, s.sessionTTL, true},
		{s.tableSessionEvents, s.sessionTTL, true},
		{s.tableSessionSummaries, s.sessionTTL, true},
		{s.tableAppStates, s.appStateTTL, false},
		{s.tableUserStates, s.userStateTTL, false},
	}

	for _, task := range tasks {
		// Skip if TTL is not set
		if task.ttl <= 0 {
			continue
		}

		// Skip app_states when cleaning up for a specific user (app states are global)
		if userKey != nil && task.tableName == s.tableAppStates {
			continue
		}

		if s.opts.softDelete {
			s.softDeleteExpiredTable(ctx, task.tableName, now, userKey, task.isSessionScope)
		} else {
			s.hardDeleteExpiredTable(ctx, task.tableName, now, userKey, task.isSessionScope)
		}
	}
}

// softDeleteExpiredTable soft-deletes expired records from a specific table.
func (s *Service) softDeleteExpiredTable(
	ctx context.Context,
	tableName string,
	now time.Time,
	userKey *session.UserKey,
	isSessionScope bool,
) {
	var query string
	var args []any

	if userKey != nil && isSessionScope {
		// User-scoped cleanup for session-related tables
		query = fmt.Sprintf(`UPDATE %s SET deleted_at = $1
			WHERE app_name = $2 AND user_id = $3
			AND expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`, tableName)
		args = []any{now, userKey.AppName, userKey.UserID}
	} else if userKey != nil && !isSessionScope {
		// User-scoped cleanup for user_states table
		query = fmt.Sprintf(`UPDATE %s SET deleted_at = $1
			WHERE app_name = $2 AND user_id = $3
			AND expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`, tableName)
		args = []any{now, userKey.AppName, userKey.UserID}
	} else {
		// Global cleanup
		query = fmt.Sprintf(`UPDATE %s SET deleted_at = $1
			WHERE expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`, tableName)
		args = []any{now}
	}

	_, err := s.pgClient.ExecContext(ctx, query, args...)
	if err != nil {
		log.Errorf("soft delete expired %s failed: %v", tableName, err)
	}
}

// hardDeleteExpiredTable physically removes expired records from a specific table.
func (s *Service) hardDeleteExpiredTable(
	ctx context.Context,
	tableName string,
	now time.Time,
	userKey *session.UserKey,
	isSessionScope bool,
) {
	var query string
	var args []any

	if userKey != nil && isSessionScope {
		// User-scoped cleanup for session-related tables
		query = fmt.Sprintf(`DELETE FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND expires_at IS NOT NULL AND expires_at <= $3`, tableName)
		args = []any{userKey.AppName, userKey.UserID, now}
	} else if userKey != nil && !isSessionScope {
		// User-scoped cleanup for user_states table
		query = fmt.Sprintf(`DELETE FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND expires_at IS NOT NULL AND expires_at <= $3`, tableName)
		args = []any{userKey.AppName, userKey.UserID, now}
	} else {
		// Global cleanup
		query = fmt.Sprintf(`DELETE FROM %s
			WHERE expires_at IS NOT NULL AND expires_at <= $1`, tableName)
		args = []any{now}
	}

	_, err := s.pgClient.ExecContext(ctx, query, args...)
	if err != nil {
		log.Errorf("hard delete expired %s failed: %v", tableName, err)
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
