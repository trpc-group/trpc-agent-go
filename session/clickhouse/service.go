//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

var _ session.Service = (*Service)(nil)

// SessionState is the state of a session.
type SessionState struct {
	ID        string           `json:"id"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// Service is the ClickHouse session service.
type Service struct {
	opts           ServiceOpts
	chClient       storage.Client
	asyncWorker    *isummary.AsyncSummaryWorker // async summary worker
	eventPairChans []chan *sessionEventPair     // channel for session events to persistence
	cleanupTicker  *time.Ticker                 // ticker for automatic cleanup
	cleanupDone    chan struct{}                // signal to stop cleanup routine
	cleanupOnce    sync.Once                    // ensure cleanup routine is stopped only once
	persistWg      sync.WaitGroup               // wait group for persist workers
	once           sync.Once

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

// NewService creates a new ClickHouse session service.
// It requires either a DSN (WithClickHouseDSN) or an instance name (WithClickHouseInstance).
func NewService(options ...ServiceOpt) (*Service, error) {
	// Apply default options
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	// Create ClickHouse client
	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderDSN(opts.dsn),
		storage.WithExtraOptions(opts.extraOptions...),
	}
	if opts.dsn == "" && opts.instanceName != "" {
		// Method 2: Use pre-registered ClickHouse instance
		var ok bool
		if builderOpts, ok = storage.GetClickHouseInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("clickhouse instance %s not found", opts.instanceName)
		}
	}

	chClient, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create clickhouse client failed: %w", err)
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
		chClient:              chClient,
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
	s.once.Do(func() {
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
		if s.asyncWorker != nil {
			s.asyncWorker.Stop()
		}

		// Close ClickHouse client
		if s.chClient != nil {
			_ = s.chClient.Close()
		}
	})

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

	// Check if session already exists using FINAL for deduplication
	var sessionExists bool
	var existingExpiresAt *time.Time
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT expires_at FROM %s FINAL 
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)
	if err != nil {
		return nil, fmt.Errorf("check existing session failed: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		sessionExists = true
		var expAt *time.Time
		if err := rows.Scan(&expAt); err != nil {
			return nil, fmt.Errorf("scan expires_at failed: %w", err)
		}
		existingExpiresAt = expAt
	}

	if sessionExists && (existingExpiresAt == nil || existingExpiresAt.After(now)) {
		log.Infof("CreateSession: session already exists (app=%s, user=%s, session=%s)",
			key.AppName, key.UserID, key.SessionID)
		return nil, fmt.Errorf("session already exists and has not expired")
	}

	log.Debugf("CreateSession: inserting new session (app=%s, user=%s, session=%s)",
		key.AppName, key.UserID, key.SessionID)

	// Insert session state (ClickHouse INSERT)
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, state, extra_data, created_at, updated_at, expires_at) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID, string(sessBytes), "{}", sessState.CreatedAt, sessState.UpdatedAt, expiresAt,
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
	final := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		sess, err := s.getSession(c.Context, c.Key, c.Options.EventNum, c.Options.EventTime)
		if err != nil {
			return nil, fmt.Errorf("clickhouse session service get session state failed: %w", err)
		}

		// Refresh session TTL if configured and session exists
		if sess != nil && s.opts.sessionTTL > 0 {
			if err := s.refreshSessionTTL(c.Context, c.Key); err != nil {
				log.WarnfContext(c.Context, "failed to refresh session TTL: %v", err)
				// Don't fail the GetSession call, just log the warning
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
		return nil, fmt.Errorf("clickhouse session service get session list failed: %w", err)
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
		return fmt.Errorf("clickhouse session service delete session state failed: %w", err)
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
		err := s.chClient.Exec(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, key, value, updated_at, expires_at) VALUES (?, ?, ?, ?, ?)`, s.tableAppStates),
			appName, k, string(v), now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf("clickhouse session service update app state failed: %w", err)
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
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT key, value FROM %s FINAL 
			WHERE app_name = ? AND (expires_at IS NULL OR expires_at > ?) AND deleted_at IS NULL`, s.tableAppStates),
		appName, time.Now())

	if err != nil {
		return nil, fmt.Errorf("clickhouse session service list app states failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		appStateMap[key] = []byte(value)
	}

	return appStateMap, nil
}

// DeleteAppState soft-deletes the state by target scope and key.
func (s *Service) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	now := time.Now()

	// Get current state to preserve fields
	var value string
	var expiresAt *time.Time
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT value, expires_at FROM %s FINAL
			WHERE app_name = ? AND key = ? AND deleted_at IS NULL`, s.tableAppStates),
		appName, key)
	if err != nil {
		return fmt.Errorf("get app state for delete failed: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		// Not found or already deleted
		return nil
	}
	if err := rows.Scan(&value, &expiresAt); err != nil {
		return fmt.Errorf("scan app state failed: %w", err)
	}

	// Soft delete: INSERT new version with deleted_at set
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, key, value, updated_at, expires_at, deleted_at)
			VALUES (?, ?, ?, ?, ?, ?)`, s.tableAppStates),
		appName, key, value, now, expiresAt, now)

	if err != nil {
		return fmt.Errorf("soft delete app state failed: %w", err)
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
		err := s.chClient.Exec(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, key, value, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`, s.tableUserStates),
			userKey.AppName, userKey.UserID, k, string(v), now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf("clickhouse session service update user state failed: %w", err)
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
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT key, value FROM %s FINAL 
			WHERE app_name = ? AND user_id = ? AND (expires_at IS NULL OR expires_at > ?) AND deleted_at IS NULL`, s.tableUserStates),
		userKey.AppName, userKey.UserID, time.Now())

	if err != nil {
		return nil, fmt.Errorf("clickhouse session service list user states failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		userStateMap[key] = []byte(value)
	}

	return userStateMap, nil
}

// UpdateSessionState updates the session-level state directly without appending an event.
func (s *Service) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	// Validate: disallow app: and user: prefixes
	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf("clickhouse session service update session state failed: %s is not allowed, use UpdateAppState instead", k)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf("clickhouse session service update session state failed: %s is not allowed, use UpdateUserState instead", k)
		}
	}

	// Get current session state using FINAL
	var currentStateStr string
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT state FROM %s FINAL WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("clickhouse session service update session state failed: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("clickhouse session service update session state failed: session not found")
	}
	if err := rows.Scan(&currentStateStr); err != nil {
		return fmt.Errorf("clickhouse session service update session state failed: %w", err)
	}

	// Unmarshal current state
	var sessState SessionState
	if len(currentStateStr) > 0 {
		if err := json.Unmarshal([]byte(currentStateStr), &sessState); err != nil {
			return fmt.Errorf("clickhouse session service update session state failed: unmarshal state: %w", err)
		}
	}
	if sessState.State == nil {
		sessState.State = make(session.StateMap)
	}

	// Merge new state into current state
	for k, v := range state {
		sessState.State[k] = v
	}

	// Marshal updated state
	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("clickhouse session service update session state failed: marshal state: %w", err)
	}

	// Update session state in database (INSERT new version for ReplacingMergeTree)
	now := time.Now()
	expiresAt := calculateExpiresAt(s.opts.sessionTTL)

	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, state, extra_data, created_at, updated_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID, string(updatedStateBytes), "{}", sessState.CreatedAt, now, expiresAt)

	if err != nil {
		return fmt.Errorf("clickhouse session service update session state failed: %w", err)
	}

	return nil
}

// DeleteUserState soft-deletes the state by target scope and key.
func (s *Service) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	now := time.Now()

	// Get current state to preserve fields
	var value string
	var expiresAt *time.Time
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT value, expires_at FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND key = ? AND deleted_at IS NULL`, s.tableUserStates),
		userKey.AppName, userKey.UserID, key)
	if err != nil {
		return fmt.Errorf("get user state for delete failed: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		// Not found or already deleted
		return nil
	}
	if err := rows.Scan(&value, &expiresAt); err != nil {
		return fmt.Errorf("scan user state failed: %w", err)
	}

	// Soft delete: INSERT new version with deleted_at set
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, key, value, updated_at, expires_at, deleted_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, s.tableUserStates),
		userKey.AppName, userKey.UserID, key, value, now, expiresAt, now)

	if err != nil {
		return fmt.Errorf("soft delete user state failed: %w", err)
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

	// persist event to ClickHouse asynchronously
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && err.Error() == "send on closed channel" {
					log.Errorf("clickhouse session service append event failed: %v", r)
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
		return fmt.Errorf("clickhouse session service append event failed: %w", err)
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
			batch := make([]*sessionEventPair, 0, s.opts.batchSize)
			ticker := time.NewTicker(s.opts.batchTimeout)
			defer ticker.Stop()

			for {
				select {
				case pair, ok := <-eventPairChan:
					if !ok {
						s.flushEventBatch(batch)
						return
					}
					batch = append(batch, pair)
					if len(batch) >= s.opts.batchSize {
						s.flushEventBatch(batch)
						batch = batch[:0]
					}
				case <-ticker.C:
					if len(batch) > 0 {
						s.flushEventBatch(batch)
						batch = batch[:0]
					}
				}
			}
		}(eventPairChan)
	}
}

