//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mysql provides the MySQL session service.
package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
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

// Service is the MySQL session service.
type Service struct {
	opts            ServiceOpts
	mysqlClient     storage.Client
	eventPairChans  []chan *sessionEventPair     // channel for session events to persistence
	trackEventChans []chan *trackEventPair       // channel for track events to persistence
	asyncWorker     *isummary.AsyncSummaryWorker // async summary worker
	cleanupTicker   *time.Ticker                 // ticker for automatic cleanup
	cleanupDone     chan struct{}                // signal to stop cleanup routine
	cleanupOnce     sync.Once                    // ensure cleanup routine is stopped only once
	persistWg       sync.WaitGroup               // wait group for persist workers
	once            sync.Once

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

// NewService creates a new MySQL session service.
// It requires either a DSN (WithMySQLClientDSN) or an instance name (WithMySQLInstance).
func NewService(options ...ServiceOpt) (*Service, error) {
	// Apply default options
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	// Create MySQL client
	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderDSN(opts.dsn),
		storage.WithExtraOptions(opts.extraOptions...),
	}
	if opts.dsn == "" && opts.instanceName != "" {
		// Method 2: Use pre-registered MySQL instance
		var ok bool
		if builderOpts, ok = storage.GetMySQLInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("mysql instance %s not found", opts.instanceName)
		}
	}

	mysqlClient, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create mysql client failed: %w", err)
	}

	// Build table names with prefix
	tableSessionStates := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameSessionStates)
	tableSessionEvents := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameSessionEvents)
	tableSessionTracks := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameSessionTrackEvents)
	tableSessionSummaries := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameSessionSummaries)
	tableAppStates := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameAppStates)
	tableUserStates := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameUserStates)

	// Create service
	s := &Service{
		opts:                  opts,
		mysqlClient:           mysqlClient,
		tableSessionStates:    tableSessionStates,
		tableSessionEvents:    tableSessionEvents,
		tableSessionTracks:    tableSessionTracks,
		tableSessionSummaries: tableSessionSummaries,
		tableAppStates:        tableAppStates,
		tableUserStates:       tableUserStates,
	}

	// Initialize database if needed
	if !opts.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}

	// Start async persistence workers if enabled
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

	// Start cleanup routine if any TTL is configured
	if opts.sessionTTL > 0 || opts.appStateTTL > 0 || opts.userStateTTL > 0 {
		s.startCleanupRoutine()
	}

	return s, nil
}

// Close closes the service and releases resources.
func (s *Service) Close() error {
	// Stop cleanup routine
	s.stopCleanupRoutine()

	// Close async persist workers
	if s.eventPairChans != nil {
		for _, ch := range s.eventPairChans {
			close(ch)
		}
	}
	if s.trackEventChans != nil {
		for _, ch := range s.trackEventChans {
			close(ch)
		}
	}
	s.persistWg.Wait()

	// Close async summary workers and wait for them to finish
	if s.asyncWorker != nil {
		s.asyncWorker.Stop()
	}

	// Close MySQL client
	if s.mysqlClient != nil {
		return s.mysqlClient.Close()
	}

	return nil
}

