//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlite provides the sqlite session service.
package sqlite

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
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
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

// Service is the sqlite session service.
type Service struct {
	opts ServiceOpts
	db   *sql.DB

	eventPairChans  []chan *sessionEventPair
	trackEventChans []chan *trackEventPair
	persistWg       sync.WaitGroup

	asyncWorker *isummary.AsyncSummaryWorker

	cleanupTicker *time.Ticker
	cleanupDone   chan struct{}
	cleanupOnce   sync.Once

	once sync.Once

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

// NewService creates a new sqlite session service.
//
// The service owns the passed-in db and will close it in Close().
func NewService(db *sql.DB, options ...ServiceOpt) (*Service, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}

	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	if opts.cleanupInterval <= 0 {
		if opts.sessionTTL > 0 || opts.appStateTTL > 0 ||
			opts.userStateTTL > 0 {
			opts.cleanupInterval = defaultCleanupInterval
		}
	}

	s := &Service{
		opts:        opts,
		db:          db,
		cleanupDone: make(chan struct{}),

		tableSessionStates: sqldb.BuildTableName(
			opts.tablePrefix,
			sqldb.TableNameSessionStates,
		),
		tableSessionEvents: sqldb.BuildTableName(
			opts.tablePrefix,
			sqldb.TableNameSessionEvents,
		),
		tableSessionTracks: sqldb.BuildTableName(
			opts.tablePrefix,
			sqldb.TableNameSessionTrackEvents,
		),
		tableSessionSummaries: sqldb.BuildTableName(
			opts.tablePrefix,
			sqldb.TableNameSessionSummaries,
		),
		tableAppStates: sqldb.BuildTableName(
			opts.tablePrefix,
			sqldb.TableNameAppStates,
		),
		tableUserStates: sqldb.BuildTableName(
			opts.tablePrefix,
			sqldb.TableNameUserStates,
		),
	}

	if !opts.skipDBInit {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			defaultDBInitTimeout,
		)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			return nil, fmt.Errorf("init database: %w", err)
		}
	}

	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}

	if isummary.HasSummarizer(opts.summarizer) && opts.asyncSummaryNum > 0 {
		s.asyncWorker = isummary.NewAsyncSummaryWorker(
			isummary.AsyncSummaryConfig{
				Summarizer:        opts.summarizer,
				AsyncSummaryNum:   opts.asyncSummaryNum,
				SummaryQueueSize:  opts.summaryQueueSize,
				SummaryJobTimeout: opts.summaryJobTimeout,
				CreateSummaryFunc: s.CreateSessionSummary,
			},
		)
		s.asyncWorker.Start()
	}

	if opts.cleanupInterval > 0 {
		s.startCleanupRoutine()
	}

	return s, nil
}

// Close closes the service and releases resources.
func (s *Service) Close() error {
	var closeErr error
	s.once.Do(func() {
		s.stopCleanupRoutine()

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

		if s.asyncWorker != nil {
			s.asyncWorker.Stop()
		}

		if s.db != nil {
			closeErr = s.db.Close()
		}
	})
	return closeErr
}

func (s *Service) fullTableName(base string) string {
	switch base {
	case sqldb.TableNameSessionStates:
		return s.tableSessionStates
	case sqldb.TableNameSessionEvents:
		return s.tableSessionEvents
	case sqldb.TableNameSessionTrackEvents:
		return s.tableSessionTracks
	case sqldb.TableNameSessionSummaries:
		return s.tableSessionSummaries
	case sqldb.TableNameAppStates:
		return s.tableAppStates
	case sqldb.TableNameUserStates:
		return s.tableUserStates
	default:
		return sqldb.BuildTableName(s.opts.tablePrefix, base)
	}
}

func applyOptions(opts ...session.Option) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}

func calculateExpiresAt(now time.Time, ttl time.Duration) *int64 {
	if ttl <= 0 {
		return nil
	}
	expires := now.Add(ttl).UTC().UnixNano()
	return &expires
}

func unixNanoToTime(ns int64) time.Time {
	return time.Unix(0, ns).UTC()
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
		if v == nil {
			sessState.State[k] = nil
			continue
		}
		copied := make([]byte, len(v))
		copy(copied, v)
		sessState.State[k] = copied
	}

	stateBytes, err := json.Marshal(sessState)
	if err != nil {
		return nil, fmt.Errorf("marshal session: %w", err)
	}

	expiresAt := calculateExpiresAt(now, s.opts.sessionTTL)

	exists, existingExpiresAt, err := s.checkSessionExists(ctx, key)
	if err != nil {
		return nil, err
	}
	if exists && !isExpired(existingExpiresAt, now) {
		return nil, fmt.Errorf("session already exists and has not expired")
	}

	if err := s.upsertSessionState(ctx, key, stateBytes, now, expiresAt,
		exists); err != nil {
		return nil, err
	}

	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, fmt.Errorf("list app states: %w", err)
	}
	userState, err := s.ListUserStates(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	if err != nil {
		return nil, fmt.Errorf("list user states: %w", err)
	}

	sess := session.NewSession(
		key.AppName,
		key.UserID,
		sessState.ID,
		session.WithSessionState(sessState.State),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)

	return mergeState(appState, userState, sess), nil
}