// flushEventBatch flushes a batch of events to ClickHouse.
func (s *Service) flushEventBatch(batch []*sessionEventPair) {
	if len(batch) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultAsyncPersistTimeout)
	defer cancel()

	// Batch insert all events
	for _, pair := range batch {
		if err := s.addEvent(ctx, pair.key, pair.event); err != nil {
			log.Errorf("async persist event failed: %v", err)
		}
	}
}

// startCleanupRoutine starts a background routine to periodically clean up expired data.
func (s *Service) startCleanupRoutine() {
	interval := s.opts.cleanupInterval
	if interval <= 0 {
		interval = defaultCleanupInterval
	}

	s.cleanupTicker = time.NewTicker(interval)
	s.cleanupDone = make(chan struct{})

	go func() {
		log.Infof("started cleanup routine for clickhouse session service (interval: %v)", interval)
		for {
			select {
			case <-s.cleanupTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				s.cleanupExpiredData(ctx)
				cancel()
			case <-s.cleanupDone:
				log.Info("cleanup routine stopped for clickhouse session service")
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

// cleanupExpiredData cleans up expired and soft-deleted data.
// For expired data: soft-delete by inserting new version with deleted_at set.
// For soft-deleted data past retention: physically remove via ALTER TABLE DELETE.
func (s *Service) cleanupExpiredData(ctx context.Context) {
	now := time.Now()

	// Physical cleanup of soft-deleted data past retention period
	if s.opts.deletedRetention > 0 {
		s.cleanupDeletedData(ctx, now)
	}

	// Soft-delete expired sessions (mark as deleted, don't physically remove)
	if s.opts.sessionTTL > 0 {
		s.softDeleteExpiredSessions(ctx, now)
	}

	// Soft-delete expired app states
	if s.opts.appStateTTL > 0 {
		s.softDeleteExpiredAppStates(ctx, now)
	}

	// Soft-delete expired user states
	if s.opts.userStateTTL > 0 {
		s.softDeleteExpiredUserStates(ctx, now)
	}
}

// cleanupDeletedData physically removes soft-deleted data past retention period.
// This is the only place where ALTER TABLE DELETE is used.
func (s *Service) cleanupDeletedData(ctx context.Context, now time.Time) {
	cutoff := now.Add(-s.opts.deletedRetention)

	// Physical delete of soft-deleted session states
	err := s.chClient.Exec(ctx,
		fmt.Sprintf(`ALTER TABLE %s DELETE WHERE deleted_at IS NOT NULL AND deleted_at <= ?`, s.tableSessionStates),
		cutoff)
	if err != nil {
		log.Errorf("cleanup deleted session states failed: %v", err)
	}

	// Physical delete of soft-deleted session events
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`ALTER TABLE %s DELETE WHERE deleted_at IS NOT NULL AND deleted_at <= ?`, s.tableSessionEvents),
		cutoff)
	if err != nil {
		log.Errorf("cleanup deleted session events failed: %v", err)
	}

	// Physical delete of soft-deleted session summaries
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`ALTER TABLE %s DELETE WHERE deleted_at IS NOT NULL AND deleted_at <= ?`, s.tableSessionSummaries),
		cutoff)
	if err != nil {
		log.Errorf("cleanup deleted session summaries failed: %v", err)
	}

	// Physical delete of soft-deleted app states
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`ALTER TABLE %s DELETE WHERE deleted_at IS NOT NULL AND deleted_at <= ?`, s.tableAppStates),
		cutoff)
	if err != nil {
		log.Errorf("cleanup deleted app states failed: %v", err)
	}

	// Physical delete of soft-deleted user states
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`ALTER TABLE %s DELETE WHERE deleted_at IS NOT NULL AND deleted_at <= ?`, s.tableUserStates),
		cutoff)
	if err != nil {
		log.Errorf("cleanup deleted user states failed: %v", err)
	}

	log.Debugf("cleaned up soft-deleted data older than %v", cutoff)
}