// calculateExpiresAt calculates the expiration timestamp based on TTL.
// Returns nil if TTL is 0 (no expiration).
func calculateExpiresAt(ttl time.Duration) *time.Time {
	if ttl <= 0 {
		return nil
	}
	expiresAt := time.Now().Add(ttl)
	return &expiresAt
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
	expiresAt := calculateExpiresAt(s.opts.sessionTTL)

	// Check if session already exists (matching PostgreSQL behavior)
	var sessionExists bool
	var existingExpiresAt sql.NullTime
	err = s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop
		sessionExists = true
		if err := rows.Scan(&existingExpiresAt); err != nil {
			return err
		}
		return nil
	}, fmt.Sprintf(
		`SELECT expires_at FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
		s.tableSessionStates,
	), key.AppName, key.UserID, key.SessionID)
	if err != nil {
		return nil, fmt.Errorf("check existing session failed: %w", err)
	}

	if sessionExists {
		// If session exists and has not expired, reject creation
		if !existingExpiresAt.Valid || existingExpiresAt.Time.After(now) {
			log.ErrorfContext(
				ctx,
				"CreateSession: session already exists and not expired (app=%s, user=%s, session=%s, expires=%v)",
				key.AppName,
				key.UserID,
				key.SessionID,
				existingExpiresAt,
			)
			return nil, fmt.Errorf("session already exists and has not expired")
		}
		// Session exists but has expired, will be overwritten below
		log.DebugfContext(
			ctx,
			"found expired session (app=%s, user=%s, session=%s), overwriting",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
	}

	log.DebugfContext(
		ctx,
		"CreateSession: inserting new session (app=%s, user=%s, "+
			"session=%s)",
		key.AppName,
		key.UserID,
		key.SessionID,
	)

	// Insert or update session state
	// If expired session exists, overwrite it; events/summaries will be filtered by created_at when reading
	if sessionExists {
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf(
				`UPDATE %s SET state = ?, created_at = ?, updated_at = ?, expires_at = ?, deleted_at = NULL
				WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
				s.tableSessionStates,
			),
			sessBytes, sessState.CreatedAt, sessState.UpdatedAt, expiresAt,
			key.AppName, key.UserID, key.SessionID,
		)
	} else {
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf(
				`INSERT INTO %s (app_name, user_id, session_id, state, created_at, updated_at, expires_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				s.tableSessionStates,
			),
			key.AppName, key.UserID, key.SessionID, sessBytes,
			sessState.CreatedAt, sessState.UpdatedAt, expiresAt,
		)
	}
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
		key.AppName, key.UserID, sessState.ID,
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
				"mysql session service get session state failed: %w",
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
		return nil, fmt.Errorf("mysql session service get session list failed: %w", err)
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
		return fmt.Errorf("mysql session service delete session state failed: %w", err)
	}
	return nil
}

// UpdateAppState updates the state by target scope and key.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	now := time.Now()
	expiresAt := calculateExpiresAt(s.opts.appStateTTL)

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		if err := s.upsertAppState(ctx, appName, k, v, now, expiresAt); err != nil {
			return fmt.Errorf("mysql session service update app state failed: %w", err)
		}
	}
	return nil
}

// upsertAppState inserts or updates an app state record.
// It first checks if an active record exists, then updates or inserts accordingly.
func (s *Service) upsertAppState(ctx context.Context, appName, key string, value []byte, now time.Time, expiresAt *time.Time) error {
	// Check if active record exists
	var id int64
	err := s.mysqlClient.QueryRow(ctx, []any{&id},
		fmt.Sprintf("SELECT id FROM %s WHERE app_name = ? AND `key` = ? AND deleted_at IS NULL LIMIT 1", s.tableAppStates),
		appName, key)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if errors.Is(err, sql.ErrNoRows) {
		// Insert new record
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s (app_name, `key`, value, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)", s.tableAppStates),
			appName, key, value, now, now, expiresAt)
	} else {
		// Update existing record
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("UPDATE %s SET value = ?, updated_at = ?, expires_at = ? WHERE id = ?", s.tableAppStates),
			value, now, expiresAt, id)
	}
	return err
}

// ListAppStates gets the app states.
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}

	appStateMap := make(session.StateMap)
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		appStateMap[key] = value
		return nil
	}, fmt.Sprintf("SELECT `key`, value FROM %s WHERE app_name = ? AND (expires_at IS NULL OR expires_at > ?) AND deleted_at IS NULL", s.tableAppStates),
		appName, time.Now())

	if err != nil {
		return nil, fmt.Errorf("mysql session service list app states failed: %w", err)
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
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("UPDATE %s SET deleted_at = ? WHERE app_name = ? AND `key` = ? AND deleted_at IS NULL", s.tableAppStates),
			time.Now(), appName, key)
	} else {
		// Hard delete: permanently remove record
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND `key` = ?", s.tableAppStates),
			appName, key)
	}

	if err != nil {
		return fmt.Errorf("mysql session service delete app state failed: %w", err)
	}
	return nil
}

// UpdateUserState updates the state by target scope and key.
func (s *Service) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	now := time.Now()
	expiresAt := calculateExpiresAt(s.opts.userStateTTL)

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		if err := s.upsertUserState(ctx, userKey.AppName, userKey.UserID, k, v, now, expiresAt); err != nil {
			return fmt.Errorf("mysql session service update user state failed: %w", err)
		}
	}
	return nil
}

// upsertUserState inserts or updates a user state record.
// It first checks if an active record exists, then updates or inserts accordingly.
func (s *Service) upsertUserState(ctx context.Context, appName, userID, key string, value []byte, now time.Time, expiresAt *time.Time) error {
	// Check if active record exists
	var id int64
	err := s.mysqlClient.QueryRow(ctx, []any{&id},
		fmt.Sprintf("SELECT id FROM %s WHERE app_name = ? AND user_id = ? AND `key` = ? AND deleted_at IS NULL LIMIT 1", s.tableUserStates),
		appName, userID, key)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if errors.Is(err, sql.ErrNoRows) {
		// Insert new record
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s (app_name, user_id, `key`, value, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)", s.tableUserStates),
			appName, userID, key, value, now, now, expiresAt)
	} else {
		// Update existing record
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("UPDATE %s SET value = ?, updated_at = ?, expires_at = ? WHERE id = ?", s.tableUserStates),
			value, now, expiresAt, id)
	}
	return err
}

// ListUserStates lists the state by target scope and key.
func (s *Service) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	userStateMap := make(session.StateMap)
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		userStateMap[key] = value
		return nil
	}, fmt.Sprintf("SELECT `key`, value FROM %s WHERE app_name = ? AND user_id = ? AND (expires_at IS NULL OR expires_at > ?) AND deleted_at IS NULL", s.tableUserStates),
		userKey.AppName, userKey.UserID, time.Now())

	if err != nil {
		return nil, fmt.Errorf("mysql session service list user states failed: %w", err)
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
			return fmt.Errorf("mysql session service update session state failed: %s is not allowed, use UpdateAppState instead", k)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf("mysql session service update session state failed: %s is not allowed, use UpdateUserState instead", k)
		}
	}

	// Get current session state
	var currentStateBytes []byte
	err := s.mysqlClient.QueryRow(ctx,
		[]any{&currentStateBytes},
		fmt.Sprintf("SELECT state FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL", s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("mysql session service update session state failed: session not found")
		}
		return fmt.Errorf("mysql session service update session state failed: %w", err)
	}

	var sessState SessionState
	if len(currentStateBytes) > 0 {
		if err := json.Unmarshal(currentStateBytes, &sessState); err != nil {
			return fmt.Errorf("mysql session service update session state failed: unmarshal state: %w", err)
		}
	}
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
	now := time.Now()
	sessState.UpdatedAt = now

	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("mysql session service update session state failed: marshal state: %w", err)
	}

	expiresAt := calculateExpiresAt(s.opts.sessionTTL)

	_, err = s.mysqlClient.Exec(ctx,
		fmt.Sprintf(`UPDATE %s SET state = ?, updated_at = ?, expires_at = ?
		 WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
		updatedStateBytes, now, expiresAt,
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("mysql session service update session state failed: %w", err)
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
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("UPDATE %s SET deleted_at = ? WHERE app_name = ? AND user_id = ? AND `key` = ? AND deleted_at IS NULL", s.tableUserStates),
			time.Now(), userKey.AppName, userKey.UserID, key)
	} else {
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND user_id = ? AND `key` = ?", s.tableUserStates),
			userKey.AppName, userKey.UserID, key)
	}
	if err != nil {
		return fmt.Errorf("mysql session service delete user state failed: %w", err)
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

	// persist event to MySQL asynchronously
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok &&
					err.Error() == "send on closed channel" {
					log.ErrorfContext(
						ctx,
						"mysql session service append event "+
							"failed: %v",
						r,
					)
					return
				}
				panic(r)
			}
		}()

		// Hash key to determine which worker channel to use
		index := sess.Hash % len(s.eventPairChans)
		select {
		case s.eventPairChans[index] <- &sessionEventPair{key: key, event: e}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	if err := s.addEvent(ctx, key, e); err != nil {
		return fmt.Errorf("mysql session service append event failed: %w", err)
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
	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("mysql session service append track event failed: %w", err)
	}

	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && err.Error() == "send on closed channel" {
					log.Errorf("mysql session service append track event failed: %v", r)
					return
				}
				panic(r)
			}
		}()

		index := sess.Hash % len(s.trackEventChans)
		select {
		case s.trackEventChans[index] <- &trackEventPair{key: key, event: trackEvent}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	if err := s.addTrackEvent(ctx, key, trackEvent); err != nil {
		return fmt.Errorf("mysql session service append track event failed: %w", err)
	}
	return nil
}

