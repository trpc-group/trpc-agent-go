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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

var _ session.Service = (*Service)(nil)
var _ session.TrackService = (*Service)(nil)

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
	eventPairChans  []chan *sessionEventPair     // channel for session events to persistence
	trackEventChans []chan *trackEventPair       // channel for track events to persistence
	asyncWorker     *isummary.AsyncSummaryWorker // async summary worker
	cleanupTicker   *time.Ticker                 // ticker for automatic cleanup
	cleanupDone     chan struct{}                // signal to stop cleanup routine
	cleanupOnce     sync.Once                    // ensure cleanup routine is stopped only once
	persistWg       sync.WaitGroup               // wait group for persist workers
	once            sync.Once                    // ensure Close is called only once

	// Table names with prefix applied
	tableSessionStates    string
	tableSessionEvents    string
	tableSessionTracks    string
	tableSessionSummaries string
	tableAppStates        string
	tableUserStates       string
}

type sessionEventPair struct {
	key   session.Key
	event *event.Event
}

type trackEventPair struct {
	key   session.Key
	event *session.TrackEvent
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
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	// Set default cleanup interval if any TTL is configured and auto cleanup is not disabled
	if opts.cleanupInterval <= 0 {
		if opts.sessionTTL > 0 || opts.appStateTTL > 0 || opts.userStateTTL > 0 {
			opts.cleanupInterval = defaultCleanupIntervalSecond
		}
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithExtraOptions(opts.extraOptions...),
	}
	// Priority: DSN > direct connection settings > instance name
	if opts.dsn != "" {
		// Use DSN directly if provided.
		builderOpts = append(builderOpts, storage.WithClientConnString(opts.dsn))
	} else if opts.host != "" {
		// Use direct connection settings if provided.
		builderOpts = append(builderOpts, storage.WithClientConnString(buildConnString(opts)))
	} else if opts.instanceName != "" {
		// Otherwise, use instance name if provided.
		var ok bool
		if builderOpts, ok = storage.GetPostgresInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("postgres instance %s not found", opts.instanceName)
		}
	} else {
		// Fallback to default connection string.
		builderOpts = append(builderOpts, storage.WithClientConnString(buildConnString(opts)))
	}
	pgClient, err := storage.GetClientBuilder()(context.Background(), builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create postgres client failed: %w", err)
	}

	s := &Service{
		opts:        opts,
		pgClient:    pgClient,
		cleanupDone: make(chan struct{}),

		// Initialize table names with schema and prefix using internal/session/sqldb
		tableSessionStates:    sqldb.BuildTableNameWithSchema(opts.schema, opts.tablePrefix, sqldb.TableNameSessionStates),
		tableSessionEvents:    sqldb.BuildTableNameWithSchema(opts.schema, opts.tablePrefix, sqldb.TableNameSessionEvents),
		tableSessionTracks:    sqldb.BuildTableNameWithSchema(opts.schema, opts.tablePrefix, sqldb.TableNameSessionTrackEvents),
		tableSessionSummaries: sqldb.BuildTableNameWithSchema(opts.schema, opts.tablePrefix, sqldb.TableNameSessionSummaries),
		tableAppStates:        sqldb.BuildTableNameWithSchema(opts.schema, opts.tablePrefix, sqldb.TableNameAppStates),
		tableUserStates:       sqldb.BuildTableNameWithSchema(opts.schema, opts.tablePrefix, sqldb.TableNameUserStates),
	}

	// Initialize database schema unless skipped
	if !opts.skipDBInit {
		s.initDB(context.Background())
	}

	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}

	// Start async summary workers if summarizer is configured.
	if opts.summarizer != nil && opts.asyncSummaryNum > 0 {
		s.asyncWorker = isummary.NewAsyncSummaryWorker(isummary.AsyncSummaryConfig{
			Summarizer:        opts.summarizer,
			AsyncSummaryNum:   opts.asyncSummaryNum,
			SummaryQueueSize:  opts.summaryQueueSize,
			SummaryJobTimeout: opts.summaryJobTimeout,
			CreateSummaryFunc: s.CreateSessionSummary,
		})
		s.asyncWorker.Start()
	}

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
		if v == nil {
			sessState.State[k] = nil
			continue
		}
		copiedValue := make([]byte, len(v))
		copy(copiedValue, v)
		sessState.State[k] = copiedValue
	}

	sessBytes, err := json.Marshal(sessState)
	if err != nil {
		return nil, fmt.Errorf("marshal session failed: %w", err)
	}

	// Calculate expires_at based on TTL
	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := now.Add(s.opts.sessionTTL)
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
		log.InfofContext(
			ctx,
			"found expired session (app=%s,. user=%s, session=%s), "+
				"triggering cleanup",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		s.cleanupExpiredForUser(
			ctx,
			session.UserKey{
				AppName: key.AppName,
				UserID:  key.UserID,
			},
		)
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

	sess := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(sessState.State),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)

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
	hctx := &session.GetSessionContext{
		Context: ctx,
		Key:     key,
		Options: opt,
	}
	final := func(
		c *session.GetSessionContext,
		next func() (*session.Session, error),
	) (*session.Session, error) {
		sess, err := s.getSession(
			c.Context,
			c.Key,
			c.Options.EventNum,
			c.Options.EventTime,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"postgres session service get session state "+
					"failed: %w",
				err,
			)
		}

		// Refresh session TTL if configured and session exists.
		if sess != nil && s.opts.sessionTTL > 0 {
			if err := s.refreshSessionTTL(c.Context, c.Key); err != nil {
				log.WarnfContext(
					c.Context,
					"failed to refresh session TTL: %v",
					err,
				)
				// Do not fail GetSession; just log a warning.
			}
		}
		return sess, nil
	}
	return hook.RunGetSessionHooks(s.opts.getSessionHooks, hctx, final)
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
	if s.opts.appStateTTL > 0 {
		t := now.Add(s.opts.appStateTTL)
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
	if s.opts.userStateTTL > 0 {
		t := now.Add(s.opts.userStateTTL)
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

// UpdateSessionState updates the session-level state directly without appending an event.
// This is useful for state initialization, correction, or synchronization scenarios
// where event history is not needed.
// Keys with app: or user: prefixes are not allowed (use UpdateAppState/UpdateUserState instead).
// Keys with temp: prefix are allowed as they represent session-scoped ephemeral state.
func (s *Service) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	// Validate: disallow app: and user: prefixes
	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf("postgres session service update session state failed: %s is not allowed, use UpdateAppState instead", k)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf("postgres session service update session state failed: %s is not allowed, use UpdateUserState instead", k)
		}
	}

	// Get current session state
	var currentStateBytes []byte
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&currentStateBytes)
		}
		return sql.ErrNoRows
	}, fmt.Sprintf(`SELECT state FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3 AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err == sql.ErrNoRows {
		return fmt.Errorf("postgres session service update session state failed: session not found")
	}
	if err != nil {
		return fmt.Errorf("postgres session service update session state failed: get session state: %w", err)
	}

	var sessState SessionState
	if len(currentStateBytes) > 0 {
		if err := json.Unmarshal(currentStateBytes, &sessState); err != nil {
			return fmt.Errorf("postgres session service update session state failed: unmarshal state: %w", err)
		}
	}
	now := time.Now()
	if sessState.State == nil {
		sessState.State = make(session.StateMap)
	}
	for k, v := range state {
		if v == nil {
			sessState.State[k] = nil
			continue
		}
		copiedValue := make([]byte, len(v))
		copy(copiedValue, v)
		sessState.State[k] = copiedValue
	}
	sessState.UpdatedAt = now

	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("postgres session service update session state failed: marshal state: %w", err)
	}

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := now.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	_, err = s.pgClient.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET state = $1, updated_at = $2, expires_at = $3
		 WHERE app_name = $4 AND user_id = $5 AND session_id = $6 AND deleted_at IS NULL`, s.tableSessionStates),
		updatedStateBytes, now, expiresAt,
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("postgres session service update session state failed: %w", err)
	}

	return nil
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
	e *event.Event,
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

	hctx := &session.AppendEventContext{
		Context: ctx,
		Session: sess,
		Event:   e,
		Key:     key,
	}
	final := func(c *session.AppendEventContext, next func() error) error {
		return s.appendEventInternal(c.Context, c.Session, c.Event, c.Key, opts...)
	}
	return hook.RunAppendEventHooks(s.opts.appendEventHooks, hctx, final)
}

