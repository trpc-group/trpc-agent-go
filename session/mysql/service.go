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
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

var _ session.Service = (*Service)(nil)

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
	eventPairChans  []chan *sessionEventPair // channel for session events to persistence
	summaryJobChans []chan *summaryJob       // channel for summary jobs to processing
	cleanupTicker   *time.Ticker             // ticker for automatic cleanup
	cleanupDone     chan struct{}            // signal to stop cleanup routine
	cleanupOnce     sync.Once                // ensure cleanup routine is stopped only once
	summaryWg       sync.WaitGroup           // wait group for summary workers
	persistWg       sync.WaitGroup           // wait group for persist workers
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
	ctx       context.Context // Detached context preserving values but not cancel.
	filterKey string
	force     bool
	session   *session.Session
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
	tableSessionSummaries := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameSessionSummaries)
	tableAppStates := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameAppStates)
	tableUserStates := sqldb.BuildTableName(opts.tablePrefix, sqldb.TableNameUserStates)

	// Create service
	s := &Service{
		opts:                  opts,
		mysqlClient:           mysqlClient,
		tableSessionStates:    tableSessionStates,
		tableSessionEvents:    tableSessionEvents,
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

	// Start async summary workers if summarizer is configured
	if opts.summarizer != nil {
		s.startAsyncSummaryWorker()
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
	s.persistWg.Wait()

	// Close async summary workers and wait for them to finish
	if s.summaryJobChans != nil {
		for _, ch := range s.summaryJobChans {
			close(ch)
		}
		s.summaryWg.Wait()
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
		sessState.State[k] = v
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
	}, fmt.Sprintf(`SELECT expires_at FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)
	if err != nil {
		return nil, fmt.Errorf("check existing session failed: %w", err)
	}

	if sessionExists {
		// If session exists and has not expired, reject creation
		if !existingExpiresAt.Valid {
			log.ErrorfContext(
				ctx,
				"CreateSession: session already exists with no "+
					"expiration (app=%s, user=%s, session=%s)",
				key.AppName,
				key.UserID,
				key.SessionID,
			)
			return nil, fmt.Errorf("session already exists and has not expired")
		}
		if existingExpiresAt.Time.After(now) {
			log.ErrorfContext(
				ctx,
				"CreateSession: session already exists and not "+
					"expired yet (app=%s, user=%s, session=%s, "+
					"expires=%v)",
				key.AppName,
				key.UserID,
				key.SessionID,
				existingExpiresAt.Time,
			)
			return nil, fmt.Errorf("session already exists and has not expired")
		}
		// If session exists but has expired, trigger cleanup before creating new one
		log.DebugfContext(
			ctx,
			"found expired session (app=%s, user=%s, session=%s), "+
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

	log.DebugfContext(
		ctx,
		"CreateSession: inserting new session (app=%s, user=%s, "+
			"session=%s)",
		key.AppName,
		key.UserID,
		key.SessionID,
	)

	// Insert session state (MySQL syntax)
	_, err = s.mysqlClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, state, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, s.tableSessionStates),
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
		_, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf("REPLACE INTO %s (app_name, `key`, value, updated_at, expires_at, deleted_at) VALUES (?, ?, ?, ?, ?, NULL)", s.tableAppStates),
			appName, k, v, now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf("mysql session service update app state failed: %w", err)
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
		_, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf("REPLACE INTO %s (app_name, user_id, `key`, value, updated_at, expires_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, NULL)", s.tableUserStates),
			userKey.AppName, userKey.UserID, k, v, now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf("mysql session service update user state failed: %w", err)
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
		sessState.State[k] = v
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

// startAsyncPersistWorker starts worker goroutines for async event persistence.
func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	// init event pair chan
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(chan *sessionEventPair, defaultChanBufferSize)
	}

	s.persistWg.Add(persisterNum)
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
	var sessionKeys []session.Key
	query := fmt.Sprintf(`SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM %s
				WHERE deleted_at IS NULL GROUP BY app_name, user_id, session_id`,
		s.tableSessionEvents)
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop
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
		return nil
	}, query)
	if err != nil {
		log.WarnfContext(
			ctx,
			"fetch events failed: %w",
			err,
		)
		return
	}
	placeholders := make([]string, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys))
	for i, key := range sessionKeys {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}

	if s.opts.softDelete {
		// Use transaction to ensure atomicity
		err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
			// Soft delete expired sessions
			result, err := tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`, s.tableSessionStates),
				now, now)
			if err != nil {
				return fmt.Errorf("soft delete sessions: %w", err)
			}
			deletedCount, _ = result.RowsAffected()

			// Soft delete related events and summaries with same expiration condition
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`,
					s.tableSessionSummaries),
				now, now); err != nil {
				return fmt.Errorf("soft delete summaries: %w", err)
			}

			if len(args) > 0 {
				if _, err := tx.ExecContext(ctx,
					fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE (app_name, user_id, session_id) IN (%s) AND deleted_at IS NULL`,
						s.tableSessionEvents, strings.Join(placeholders, ",")), append([]any{now}, args...)...); err != nil {
					return fmt.Errorf("soft delete events: %w", err)
				}
			}

			return nil
		})
		if err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired sessions failed: %v",
				err,
			)
			return
		}
	} else {
		// Use transaction to ensure atomicity
		err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
			// Hard delete expired sessions, events, and summaries with same condition
			result, err := tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at <= ?`, s.tableSessionStates),
				now)
			if err != nil {
				return fmt.Errorf("hard delete sessions: %w", err)
			}
			deletedCount, _ = result.RowsAffected()

			// Hard delete events and summaries with same expiration condition
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at <= ?`,
					s.tableSessionSummaries),
				now); err != nil {
				return fmt.Errorf("hard delete summaries: %w", err)
			}
			if len(args) > 0 {
				if _, err := tx.ExecContext(ctx,
					fmt.Sprintf(`DELETE FROM %s WHERE (app_name, user_id, session_id) IN (%s) AND deleted_at IS NULL`,
						s.tableSessionEvents, strings.Join(placeholders, ",")), args...); err != nil {
					return fmt.Errorf("soft delete summaries: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired sessions failed: %v",
				err,
			)
			return
		}
	}

	if deletedCount > 0 {
		log.InfofContext(
			ctx,
			"cleaned up %d expired sessions",
			deletedCount,
		)
	}
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

// cleanupExpiredForUser cleans up all expired data for a specific user.
// This is called when creating a session finds an expired session exists.
func (s *Service) cleanupExpiredForUser(ctx context.Context, userKey session.UserKey) {
	now := time.Now()

	if s.opts.softDelete {
		// Soft delete expired sessions for this user
		if _, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE app_name = ? AND user_id = ? AND expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`, s.tableSessionStates),
			now, userKey.AppName, userKey.UserID, now); err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired sessions for user failed: %v",
				err,
			)
		}
	} else {
		// Hard delete expired sessions for this user
		if _, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE app_name = ? AND user_id = ? AND expires_at IS NOT NULL AND expires_at <= ?`, s.tableSessionStates),
			userKey.AppName, userKey.UserID, now); err != nil {
			log.ErrorfContext(
				ctx,
				"cleanup expired sessions for user failed: %v",
				err,
			)
		}
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
	if sess.State == nil {
		sess.State = make(session.StateMap)
	}

	// Merge with priority: session state > user state > app state
	for k, v := range appState {
		sess.State[session.StateAppPrefix+k] = v
	}
	for k, v := range userState {
		sess.State[session.StateUserPrefix+k] = v
	}
	return sess
}