func (s *Service) checkSessionExists(
	ctx context.Context,
	key session.Key,
) (bool, sql.NullInt64, error) {
	var expiresAt sql.NullInt64
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT expires_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, sql.NullInt64{}, nil
	}
	if err != nil {
		return false, sql.NullInt64{}, fmt.Errorf(
			"check existing session: %w",
			err,
		)
	}
	return true, expiresAt, nil
}

func isExpired(expiresAt sql.NullInt64, now time.Time) bool {
	if !expiresAt.Valid {
		return false
	}
	return unixNanoToTime(expiresAt.Int64).Before(now)
}

func (s *Service) upsertSessionState(
	ctx context.Context,
	key session.Key,
	stateBytes []byte,
	now time.Time,
	expiresAt *int64,
	exists bool,
) error {
	if exists {
		_, err := s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s
SET state = ?, created_at = ?, updated_at = ?, expires_at = ?,
    deleted_at = NULL
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
				s.tableSessionStates,
			),
			stateBytes,
			now.UTC().UnixNano(),
			now.UTC().UnixNano(),
			expiresAt,
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		if err != nil {
			return fmt.Errorf("update session: %w", err)
		}
		return nil
	}

	_, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
  app_name, user_id, session_id, state, created_at, updated_at, expires_at,
  deleted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		stateBytes,
		now.UTC().UnixNano(),
		now.UTC().UnixNano(),
		expiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
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
			return nil, err
		}

		return sess, nil
	}

	return hook.RunGetSessionHooks(s.opts.getSessionHooks, hctx, final)
}

// ListSessions lists sessions for a user.
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	opt := applyOptions(opts...)
	return s.listSessions(ctx, userKey, opt.EventNum, opt.EventTime)
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
	return s.deleteSessionState(ctx, key)
}

// UpdateAppState updates the app state.
func (s *Service) UpdateAppState(
	ctx context.Context,
	appName string,
	state session.StateMap,
) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	now := time.Now().UTC()
	expiresAt := calculateExpiresAt(now, s.opts.appStateTTL)

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		if err := s.upsertAppState(ctx, appName, k, v, now, expiresAt); err != nil {
			return fmt.Errorf("update app state: %w", err)
		}
	}
	return nil
}

func (s *Service) upsertAppState(
	ctx context.Context,
	appName string,
	key string,
	value []byte,
	now time.Time,
	expiresAt *int64,
) error {
	var id int64
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id FROM %s
WHERE app_name = ? AND key = ? AND deleted_at IS NULL
LIMIT 1`,
			s.tableAppStates,
		),
		appName,
		key,
	).Scan(&id)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, key, value, created_at, updated_at, expires_at, deleted_at
) VALUES (?, ?, ?, ?, ?, ?, NULL)`,
				s.tableAppStates,
			),
			appName,
			key,
			value,
			now.UTC().UnixNano(),
			now.UTC().UnixNano(),
			expiresAt,
		)
		return err
	}

	_, err = s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s
SET value = ?, updated_at = ?, expires_at = ?
WHERE id = ?`,
			s.tableAppStates,
		),
		value,
		now.UTC().UnixNano(),
		expiresAt,
		id,
	)
	return err
}

// ListAppStates lists all app states.
func (s *Service) ListAppStates(
	ctx context.Context,
	appName string,
) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}

	const sqlStmt = `SELECT key, value FROM %s
WHERE app_name = ? AND (expires_at IS NULL OR expires_at > ?)
AND deleted_at IS NULL`
	query := fmt.Sprintf(sqlStmt, s.tableAppStates)

	nowNs := time.Now().UTC().UnixNano()
	rows, err := s.db.QueryContext(ctx, query, appName, nowNs)
	if err != nil {
		return nil, fmt.Errorf("list app states: %w", err)
	}
	defer rows.Close()

	out := make(session.StateMap)
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan app state: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate app states: %w", err)
	}
	return out, nil
}

// DeleteAppState deletes a single app state key.
func (s *Service) DeleteAppState(
	ctx context.Context,
	appName string,
	key string,
) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	if s.opts.softDelete {
		_, err := s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s
SET deleted_at = ?
WHERE app_name = ? AND key = ? AND deleted_at IS NULL`,
				s.tableAppStates,
			),
			time.Now().UTC().UnixNano(),
			appName,
			key,
		)
		if err != nil {
			return fmt.Errorf("delete app state: %w", err)
		}
		return nil
	}

	_, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s WHERE app_name = ? AND key = ?`,
			s.tableAppStates,
		),
		appName,
		key,
	)
	if err != nil {
		return fmt.Errorf("delete app state: %w", err)
	}
	return nil
}