// appendEventInternal is the internal implementation of AppendEvent.
func (s *Service) appendEventInternal(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	key session.Key,
	opts ...session.Option,
) error {
	// update user session with the given event
	sess.UpdateUserSession(e, opts...)

	// persist event to postgres asynchronously
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok &&
					err.Error() == "send on closed channel" {
					log.ErrorfContext(
						ctx,
						"postgres session service append event "+
							"failed: %v",
						r,
					)
					return
				}
				panic(r)
			}
		}()

		index := sess.Hash % len(s.eventPairChans)
		select {
		case s.eventPairChans[index] <- &sessionEventPair{key: key, event: e}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	if err := s.addEvent(ctx, key, e); err != nil {
		return fmt.Errorf("postgres session service append event failed: %w", err)
	}

	return nil
}

// AppendTrackEvent appends a protocol-specific track event to a session.
func (s *Service) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
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
	// Update user session with the given track event.
	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return err
	}

	// Persist track event to postgres asynchronously.
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok &&
					err.Error() == "send on closed channel" {
					log.ErrorfContext(
						ctx,
						"postgres session service append track "+
							"event failed: %v",
						err,
					)
					return
				}
				panic(r)
			}
		}()

		hKey := fmt.Sprintf("%s:%s:%s:%s", key.AppName, key.UserID, key.SessionID, trackEvent.Track)
		n := len(s.trackEventChans)
		index := session.HashString(hKey) % n
		select {
		case s.trackEventChans[index] <- &trackEventPair{key: key, event: trackEvent}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}
	if err := s.addTrackEvent(ctx, key, trackEvent); err != nil {
		return fmt.Errorf("postgres session service append track event failed: %w", err)
	}
	return nil
}