// softDeleteExpiredSessions marks expired sessions as deleted.
func (s *Service) softDeleteExpiredSessions(ctx context.Context, now time.Time) {
	// Query expired but not yet deleted sessions
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT app_name, user_id, session_id, state, created_at, expires_at FROM %s FINAL
			WHERE expires_at IS NOT NULL AND expires_at <= ?
			AND deleted_at IS NULL`, s.tableSessionStates),
		now)
	if err != nil {
		log.Errorf("query expired sessions failed: %v", err)
		return
	}
	defer rows.Close()

	type expiredSession struct {
		appName, userID, sessionID, stateStr string
		createdAt                            time.Time
		expiresAt                            *time.Time
	}
	var expiredSessions []expiredSession

	for rows.Next() {
		var es expiredSession
		if err := rows.Scan(&es.appName, &es.userID, &es.sessionID, &es.stateStr, &es.createdAt, &es.expiresAt); err != nil {
			log.Errorf("scan expired session failed: %v", err)
			continue
		}
		expiredSessions = append(expiredSessions, es)
	}

	if len(expiredSessions) > 0 {
		err = s.chClient.BatchInsert(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, state, extra_data, created_at, updated_at, expires_at, deleted_at)`,
				s.tableSessionStates),
			func(batch driver.Batch) error {
				for _, es := range expiredSessions {
					if err := batch.Append(es.appName, es.userID, es.sessionID, es.stateStr, "{}", es.createdAt, now, es.expiresAt, now); err != nil {
						return err
					}
				}
				return nil
			})
		if err != nil {
			log.Errorf("batch soft delete expired sessions failed: %v", err)
		}

		// Soft delete events and summaries for expired sessions
		for _, es := range expiredSessions {
			key := session.Key{
				AppName:   es.appName,
				UserID:    es.userID,
				SessionID: es.sessionID,
			}
			s.softDeleteSessionEvents(ctx, key, now)
			s.softDeleteSessionSummaries(ctx, key, now)
		}
	}
}

