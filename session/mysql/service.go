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
	"math/rand"
	"strings"
	"sync"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
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

var errSessionNotFound = errors.New("session not found")

const (
	createSessionTransactionAttempts = 3
)

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
			_ = mysqlClient.Close()
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}

	// Start async persistence workers if enabled
	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}

	// Start async summary workers if summary generation is configured.
	if isummary.HasSummarizer(opts.summarizer) && opts.asyncSummaryNum > 0 {
		s.asyncWorker = isummary.NewAsyncSummaryWorker(isummary.AsyncSummaryConfig{
			Summarizer:        opts.summarizer,
			AsyncSummaryNum:   opts.asyncSummaryNum,
			SummaryQueueSize:  opts.summaryQueueSize,
			SummaryJobTimeout: opts.summaryJobTimeout,
			SummaryDispatchPolicy: isummary.NewSummaryDispatchPolicy(
				opts.summaryFilterAllowlist,
				opts.shouldCascadeFullSessionSummary(),
			),
			CreateSummaryFunc: s.CreateSessionSummary,
		})
		s.asyncWorker.Start()
	}

	// Start cleanup routine if any TTL is configured
	if opts.sessionTTL > 0 || opts.appStateTTL > 0 || opts.userStateTTL > 0 ||
		opts.effectiveTrackEventTTL() > 0 {
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

	sessState := &SessionState{
		ID:    key.SessionID,
		State: make(session.StateMap),
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

	for attempt := 0; attempt < createSessionTransactionAttempts; attempt++ {
		now := time.Now()
		sessState.CreatedAt = now
		sessState.UpdatedAt = now

		sessBytes, err := json.Marshal(sessState)
		if err != nil {
			return nil, fmt.Errorf("marshal session failed: %w", err)
		}

		// Calculate expires_at based on TTL for this attempt so lock retries do
		// not shorten the effective lifetime.
		expiresAt := calculateExpiresAt(s.opts.sessionTTL)
		err = s.createSessionTransaction(ctx, key, sessState, sessBytes, expiresAt, now)
		if err == nil {
			break
		}
		if !isRetryableMySQLLockError(err) || attempt+1 == createSessionTransactionAttempts {
			return nil, err
		}
		if err := sleepBeforeCreateSessionRetry(ctx, attempt); err != nil {
			return nil, err
		}
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

func (s *Service) createSessionTransaction(
	ctx context.Context,
	key session.Key,
	sessState *SessionState,
	sessBytes []byte,
	expiresAt *time.Time,
	now time.Time,
) error {
	return s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		sessionExists, existingExpiresAt, nonExpiredExists, err := s.lockActiveSessionStates(ctx, tx, key, now)
		if err != nil {
			return err
		}

		if nonExpiredExists {
			log.ErrorfContext(
				ctx,
				"CreateSession: session already exists and not expired (app=%s, user=%s, session=%s, expires=%v)",
				key.AppName,
				key.UserID,
				key.SessionID,
				existingExpiresAt,
			)
			return fmt.Errorf("session already exists and has not expired")
		}
		if sessionExists {
			log.DebugfContext(
				ctx,
				"found expired session (app=%s, user=%s, session=%s), overwriting",
				key.AppName,
				key.UserID,
				key.SessionID,
			)
			if err := s.tombstoneDuplicateSessionStates(ctx, tx, []session.Key{key}, now); err != nil {
				return err
			}
		}

		log.DebugfContext(
			ctx,
			"CreateSession: inserting new session (app=%s, user=%s, session=%s)",
			key.AppName,
			key.UserID,
			key.SessionID,
		)

		var writeErr error
		if sessionExists {
			_, writeErr = tx.ExecContext(ctx,
				fmt.Sprintf(
					`UPDATE %s SET state = ?, created_at = ?, updated_at = ?, expires_at = ?, deleted_at = NULL
					WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
					s.tableSessionStates,
				),
				string(sessBytes), sessState.CreatedAt, sessState.UpdatedAt, expiresAt,
				key.AppName, key.UserID, key.SessionID,
			)
		} else {
			_, writeErr = tx.ExecContext(ctx,
				fmt.Sprintf(
					`INSERT INTO %s (app_name, user_id, session_id, state, created_at, updated_at, expires_at)
					VALUES (?, ?, ?, ?, ?, ?, ?)`,
					s.tableSessionStates,
				),
				key.AppName, key.UserID, key.SessionID, string(sessBytes),
				sessState.CreatedAt, sessState.UpdatedAt, expiresAt,
			)
		}
		if writeErr != nil {
			return fmt.Errorf("create session failed: %w", writeErr)
		}
		return nil
	}, func(options *sql.TxOptions) {
		// SERIALIZABLE avoids relying on the server default isolation for
		// absent-key locking between the existence check and insert.
		options.Isolation = sql.LevelSerializable
	})
}

func (s *Service) lockActiveSessionStates(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	now time.Time,
) (bool, sql.NullTime, bool, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(
		`SELECT expires_at FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
		FOR UPDATE`,
		s.tableSessionStates,
	), key.AppName, key.UserID, key.SessionID)
	if err != nil {
		return false, sql.NullTime{}, false, fmt.Errorf("check existing session failed: %w", err)
	}
	defer rows.Close()

	var sessionExists bool
	var existingExpiresAt sql.NullTime
	var nonExpiredExists bool
	for rows.Next() {
		var rowExpiresAt sql.NullTime
		sessionExists = true
		if err := rows.Scan(&rowExpiresAt); err != nil {
			return false, sql.NullTime{}, false, fmt.Errorf("check existing session failed: %w", err)
		}
		if !rowExpiresAt.Valid || rowExpiresAt.Time.After(now) {
			existingExpiresAt = rowExpiresAt
			nonExpiredExists = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, sql.NullTime{}, false, fmt.Errorf("check existing session failed: %w", err)
	}
	return sessionExists, existingExpiresAt, nonExpiredExists, nil
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
	if err := session.ValidateGetSessionOptions(opt, true); err != nil {
		return nil, err
	}
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
			c.Options.EventPage,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"mysql session service get session state failed: %w",
				err,
			)
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
	if err := session.ValidateListSessionsOptions(opt); err != nil {
		return nil, err
	}
	sessList, err := s.listSessions(
		ctx,
		userKey,
		opt.EventNum,
		opt.EventTime,
		opt.ListSessionOnlyMeta,
		opt.ListSessionPage,
	)
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
		// Include user_id for shard routing (TDSQL PK is (id, user_id));
		// also valid as an extra filter for standard MySQL.
		_, err = s.mysqlClient.Exec(ctx,
			fmt.Sprintf("UPDATE %s SET value = ?, updated_at = ?, expires_at = ? WHERE id = ? AND user_id = ?", s.tableUserStates),
			value, now, expiresAt, id, userID)
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

	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		sessState, _, err := loadSessionStateForUpdate(ctx, tx, s.tableSessionStates, key)
		if err != nil {
			if errors.Is(err, errSessionNotFound) {
				return fmt.Errorf("mysql session service update session state failed: session not found")
			}
			return fmt.Errorf("mysql session service update session state failed: %w", err)
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
			return fmt.Errorf("mysql session service update session state failed: marshal state: %w", err)
		}

		expiresAt := calculateExpiresAt(s.opts.sessionTTL)
		_, err = tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET state = ?, updated_at = ?, expires_at = ?
		 WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
			string(updatedStateBytes), now, expiresAt,
			key.AppName, key.UserID, key.SessionID)
		if err != nil {
			return fmt.Errorf("mysql session service update session state failed: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
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

// GetTrackEvents returns persisted track events for the given session track.
func (s *Service) GetTrackEvents(
	ctx context.Context,
	key session.Key,
	track session.Track,
	opts ...session.Option,
) (*session.TrackEvents, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	trackEvents, err := s.getTrackEventsByTrackLists(
		ctx,
		[]session.Key{key},
		[][]session.Track{{track}},
		opt.EventNum,
		opt.EventTime,
	)
	if err != nil {
		return nil, fmt.Errorf("mysql session service get track events failed: %w", err)
	}
	return &session.TrackEvents{Track: track, Events: trackEvents[0][track]}, nil
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
		if s.opts.tdsqlSharding {
			s.tdsqlCleanupExpiredSessions(ctx, now)
		} else {
			s.cleanupExpiredSessions(ctx, now)
		}
	}
	if s.opts.trackEventTTL != nil && s.opts.effectiveTrackEventTTL() > 0 {
		if s.opts.tdsqlSharding {
			s.tdsqlCleanupExpiredTrackEvents(ctx, now)
		} else {
			s.cleanupExpiredTrackEvents(ctx, now)
		}
	}

	// Clean up expired app states (broadcast table, no routing needed)
	if s.opts.appStateTTL > 0 {
		s.cleanupExpiredAppStates(ctx, now)
	}

	// Clean up expired user states
	if s.opts.userStateTTL > 0 {
		if s.opts.tdsqlSharding {
			s.tdsqlCleanupExpiredUserStates(ctx, now)
		} else {
			s.cleanupExpiredUserStates(ctx, now)
		}
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
			ORDER BY expires_at
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
		n, err := s.deleteSessions(ctx, tx, sessionKeys, now)
		if err != nil {
			return err
		}

		// We count the number of sessions deleted, not the total rows affected across all tables
		deletedCount = int64(n)
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
func (s *Service) deleteSessions(ctx context.Context, tx *sql.Tx, keys []session.Key, now time.Time) (int, error) {
	whereClause, args := s.sessionKeysWhereClause(keys)
	if whereClause == "" {
		return 0, nil
	}

	stateWhereClause := whereClause + " AND expires_at IS NOT NULL AND expires_at <= ?"
	stateArgs := append(append([]any(nil), args...), now)

	verifiedKeys, err := s.lockExpiredSessionKeys(ctx, tx, stateWhereClause, stateArgs)
	if err != nil {
		return 0, err
	}
	if len(verifiedKeys) == 0 {
		return 0, nil
	}

	uniqueKeys := deduplicateSessionKeys(verifiedKeys)
	childWhereClause, childArgs := s.sessionKeysWhereClause(uniqueKeys)
	stateWhereClause = childWhereClause + " AND expires_at IS NOT NULL AND expires_at <= ?"
	stateArgs = append(append([]any(nil), childArgs...), now)

	if s.opts.softDelete {
		if err := s.tombstoneDuplicateSessionStates(ctx, tx, uniqueKeys, now); err != nil {
			return 0, err
		}
		return len(uniqueKeys), s.softDeleteSessions(ctx, tx, stateWhereClause, stateArgs, childWhereClause, childArgs, now)
	}
	return len(uniqueKeys), s.hardDeleteSessions(ctx, tx, stateWhereClause, stateArgs, childWhereClause, childArgs)
}

func (s *Service) sessionKeysWhereClause(keys []session.Key) (string, []any) {
	return s.sessionKeysWhereClauseFor("", keys)
}

func (s *Service) sessionKeysWhereClauseFor(columnPrefix string, keys []session.Key) (string, []any) {
	if len(keys) == 0 {
		return "", nil
	}
	placeholders := make([]string, len(keys))
	args := make([]any, 0, len(keys)*3+1)
	for i, key := range keys {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}
	whereClause := fmt.Sprintf(
		`(%sapp_name, %suser_id, %ssession_id) IN (%s) AND %sdeleted_at IS NULL`,
		columnPrefix, columnPrefix, columnPrefix, strings.Join(placeholders, ","), columnPrefix,
	)

	// TDSQL proxy cannot extract shardkey from tuple comparison. Add an
	// explicit user_id filter for DML routing when keys share the same user_id.
	if s.opts.tdsqlSharding {
		whereClause += fmt.Sprintf(" AND %suser_id = ?", columnPrefix)
		args = append(args, keys[0].UserID)
	}
	return whereClause, args
}

func deduplicateSessionKeys(keys []session.Key) []session.Key {
	unique := make([]session.Key, 0, len(keys))
	seen := make(map[session.Key]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	return unique
}

func (s *Service) lockExpiredSessionKeys(
	ctx context.Context,
	tx *sql.Tx,
	stateWhereClause string,
	stateArgs []any,
) ([]session.Key, error) {
	rows, err := tx.QueryContext(ctx,
		fmt.Sprintf(`SELECT app_name, user_id, session_id FROM %s WHERE %s FOR UPDATE`,
			s.tableSessionStates, stateWhereClause),
		stateArgs...)
	if err != nil {
		return nil, fmt.Errorf("recheck expired sessions failed: %w", err)
	}
	defer rows.Close()

	var keys []session.Key
	for rows.Next() {
		var key session.Key
		if err := rows.Scan(&key.AppName, &key.UserID, &key.SessionID); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func (s *Service) tombstoneDuplicateSessionStates(
	ctx context.Context,
	tx *sql.Tx,
	keys []session.Key,
	now time.Time,
) error {
	whereClause, args := s.sessionKeysWhereClauseFor("duplicate.", keys)
	if whereClause == "" {
		return nil
	}

	query := fmt.Sprintf(`SELECT DISTINCT duplicate.id, duplicate.user_id FROM %s AS duplicate
		INNER JOIN %s AS canonical
			ON canonical.app_name = duplicate.app_name
			AND canonical.user_id = duplicate.user_id
			AND canonical.session_id = duplicate.session_id
			AND canonical.deleted_at IS NULL
			AND canonical.expires_at IS NOT NULL
			AND canonical.expires_at <= ?
			AND canonical.id < duplicate.id
		WHERE %s
			AND duplicate.expires_at IS NOT NULL
			AND duplicate.expires_at <= ?
		ORDER BY duplicate.id ASC
		FOR UPDATE`, s.tableSessionStates, s.tableSessionStates, whereClause)
	queryArgs := make([]any, 0, len(args)+2)
	queryArgs = append(queryArgs, now)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, now)

	type stateRow struct {
		id     int64
		userID string
	}
	rows, err := tx.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return fmt.Errorf("query duplicate session states: %w", err)
	}
	var duplicateRows []stateRow
	for rows.Next() {
		var row stateRow
		if err := rows.Scan(&row.id, &row.userID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan duplicate session state: %w", err)
		}
		duplicateRows = append(duplicateRows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate duplicate session states: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close duplicate session states: %w", err)
	}

	for i, row := range duplicateRows {
		// Use whole-second spacing so legacy TIMESTAMP/DATETIME columns without
		// fractional precision keep tombstones distinct from the canonical row.
		deletedAt := now.Add(-time.Duration(i+1) * time.Second)
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET deleted_at = ?
				WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
			deletedAt, row.id, row.userID); err != nil {
			if !isDuplicateEntryError(err) {
				return fmt.Errorf("tombstone duplicate session state %d: %w", row.id, err)
			}

			log.WarnfContext(ctx, "tombstoning session state %d hit a duplicate legacy row; "+
				"deleting only that active duplicate row: %v", row.id, err)
			if _, deleteErr := tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
					WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
				row.id, row.userID); deleteErr != nil {
				return fmt.Errorf("delete duplicate active session state %d: %w", row.id, deleteErr)
			}
		}
	}
	return nil
}

// softDeleteSessions performs soft delete on session tables.
func (s *Service) softDeleteSessions(
	ctx context.Context,
	tx *sql.Tx,
	stateWhereClause string,
	stateArgs []any,
	childWhereClause string,
	childArgs []any,
	now time.Time,
) error {
	// Soft delete session states. Legacy schemas may contain duplicate active rows
	// that collide when assigned the same deleted_at value.
	if err := s.softDeleteSessionStates(ctx, tx, stateWhereClause, stateArgs, now); err != nil {
		return fmt.Errorf("soft delete sessions: %w", err)
	}

	// Soft delete summaries. Legacy schemas may contain duplicate active rows
	// that collide when assigned the same deleted_at value.
	if err := s.softDeleteSummaries(ctx, tx, childWhereClause, childArgs, now); err != nil {
		return fmt.Errorf("soft delete summaries: %w", err)
	}

	// Soft delete events
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE %s`, s.tableSessionEvents, childWhereClause),
		append([]any{now}, childArgs...)...); err != nil {
		return fmt.Errorf("soft delete events: %w", err)
	}

	if s.opts.trackEventTTL == nil {
		// Soft delete track events.
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE %s`, s.tableSessionTracks, childWhereClause),
			append([]any{now}, childArgs...)...); err != nil {
			return fmt.Errorf("soft delete track events: %w", err)
		}
	}

	return nil
}

func (s *Service) softDeleteSessionStates(
	ctx context.Context,
	tx *sql.Tx,
	whereClause string,
	args []any,
	now time.Time,
) error {
	activeWhereClause := fmt.Sprintf("(%s) AND deleted_at IS NULL", whereClause)
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE %s`, s.tableSessionStates, activeWhereClause),
		append([]any{now}, args...)...)
	if err == nil {
		return nil
	}
	if !isDuplicateEntryError(err) {
		return err
	}

	// MySQL rolls back the failed UPDATE statement without aborting the
	// transaction. Retry each row so healthy states retain their soft-delete
	// history and only a row that still conflicts is physically removed.
	log.WarnfContext(ctx, "soft deleting session states hit duplicate legacy rows; "+
		"retrying the affected active session states individually: %v", err)
	return s.softDeleteSessionStatesIndividually(ctx, tx, activeWhereClause, args, now)
}

func (s *Service) softDeleteSessionStatesIndividually(
	ctx context.Context,
	tx *sql.Tx,
	activeWhereClause string,
	args []any,
	now time.Time,
) error {
	return s.softDeleteRowsIndividually(ctx, tx, s.tableSessionStates,
		"session state", "session states", activeWhereClause, args, now)
}

func (s *Service) softDeleteSummaries(
	ctx context.Context,
	tx *sql.Tx,
	whereClause string,
	args []any,
	now time.Time,
) error {
	activeWhereClause := fmt.Sprintf("(%s) AND deleted_at IS NULL", whereClause)
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE %s`, s.tableSessionSummaries, activeWhereClause),
		append([]any{now}, args...)...)
	if err == nil {
		return nil
	}
	if !isDuplicateEntryError(err) {
		return err
	}

	// MySQL rolls back the failed UPDATE statement without aborting the
	// transaction. Retry each row so healthy summaries retain their soft-delete
	// history and only a row that still conflicts is physically removed.
	log.WarnfContext(ctx, "soft deleting summaries hit duplicate legacy rows; "+
		"retrying the affected active summaries individually: %v", err)
	return s.softDeleteSummariesIndividually(ctx, tx, activeWhereClause, args, now)
}

func (s *Service) softDeleteSummariesIndividually(
	ctx context.Context,
	tx *sql.Tx,
	activeWhereClause string,
	args []any,
	now time.Time,
) error {
	return s.softDeleteRowsIndividually(ctx, tx, s.tableSessionSummaries,
		"summary", "summaries", activeWhereClause, args, now)
}

func (s *Service) softDeleteRowsIndividually(
	ctx context.Context,
	tx *sql.Tx,
	tableName string,
	rowName string,
	rowNamePlural string,
	activeWhereClause string,
	args []any,
	now time.Time,
) error {
	type rowKey struct {
		id     int64
		userID string
	}

	rows, err := tx.QueryContext(ctx,
		fmt.Sprintf(`SELECT id, user_id FROM %s WHERE %s ORDER BY id ASC FOR UPDATE`,
			tableName, activeWhereClause),
		args...)
	if err != nil {
		return fmt.Errorf("query active %s after soft-delete conflict: %w", rowNamePlural, err)
	}
	var keys []rowKey
	for rows.Next() {
		var key rowKey
		if err := rows.Scan(&key.id, &key.userID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan active %s after soft-delete conflict: %w", rowName, err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate active %s after soft-delete conflict: %w", rowNamePlural, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close active %s after soft-delete conflict: %w", rowNamePlural, err)
	}

	for _, key := range keys {
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET deleted_at = ?
				WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, tableName),
			now, key.id, key.userID)
		if err == nil {
			continue
		}
		if !isDuplicateEntryError(err) {
			return fmt.Errorf("soft delete %s %d individually: %w", rowName, key.id, err)
		}

		log.WarnfContext(ctx, "soft deleting %s %d still hit a duplicate legacy row; "+
			"deleting only that active row: %v", rowName, key.id, err)
		if _, deleteErr := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s
				WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, tableName),
			key.id, key.userID); deleteErr != nil {
			return fmt.Errorf("delete duplicate active %s %d: %w", rowName, key.id, deleteErr)
		}
	}
	return nil
}

func isDuplicateEntryError(err error) bool {
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == sqldb.MySQLErrDuplicateEntry
}

func isRetryableMySQLLockError(err error) bool {
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) &&
		(mysqlErr.Number == sqldb.MySQLErrLockWaitTimeout || mysqlErr.Number == sqldb.MySQLErrLockDeadlock)
}

func sleepBeforeCreateSessionRetry(ctx context.Context, attempt int) error {
	const base = 10 * time.Millisecond
	jitter := time.Duration(rand.Int63n(int64(base)))
	delay := time.Duration(attempt+1)*base + jitter
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// hardDeleteSessions performs hard delete on session tables.
func (s *Service) hardDeleteSessions(
	ctx context.Context,
	tx *sql.Tx,
	stateWhereClause string,
	stateArgs []any,
	childWhereClause string,
	childArgs []any,
) error {
	// Hard delete session states
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tableSessionStates, stateWhereClause),
		stateArgs...)
	if err != nil {
		return fmt.Errorf("hard delete sessions: %w", err)
	}

	// Hard delete summaries
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tableSessionSummaries, childWhereClause),
		childArgs...); err != nil {
		return fmt.Errorf("hard delete summaries: %w", err)
	}

	// Hard delete events
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tableSessionEvents, childWhereClause),
		childArgs...); err != nil {
		return fmt.Errorf("hard delete events: %w", err)
	}

	if s.opts.trackEventTTL == nil {
		// Hard delete track events.
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tableSessionTracks, childWhereClause),
			childArgs...); err != nil {
			return fmt.Errorf("hard delete track events: %w", err)
		}
	}

	return nil
}

func (s *Service) cleanupExpiredTrackEvents(ctx context.Context, now time.Time) {
	if s.opts.softDelete {
		_, err := s.mysqlClient.Exec(ctx,
			fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`, s.tableSessionTracks),
			now, now)
		if err != nil {
			log.ErrorfContext(ctx, "cleanup expired track events failed: %v", err)
		}
		return
	}
	_, err := s.mysqlClient.Exec(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at <= ?`, s.tableSessionTracks),
		now)
	if err != nil {
		log.ErrorfContext(ctx, "cleanup expired track events failed: %v", err)
	}
}

func (s *Service) tdsqlCleanupExpiredTrackEvents(ctx context.Context, now time.Time) {
	type idPair struct {
		id     int64
		userID string
	}
	query := fmt.Sprintf(`SELECT id, user_id FROM %s
		WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL
		LIMIT 1000`, s.tableSessionTracks)
	var pairs []idPair
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		var p idPair
		if err := rows.Scan(&p.id, &p.userID); err != nil {
			return err
		}
		pairs = append(pairs, p)
		return nil
	}, query, now)
	if err != nil {
		log.ErrorfContext(ctx, "tdsql cleanup: scan expired track events failed: %v", err)
		return
	}
	if len(pairs) == 0 {
		return
	}
	grouped := make(map[string][]int64)
	for _, p := range pairs {
		grouped[p.userID] = append(grouped[p.userID], p.id)
	}
	var total int64
	for userID, ids := range grouped {
		placeholders := make([]string, len(ids))
		for i := range ids {
			placeholders[i] = "?"
		}
		idClause := strings.Join(placeholders, ",")
		var err error
		if s.opts.softDelete {
			args := make([]any, 0, len(ids)+3)
			args = append(args, now)
			for _, id := range ids {
				args = append(args, id)
			}
			args = append(args, userID, now)
			_, err = s.mysqlClient.Exec(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE id IN (%s) AND user_id = ? AND expires_at IS NOT NULL AND expires_at <= ?`,
					s.tableSessionTracks, idClause),
				args...)
		} else {
			args := make([]any, 0, len(ids)+2)
			for _, id := range ids {
				args = append(args, id)
			}
			args = append(args, userID, now)
			_, err = s.mysqlClient.Exec(ctx,
				fmt.Sprintf(`DELETE FROM %s WHERE id IN (%s) AND user_id = ? AND expires_at IS NOT NULL AND expires_at <= ?`,
					s.tableSessionTracks, idClause),
				args...)
		}
		if err != nil {
			log.ErrorfContext(ctx, "tdsql cleanup: delete track events failed: %v", err)
			continue
		}
		total += int64(len(ids))
	}
	if total > 0 {
		log.InfofContext(ctx, "tdsql cleanup: cleaned up %d expired track events", total)
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

// tdsqlCleanupExpiredSessions uses two-phase cleanup for TDSQL distributed mode.
// Phase 1: scan expired sessions without FOR UPDATE (cross-shard read is allowed).
// Phase 2: delete per user_id so DML routes to the correct shard.
func (s *Service) tdsqlCleanupExpiredSessions(ctx context.Context, now time.Time) {
	query := fmt.Sprintf(`SELECT app_name, user_id, session_id FROM %s
		WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL
		LIMIT 1000`,
		s.tableSessionStates)

	var sessionKeys []session.Key
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		var app, user, sess string
		if err := rows.Scan(&app, &user, &sess); err != nil {
			return err
		}
		sessionKeys = append(sessionKeys, session.Key{
			AppName: app, UserID: user, SessionID: sess,
		})
		return nil
	}, query, now)

	if err != nil {
		log.ErrorfContext(ctx, "tdsql cleanup: scan expired sessions failed: %v", err)
		return
	}
	if len(sessionKeys) == 0 {
		return
	}

	// Group by user_id for shard-local DML.
	grouped := make(map[string][]session.Key)
	for _, k := range sessionKeys {
		grouped[k.UserID] = append(grouped[k.UserID], k)
	}

	var total int64
	for _, keys := range grouped {
		var n int
		err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
			var err error
			n, err = s.deleteSessions(ctx, tx, keys, now)
			return err
		})
		if err != nil {
			log.ErrorfContext(ctx, "tdsql cleanup: delete sessions failed: %v", err)
			continue
		}
		total += int64(n)
	}

	if total > 0 {
		log.InfofContext(ctx, "tdsql cleanup: cleaned up %d expired sessions", total)
	}
}