// startAsyncPersistWorker starts worker goroutines for async event persistence.
func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	// init event pair chan and track pair chan.
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	s.trackEventChans = make([]chan *trackEventPair, persisterNum)
	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(chan *sessionEventPair, defaultChanBufferSize)
		s.trackEventChans[i] = make(chan *trackEventPair, defaultChanBufferSize)
	}

	s.persistWg.Add(persisterNum * 2)
	for _, eventPairChan := range s.eventPairChans {
		go func(eventPairChan chan *sessionEventPair) {
			defer s.persistWg.Done()
			for eventPair := range eventPairChan {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				log.DebugfContext(
					ctx,
					"Session persistence queue monitoring: channel "+
						"capacity: %d, current length: %d, "+
						"(app=%s, user=%s, session=%s)",
					cap(eventPairChan),
					len(eventPairChan),
					eventPair.key.AppName,
					eventPair.key.UserID,
					eventPair.key.SessionID,
				)
				if err := s.addEvent(ctx, eventPair.key, eventPair.event); err != nil {
					log.ErrorfContext(
						ctx,
						"async persist event failed: %w",
						err,
					)
				}
				cancel()
			}
		}(eventPairChan)
	}

	for _, trackPairChan := range s.trackEventChans {
		go func(trackPairChan chan *trackEventPair) {
			defer s.persistWg.Done()
			for trackEventPair := range trackPairChan {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				log.DebugfContext(
					ctx,
					"Session persistence queue monitoring: channel "+
						"capacity: %d, current length: %d, "+
						"(app=%s, user=%s, session=%s)",
					cap(trackPairChan),
					len(trackPairChan),
					trackEventPair.key.AppName,
					trackEventPair.key.UserID,
					trackEventPair.key.SessionID,
				)
				if err := s.addTrackEvent(ctx, trackEventPair.key, trackEventPair.event); err != nil {
					log.ErrorfContext(
						ctx,
						"async persist event failed: %w",
						err,
					)
				}
				cancel()
			}
		}(trackPairChan)
	}
}