// Close closes the service.
func (s *Service) Close() error {
	s.once.Do(func() {
		// Stop cleanup routine.
		s.stopCleanupRoutine()

		// Close event pair channels and wait for persist workers.
		for _, ch := range s.eventPairChans {
			close(ch)
		}
		// Close track event channels and wait for persist workers.
		for _, ch := range s.trackEventChans {
			close(ch)
		}
		s.persistWg.Wait()

		// Close summary job channels and wait for summary workers.
		if s.asyncWorker != nil {
			s.asyncWorker.Stop()
		}

		// Close postgres connection after all workers are stopped.
		if s.pgClient != nil {
			s.pgClient.Close()
		}
	})

	return nil
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

	type cleanupTask struct {
		tableName string
		ttl       time.Duration
	}

	tasks := []cleanupTask{
		{s.tableSessionStates, s.opts.sessionTTL},
		{s.tableSessionEvents, s.opts.sessionTTL},
		{s.tableSessionTracks, s.opts.sessionTTL},
		{s.tableSessionSummaries, s.opts.sessionTTL},
		{s.tableAppStates, s.opts.appStateTTL},
		{s.tableUserStates, s.opts.userStateTTL},
	}

	validTasks := []cleanupTask{}
	for _, task := range tasks {
		if task.ttl <= 0 {
			continue
		}
		if userKey != nil && task.tableName == s.tableAppStates {
			continue
		}
		validTasks = append(validTasks, task)
	}

	if len(validTasks) > 0 {
		err := s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
			for _, task := range validTasks {
				if s.opts.softDelete {
					if err := s.softDeleteExpiredTableInTx(ctx, tx, task.tableName, now, userKey); err != nil {
						return err
					}
				} else {
					if err := s.hardDeleteExpiredTableInTx(ctx, tx, task.tableName, now, userKey); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired tables failed: %v",
				err,
			)
		}
	}
}