// tdsqlCleanupExpiredUserStates uses two-phase cleanup for TDSQL distributed mode.
// Phase 1: scan expired user_states to get (id, user_id) pairs.
// Phase 2: delete per user_id for shard routing.
func (s *Service) tdsqlCleanupExpiredUserStates(ctx context.Context, now time.Time) {
	type idPair struct {
		id     int64
		userID string
	}

	query := fmt.Sprintf(`SELECT id, user_id FROM %s
		WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL
		LIMIT 1000`, s.tableUserStates)

	var pairs []idPair
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		var p idPair
		if err := rows.Scan(&p.id, &p.userID); err != nil {
			return err
		}
		pairs = append(pairs, p)
		return nil
	}, query, now)

	if err != nil {
		log.ErrorfContext(ctx, "tdsql cleanup: scan expired user states failed: %v", err)
		return
	}
	if len(pairs) == 0 {
		return
	}

	// Group by user_id for shard-local DML.
	grouped := make(map[string][]int64)
	for _, p := range pairs {
		grouped[p.userID] = append(grouped[p.userID], p.id)
	}

	var total int64
	for userID, ids := range grouped {
		placeholders := make([]string, len(ids))
		for i := range ids {
			placeholders[i] = "?"
		}
		idClause := strings.Join(placeholders, ",")

		// Recheck expiry to prevent deleting renewed user states between scan and delete.
		var err error
		if s.opts.softDelete {
			args := make([]any, 0, len(ids)+3)
			args = append(args, now)
			for _, id := range ids {
				args = append(args, id)
			}
			args = append(args, userID, now)
			_, err = s.mysqlClient.Exec(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = ? WHERE id IN (%s) AND user_id = ? AND expires_at IS NOT NULL AND expires_at <= ?`,
					s.tableUserStates, idClause),
				args...)
		} else {
			args := make([]any, 0, len(ids)+2)
			for _, id := range ids {
				args = append(args, id)
			}
			args = append(args, userID, now)
			_, err = s.mysqlClient.Exec(ctx,
				fmt.Sprintf(`DELETE FROM %s WHERE id IN (%s) AND user_id = ? AND expires_at IS NOT NULL AND expires_at <= ?`,
					s.tableUserStates, idClause),
				args...)
		}
		if err != nil {
			log.ErrorfContext(ctx, "tdsql cleanup: delete user states failed: %v", err)
			continue
		}
		total += int64(len(ids))
	}

	if total > 0 {
		log.InfofContext(ctx, "tdsql cleanup: cleaned up %d expired user states", total)
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