// startCleanupRoutine starts a background routine to periodically clean up
// expired data.
func (s *Service) startCleanupRoutine() {
	interval := s.opts.cleanupInterval
	if interval <= 0 {
		interval = defaultCleanupIntervalSecond
	}

	s.cleanupTicker = time.NewTicker(interval)
	s.cleanupDone = make(chan struct{})

	go func() {
		log.InfofContext(
			context.Background(),
			"started cleanup routine for mysql session service "+
				"(interval: %v)",
			interval,
		)
		for {
			select {
			case <-s.cleanupTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				s.cleanupExpiredData(ctx)
				cancel()
			case <-s.cleanupDone:
				log.InfoContext(
					context.Background(),
					"cleanup routine stopped for mysql session service",
				)
				return
			}
		}
	}()
}

// stopCleanupRoutine stops the cleanup routine.
func (s *Service) stopCleanupRoutine() {
	s.cleanupOnce.Do(func() {
		if s.cleanupTicker != nil {
			s.cleanupTicker.Stop()
		}
		if s.cleanupDone != nil {
			close(s.cleanupDone)
		}
	})
}

// cleanupExpiredData cleans up expired session states, events, summaries, and app/user states.
func (s *Service) cleanupExpiredData(ctx context.Context) {
	now := time.Now()

	// Clean up expired sessions
	if s.opts.sessionTTL > 0 {
		s.cleanupExpiredSessions(ctx, now)
	}

	// Clean up expired app states
	if s.opts.appStateTTL > 0 {
		s.cleanupExpiredAppStates(ctx, now)
	}

	// Clean up expired user states
	if s.opts.userStateTTL > 0 {
		s.cleanupExpiredUserStates(ctx, now)
	}
}

// cleanupExpiredSessions cleans up expired session states, events, and summaries.
func (s *Service) cleanupExpiredSessions(ctx context.Context, now time.Time) {
	var deletedCount int64

	// Delete expired sessions and related data in a transaction.
	// We directly use SELECT ... FOR UPDATE with LIMIT to find and lock expired sessions.
	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		// 1. Find and lock expired sessions
		// Use LIMIT to avoid locking too many rows in one transaction.
		query := fmt.Sprintf(`SELECT app_name, user_id, session_id FROM %s
			WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL
			LIMIT 1000 FOR UPDATE`,
			s.tableSessionStates)

		var sessionKeys []session.Key
		rows, err := tx.QueryContext(ctx, query, now)
		if err != nil {
			return fmt.Errorf("fetch expired sessions failed: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var app, user, sess string
			if err := rows.Scan(&app, &user, &sess); err != nil {
				continue
			}
			sessionKeys = append(sessionKeys, session.Key{
				AppName:   app,
				UserID:    user,
				SessionID: sess,
			})
		}

		if len(sessionKeys) == 0 {
			return nil
		}

		// 2. Delete the locked sessions
		if err := s.deleteSessions(ctx, tx, sessionKeys, now); err != nil {
			return err
		}

		// We count the number of sessions deleted, not the total rows affected across all tables
		deletedCount = int64(len(sessionKeys))
		return nil
	})

	if err != nil {
		log.ErrorfContext(ctx, "cleanup expired sessions failed: %v", err)
		return
	}

	if deletedCount > 0 {
		log.InfofContext(ctx, "cleaned up %d expired sessions", deletedCount)
	}
}