func (s *Service) softDeleteExpiredTableInTx(
	ctx context.Context,
	tx *sql.Tx,
	tableName string,
	now time.Time,
	userKey *session.UserKey,
) error {
	if tableName == s.tableSessionEvents {
		var sessionKeys []session.Key
		var err error
		handleFunc := func(rows *sql.Rows) error {
			for rows.Next() {
				var appName, userID, sessionID string
				var updatedAt time.Time
				if err := rows.Scan(&appName, &userID, &sessionID, &updatedAt); err != nil {
					return err
				}
				if updatedAt.Before(now.Add(-s.opts.sessionTTL)) {
					sessionKeys = append(sessionKeys, session.Key{
						AppName:   appName,
						UserID:    userID,
						SessionID: sessionID,
					})
				}
			}
			return nil
		}
		if userKey != nil {
			query := fmt.Sprintf(`SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND deleted_at IS NULL GROUP BY app_name, user_id, session_id`, tableName)
			args := []any{userKey.AppName, userKey.UserID}
			err = s.pgClient.Query(ctx, handleFunc, query, args...)
		} else {
			query := fmt.Sprintf(`SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM %s
				WHERE deleted_at IS NULL GROUP BY app_name, user_id, session_id`, tableName)
			err = s.pgClient.Query(ctx, handleFunc, query)
		}
		if err != nil {
			return fmt.Errorf("soft delete expired %s: %w", tableName, err)
		}
		placeholders := make([]string, len(sessionKeys))
		args := make([]any, 0, len(sessionKeys))
		index := 2
		for i, key := range sessionKeys {
			placeholders[i] = fmt.Sprintf("($%d, $%d, $%d)", index, index+1, index+2)
			index += 3
			args = append(args, key.AppName, key.UserID, key.SessionID)
		}
		if len(args) > 0 {
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = $1 WHERE (app_name, user_id, session_id) IN (%s) AND deleted_at IS NULL`,
					tableName, strings.Join(placeholders, ",")), append([]any{now}, args...)...); err != nil {
				return fmt.Errorf("soft delete events: %w", err)
			}
		}
		return nil
	}

	var query string
	var args []any
	if userKey != nil {
		query = fmt.Sprintf(`UPDATE %s SET deleted_at = $1
			WHERE app_name = $2 AND user_id = $3
			AND expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`, tableName)
		args = []any{now, userKey.AppName, userKey.UserID}
	} else {
		query = fmt.Sprintf(`UPDATE %s SET deleted_at = $1
			WHERE expires_at IS NOT NULL AND expires_at <= $1 AND deleted_at IS NULL`, tableName)
		args = []any{now}
	}

	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("soft delete expired %s: %w", tableName, err)
	}
	return nil
}

func (s *Service) hardDeleteExpiredTableInTx(
	ctx context.Context,
	tx *sql.Tx,
	tableName string,
	now time.Time,
	userKey *session.UserKey,
) error {
	if tableName == s.tableSessionEvents {
		var sessionKeys []session.Key
		var err error
		handleFunc := func(rows *sql.Rows) error {
			for rows.Next() {
				var appName, userID, sessionID string
				var updatedAt time.Time
				if err := rows.Scan(&appName, &userID, &sessionID, &updatedAt); err != nil {
					return err
				}
				if updatedAt.Before(now.Add(-s.opts.sessionTTL)) {
					sessionKeys = append(sessionKeys, session.Key{
						AppName:   appName,
						UserID:    userID,
						SessionID: sessionID,
					})
				}
			}
			return nil
		}
		if userKey != nil {
			query := fmt.Sprintf(`SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND deleted_at IS NULL GROUP BY app_name, user_id, session_id`, tableName)
			args := []any{userKey.AppName, userKey.UserID}
			err = s.pgClient.Query(ctx, handleFunc, query, args...)
		} else {
			query := fmt.Sprintf(`SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM %s
				WHERE deleted_at IS NULL GROUP BY app_name, user_id, session_id`, tableName)
			err = s.pgClient.Query(ctx, handleFunc, query)
		}
		if err != nil {
			return fmt.Errorf("soft delete expired %s: %w", tableName, err)
		}
		placeholders := make([]string, len(sessionKeys))
		args := make([]any, 0, len(sessionKeys))
		index := 1
		for i, key := range sessionKeys {
			placeholders[i] = fmt.Sprintf("($%d, $%d, $%d)", index, index+1, index+2)
			index += 3
			args = append(args, key.AppName, key.UserID, key.SessionID)
		}
		if len(args) > 0 {
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s WHERE (app_name, user_id, session_id) IN (%s) AND deleted_at IS NULL`,
					tableName, strings.Join(placeholders, ",")), args...); err != nil {
				return fmt.Errorf("hard delete events: %w", err)
			}
		}
		return nil
	}

	var query string
	var args []any
	if userKey != nil {
		query = fmt.Sprintf(`DELETE FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND expires_at IS NOT NULL AND expires_at <= $3`, tableName)
		args = []any{userKey.AppName, userKey.UserID, now}
	} else {
		query = fmt.Sprintf(`DELETE FROM %s
			WHERE expires_at IS NOT NULL AND expires_at <= $1`, tableName)
		args = []any{now}
	}

	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("hard delete expired %s: %w", tableName, err)
	}
	return nil
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