// softDeleteSessionEvents marks all events for a session as deleted.
func (s *Service) softDeleteSessionEvents(ctx context.Context, key session.Key, now time.Time) {
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT event_id, event, created_at, updated_at FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL`, s.tableSessionEvents),
		key.AppName, key.UserID, key.SessionID)
	if err != nil {
		log.Errorf("query session events for delete failed: %v", err)
		return
	}
	defer rows.Close()

	type eventRecord struct {
		eventID, eventData   string
		createdAt, updatedAt time.Time
	}
	var events []eventRecord

	for rows.Next() {
		var e eventRecord
		if err := rows.Scan(&e.eventID, &e.eventData, &e.createdAt, &e.updatedAt); err != nil {
			log.Errorf("scan event failed: %v", err)
			continue
		}
		events = append(events, e)
	}

	if len(events) > 0 {
		err = s.chClient.BatchInsert(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, event_id, event, extra_data, created_at, updated_at, deleted_at)`,
				s.tableSessionEvents),
			func(batch driver.Batch) error {
				for _, e := range events {
					if err := batch.Append(key.AppName, key.UserID, key.SessionID, e.eventID, e.eventData, "{}", e.createdAt, now, now); err != nil {
						return err
					}
				}
				return nil
			})
		if err != nil {
			log.Errorf("batch soft delete session events failed: %v", err)
		}
	}
}