// deleteSessions deletes session data for the given keys within a transaction.
func (s *Service) deleteSessions(ctx context.Context, tx *sql.Tx, keys []session.Key, now time.Time) error {
	// Prepare delete args for verified keys
	placeholders := make([]string, len(keys))
	args := make([]any, 0, len(keys)*3)
	for i, key := range keys {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}
	whereClause := fmt.Sprintf(`(app_name, user_id, session_id) IN (%s) AND deleted_at IS NULL`, strings.Join(placeholders, ","))

	if s.opts.softDelete {
		return s.softDeleteSessions(ctx, tx, whereClause, args, now)
	}
	return s.hardDeleteSessions(ctx, tx, whereClause, args)
}

// softDeleteSessions performs soft delete on session tables.
func (s *Service) softDeleteSessions(ctx context.Context, tx *sql.Tx, whereClause string, args []any, now time.Time) error {
	// Soft delete session states
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE %s`, s.tableSessionStates, whereClause),
		append([]any{now}, args...)...)
	if err != nil {
		return fmt.Errorf("soft delete sessions: %w", err)
	}

	// Soft delete summaries
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE %s`, s.tableSessionSummaries, whereClause),
		append([]any{now}, args...)...); err != nil {
		return fmt.Errorf("soft delete summaries: %w", err)
	}

	// Soft delete events
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE %s`, s.tableSessionEvents, whereClause),
		append([]any{now}, args...)...); err != nil {
		return fmt.Errorf("soft delete events: %w", err)
	}

	// Soft delete track events
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE %s`, s.tableSessionTracks, whereClause),
		append([]any{now}, args...)...); err != nil {
		return fmt.Errorf("soft delete track events: %w", err)
	}

	return nil
}

// hardDeleteSessions performs hard delete on session tables.
func (s *Service) hardDeleteSessions(ctx context.Context, tx *sql.Tx, whereClause string, args []any) error {
	// Hard delete session states
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tableSessionStates, whereClause),
		args...)
	if err != nil {
		return fmt.Errorf("hard delete sessions: %w", err)
	}

	// Hard delete summaries
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tableSessionSummaries, whereClause),
		args...); err != nil {
		return fmt.Errorf("hard delete summaries: %w", err)
	}

	// Hard delete events
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tableSessionEvents, whereClause),
		args...); err != nil {
		return fmt.Errorf("hard delete events: %w", err)
	}

	// Hard delete track events
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tableSessionTracks, whereClause),
		args...); err != nil {
		return fmt.Errorf("hard delete track events: %w", err)
	}

	return nil
}

// cleanupExpiredAppStates cleans up expired app states.
func (s *Service) cleanupExpiredAppStates(ctx context.Context, now time.Time) {
	var deletedCount int64

	if s.opts.softDelete {
		result, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`, s.tableAppStates),
			now, now)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired app states failed: %v",
				err,
			)
			return
		}
		deletedCount, _ = result.RowsAffected()
	} else {
		result, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at <= ?`, s.tableAppStates),
			now)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired app states failed: %v",
				err,
			)
			return
		}
		deletedCount, _ = result.RowsAffected()
	}

	if deletedCount > 0 {
		log.InfofContext(
			ctx,
			"cleaned up %d expired app states",
			deletedCount,
		)
	}
}

// cleanupExpiredUserStates cleans up expired user states.
func (s *Service) cleanupExpiredUserStates(ctx context.Context, now time.Time) {
	var deletedCount int64

	if s.opts.softDelete {
		result, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`, s.tableUserStates),
			now, now)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired user states failed: %v",
				err,
			)
			return
		}
		deletedCount, _ = result.RowsAffected()
	} else {
		result, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at <= ?`, s.tableUserStates),
			now)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired user states failed: %v",
				err,
			)
			return
		}
		deletedCount, _ = result.RowsAffected()
	}

	if deletedCount > 0 {
		log.InfofContext(
			ctx,
			"cleaned up %d expired user states",
			deletedCount,
		)
	}
}

// applyOptions is a convenience wrapper to internal/session.ApplyOptions.
func applyOptions(opts ...session.Option) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}

// mergeState is a convenience wrapper to internal/session.MergeState.
func mergeState(appState, userState session.StateMap, sess *session.Session) *session.Session {
	if sess == nil {
		return nil
	}
	// Merge with priority: session state > user state > app state
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
	}
	return sess
}