// softDeleteSessionSummaries marks all summaries for a session as deleted.
func (s *Service) softDeleteSessionSummaries(ctx context.Context, key session.Key, now time.Time) {
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT filter_key, summary, created_at, updated_at FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID)
	if err != nil {
		log.Errorf("query session summaries for delete failed: %v", err)
		return
	}
	defer rows.Close()

	type summaryRecord struct {
		filterKey, summaryData string
		createdAt, updatedAt   time.Time
	}
	var summaries []summaryRecord

	for rows.Next() {
		var s summaryRecord
		if err := rows.Scan(&s.filterKey, &s.summaryData, &s.createdAt, &s.updatedAt); err != nil {
			log.Errorf("scan summary failed: %v", err)
			continue
		}
		summaries = append(summaries, s)
	}

	if len(summaries) > 0 {
		err = s.chClient.BatchInsert(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, created_at, updated_at, deleted_at)`,
				s.tableSessionSummaries),
			func(batch driver.Batch) error {
				for _, sum := range summaries {
					if err := batch.Append(key.AppName, key.UserID, key.SessionID, sum.filterKey, sum.summaryData, sum.createdAt, now, now); err != nil {
						return err
					}
				}
				return nil
			})
		if err != nil {
			log.Errorf("batch soft delete session summaries failed: %v", err)
		}
	}
}

// softDeleteExpiredAppStates marks expired app states as deleted.
func (s *Service) softDeleteExpiredAppStates(ctx context.Context, now time.Time) {
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT app_name, key, value, expires_at FROM %s FINAL
			WHERE expires_at IS NOT NULL AND expires_at <= ?
			AND deleted_at IS NULL`, s.tableAppStates),
		now)
	if err != nil {
		log.Errorf("query expired app states failed: %v", err)
		return
	}
	defer rows.Close()

	type expiredAppState struct {
		appName, key, value string
		expiresAt           *time.Time
	}
	var expiredStates []expiredAppState

	for rows.Next() {
		var es expiredAppState
		if err := rows.Scan(&es.appName, &es.key, &es.value, &es.expiresAt); err != nil {
			log.Errorf("scan expired app state failed: %v", err)
			continue
		}
		expiredStates = append(expiredStates, es)
	}

	if len(expiredStates) > 0 {
		err = s.chClient.BatchInsert(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, key, value, updated_at, expires_at, deleted_at)`,
				s.tableAppStates),
			func(batch driver.Batch) error {
				for _, es := range expiredStates {
					if err := batch.Append(es.appName, es.key, es.value, now, es.expiresAt, now); err != nil {
						return err
					}
				}
				return nil
			})
		if err != nil {
			log.Errorf("batch soft delete expired app states failed: %v", err)
		}
	}
}

// softDeleteExpiredUserStates marks expired user states as deleted.
func (s *Service) softDeleteExpiredUserStates(ctx context.Context, now time.Time) {
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT app_name, user_id, key, value, expires_at FROM %s FINAL
			WHERE expires_at IS NOT NULL AND expires_at <= ?
			AND deleted_at IS NULL`, s.tableUserStates),
		now)
	if err != nil {
		log.Errorf("query expired user states failed: %v", err)
		return
	}
	defer rows.Close()

	type expiredUserState struct {
		appName, userID, key, value string
		expiresAt                   *time.Time
	}
	var expiredStates []expiredUserState

	for rows.Next() {
		var es expiredUserState
		if err := rows.Scan(&es.appName, &es.userID, &es.key, &es.value, &es.expiresAt); err != nil {
			log.Errorf("scan expired user state failed: %v", err)
			continue
		}
		expiredStates = append(expiredStates, es)
	}

	if len(expiredStates) > 0 {
		err = s.chClient.BatchInsert(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, key, value, updated_at, expires_at, deleted_at)`,
				s.tableUserStates),
			func(batch driver.Batch) error {
				for _, es := range expiredStates {
					if err := batch.Append(es.appName, es.userID, es.key, es.value, now, es.expiresAt, now); err != nil {
						return err
					}
				}
				return nil
			})
		if err != nil {
			log.Errorf("batch soft delete expired user states failed: %v", err)
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
