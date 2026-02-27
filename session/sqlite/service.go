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
	"trpc.group/trpc-go/trpc-agent-go/log"
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

	if opts.summarizer != nil && opts.asyncSummaryNum > 0 {
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

		if sess != nil && s.opts.sessionTTL > 0 {
			if err := s.refreshSessionTTL(c.Context, c.Key); err != nil {
				log.WarnfContext(
					c.Context,
					"failed to refresh session TTL: %v",
					err,
				)
			}
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

// UpdateUserState updates user state.
func (s *Service) UpdateUserState(
	ctx context.Context,
	userKey session.UserKey,
	state session.StateMap,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	now := time.Now().UTC()
	expiresAt := calculateExpiresAt(now, s.opts.userStateTTL)

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		if err := s.upsertUserState(
			ctx,
			userKey.AppName,
			userKey.UserID,
			k,
			v,
			now,
			expiresAt,
		); err != nil {
			return fmt.Errorf("update user state: %w", err)
		}
	}
	return nil
}

func (s *Service) upsertUserState(
	ctx context.Context,
	appName string,
	userID string,
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
WHERE app_name = ? AND user_id = ? AND key = ?
AND deleted_at IS NULL
LIMIT 1`,
			s.tableUserStates,
		),
		appName,
		userID,
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
  app_name, user_id, key, value, created_at, updated_at, expires_at,
  deleted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`,
				s.tableUserStates,
			),
			appName,
			userID,
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
			s.tableUserStates,
		),
		value,
		now.UTC().UnixNano(),
		expiresAt,
		id,
	)
	return err
}

// ListUserStates lists user states.
func (s *Service) ListUserStates(
	ctx context.Context,
	userKey session.UserKey,
) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	const sqlStmt = `SELECT key, value FROM %s
WHERE app_name = ? AND user_id = ?
AND (expires_at IS NULL OR expires_at > ?)
AND deleted_at IS NULL`
	query := fmt.Sprintf(sqlStmt, s.tableUserStates)

	rows, err := s.db.QueryContext(
		ctx,
		query,
		userKey.AppName,
		userKey.UserID,
		time.Now().UTC().UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("list user states: %w", err)
	}
	defer rows.Close()

	out := make(session.StateMap)
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan user state: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user states: %w", err)
	}
	return out, nil
}

// DeleteUserState deletes a user state key.
func (s *Service) DeleteUserState(
	ctx context.Context,
	userKey session.UserKey,
	key string,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
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
WHERE app_name = ? AND user_id = ? AND key = ?
AND deleted_at IS NULL`,
				s.tableUserStates,
			),
			time.Now().UTC().UnixNano(),
			userKey.AppName,
			userKey.UserID,
			key,
		)
		if err != nil {
			return fmt.Errorf("delete user state: %w", err)
		}
		return nil
	}

	_, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s WHERE app_name = ? AND user_id = ? AND key = ?`,
			s.tableUserStates,
		),
		userKey.AppName,
		userKey.UserID,
		key,
	)
	if err != nil {
		return fmt.Errorf("delete user state: %w", err)
	}
	return nil
}

// UpdateSessionState updates the session-level state without appending an
// event. Keys with app: or user: prefixes are not allowed.
func (s *Service) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf(
				"%s is not allowed, use UpdateAppState instead",
				k,
			)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf(
				"%s is not allowed, use UpdateUserState instead",
				k,
			)
		}
	}

	var current []byte
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT state FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("session not found")
	}
	if err != nil {
		return fmt.Errorf("get session state: %w", err)
	}

	var sessState SessionState
	if len(current) > 0 {
		if err := json.Unmarshal(current, &sessState); err != nil {
			return fmt.Errorf("unmarshal state: %w", err)
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
		copied := make([]byte, len(v))
		copy(copied, v)
		sessState.State[k] = copied
	}

	now := time.Now().UTC()
	sessState.UpdatedAt = now
	updatedBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	expiresAt := calculateExpiresAt(now, s.opts.sessionTTL)

	_, err = s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s
SET state = ?, updated_at = ?, expires_at = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		updatedBytes,
		now.UTC().UnixNano(),
		expiresAt,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
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
	if sess == nil {
		return session.ErrNilSession
	}
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
		return s.appendEventInternal(
			c.Context,
			c.Session,
			c.Event,
			c.Key,
			opts...,
		)
	}
	return hook.RunAppendEventHooks(s.opts.appendEventHooks, hctx, final)
}

func (s *Service) appendEventInternal(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	key session.Key,
	opts ...session.Option,
) error {
	sess.UpdateUserSession(e, opts...)

	if s.opts.enableAsyncPersist {
		return s.enqueueEventPersist(ctx, sess, key, e)
	}

	if err := s.addEvent(ctx, key, e); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

func (s *Service) enqueueEventPersist(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *event.Event,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok &&
				e.Error() == "send on closed channel" {
				log.ErrorfContext(
					ctx,
					"async persist event: %v",
					r,
				)
				err = nil
				return
			}
			panic(r)
		}
	}()

	index := sess.Hash % len(s.eventPairChans)
	select {
	case s.eventPairChans[index] <- &sessionEventPair{key: key, event: e}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AppendTrackEvent appends a track event to a session.
func (s *Service) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}

	if s.opts.enableAsyncPersist {
		return s.enqueueTrackPersist(ctx, sess, key, trackEvent)
	}

	if err := s.addTrackEvent(ctx, key, trackEvent); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}
	return nil
}

func (s *Service) enqueueTrackPersist(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *session.TrackEvent,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok &&
				e.Error() == "send on closed channel" {
				log.ErrorfContext(
					ctx,
					"async persist track event: %v",
					r,
				)
				err = nil
				return
			}
			panic(r)
		}
	}()

	index := sess.Hash % len(s.trackEventChans)
	select {
	case s.trackEventChans[index] <- &trackEventPair{key: key, event: e}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	s.trackEventChans = make([]chan *trackEventPair, persisterNum)

	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(
			chan *sessionEventPair,
			defaultChanBufferSize,
		)
		s.trackEventChans[i] = make(
			chan *trackEventPair,
			defaultChanBufferSize,
		)
	}

	s.persistWg.Add(persisterNum * 2)

	for _, ch := range s.eventPairChans {
		go func(ch chan *sessionEventPair) {
			defer s.persistWg.Done()
			for pair := range ch {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				if err := s.addEvent(ctx, pair.key, pair.event); err != nil {
					log.ErrorfContext(
						ctx,
						"async persist event: %v",
						err,
					)
				}
				cancel()
			}
		}(ch)
	}

	for _, ch := range s.trackEventChans {
		go func(ch chan *trackEventPair) {
			defer s.persistWg.Done()
			for pair := range ch {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				if err := s.addTrackEvent(
					ctx,
					pair.key,
					pair.event,
				); err != nil {
					log.ErrorfContext(
						ctx,
						"async persist track event: %v",
						err,
					)
				}
				cancel()
			}
		}(ch)
	}
}

func (s *Service) startCleanupRoutine() {
	interval := s.opts.cleanupInterval
	if interval <= 0 {
		return
	}

	s.cleanupTicker = time.NewTicker(interval)
	go func() {
		log.InfofContext(
			context.Background(),
			"started cleanup routine for sqlite session service "+
				"(interval: %v)",
			interval,
		)
		for {
			select {
			case <-s.cleanupTicker.C:
				ctx, cancel := context.WithTimeout(
					context.Background(),
					5*time.Minute,
				)
				s.cleanupExpiredData(ctx)
				cancel()
			case <-s.cleanupDone:
				log.InfoContext(
					context.Background(),
					"cleanup routine stopped for sqlite session "+
						"service",
				)
				return
			}
		}
	}()
}

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

func (s *Service) cleanupExpiredData(ctx context.Context) {
	now := time.Now().UTC()
	if s.opts.sessionTTL > 0 {
		s.cleanupExpiredSessions(ctx, now)
	}
	if s.opts.appStateTTL > 0 {
		s.cleanupExpiredAppStates(ctx, now)
	}
	if s.opts.userStateTTL > 0 {
		s.cleanupExpiredUserStates(ctx, now)
	}
}

func (s *Service) cleanupExpiredSessions(ctx context.Context, now time.Time) {
	nowNs := now.UTC().UnixNano()
	if s.opts.softDelete {
		s.softDeleteExpiredSessions(ctx, nowNs)
		return
	}
	s.hardDeleteExpiredSessions(ctx, nowNs)
}

func (s *Service) softDeleteExpiredSessions(
	ctx context.Context,
	nowNs int64,
) {
	const whereExpired = `expires_at IS NOT NULL AND expires_at <= ?`

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.ErrorfContext(ctx, "begin tx: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.softDeleteExpiredSessionsTx(
		ctx,
		tx,
		nowNs,
		whereExpired,
	); err != nil {
		log.ErrorfContext(ctx, "cleanup expired sessions: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.ErrorfContext(ctx, "commit cleanup: %v", err)
		return
	}
}

func (s *Service) softDeleteExpiredSessionsTx(
	ctx context.Context,
	tx *sql.Tx,
	nowNs int64,
	whereExpired string,
) error {
	args := []any{nowNs, nowNs}

	_, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET deleted_at = ?
WHERE %s AND deleted_at IS NULL`,
			s.tableSessionStates,
			whereExpired,
		),
		args...,
	)
	if err != nil {
		return err
	}

	if err := s.softDeleteExpiredBySession(ctx, tx, s.tableSessionEvents,
		nowNs, whereExpired); err != nil {
		return err
	}
	if err := s.softDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionTracks,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}
	if err := s.softDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionSummaries,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}
	return nil
}

func (s *Service) softDeleteExpiredBySession(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	nowNs int64,
	whereExpired string,
) error {
	_, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET deleted_at = ?
WHERE deleted_at IS NULL AND EXISTS (
  SELECT 1 FROM %s st
  WHERE st.app_name = %s.app_name
    AND st.user_id = %s.user_id
    AND st.session_id = %s.session_id
    AND %s
)`,
			table,
			s.tableSessionStates,
			table,
			table,
			table,
			whereExpired,
		),
		nowNs,
		nowNs,
	)
	return err
}

func (s *Service) hardDeleteExpiredSessions(
	ctx context.Context,
	nowNs int64,
) {
	const whereExpired = `expires_at IS NOT NULL AND expires_at <= ?`

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.ErrorfContext(ctx, "begin tx: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.hardDeleteExpiredSessionsTx(
		ctx,
		tx,
		nowNs,
		whereExpired,
	); err != nil {
		log.ErrorfContext(ctx, "cleanup expired sessions: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.ErrorfContext(ctx, "commit cleanup: %v", err)
		return
	}
}

func (s *Service) hardDeleteExpiredSessionsTx(
	ctx context.Context,
	tx *sql.Tx,
	nowNs int64,
	whereExpired string,
) error {
	if err := s.hardDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionEvents,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}
	if err := s.hardDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionTracks,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}
	if err := s.hardDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionSummaries,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}

	_, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s WHERE %s`,
			s.tableSessionStates,
			whereExpired,
		),
		nowNs,
	)
	return err
}

func (s *Service) hardDeleteExpiredBySession(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	nowNs int64,
	whereExpired string,
) error {
	_, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
WHERE EXISTS (
  SELECT 1 FROM %s st
  WHERE st.app_name = %s.app_name
    AND st.user_id = %s.user_id
    AND st.session_id = %s.session_id
    AND %s
)`,
			table,
			s.tableSessionStates,
			table,
			table,
			table,
			whereExpired,
		),
		nowNs,
	)
	return err
}

func (s *Service) cleanupExpiredAppStates(
	ctx context.Context,
	now time.Time,
) {
	nowNs := now.UTC().UnixNano()
	if s.opts.softDelete {
		_, err := s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET deleted_at = ?
WHERE expires_at IS NOT NULL AND expires_at <= ?
AND deleted_at IS NULL`,
				s.tableAppStates,
			),
			nowNs,
			nowNs,
		)
		if err != nil {
			log.ErrorfContext(ctx, "cleanup app states: %v", err)
		}
		return
	}

	_, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
WHERE expires_at IS NOT NULL AND expires_at <= ?`,
			s.tableAppStates,
		),
		nowNs,
	)
	if err != nil {
		log.ErrorfContext(ctx, "cleanup app states: %v", err)
	}
}

func (s *Service) cleanupExpiredUserStates(
	ctx context.Context,
	now time.Time,
) {
	nowNs := now.UTC().UnixNano()
	if s.opts.softDelete {
		_, err := s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET deleted_at = ?
WHERE expires_at IS NOT NULL AND expires_at <= ?
AND deleted_at IS NULL`,
				s.tableUserStates,
			),
			nowNs,
			nowNs,
		)
		if err != nil {
			log.ErrorfContext(ctx, "cleanup user states: %v", err)
		}
		return
	}

	_, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
WHERE expires_at IS NOT NULL AND expires_at <= ?`,
			s.tableUserStates,
		),
		nowNs,
	)
	if err != nil {
		log.ErrorfContext(ctx, "cleanup user states: %v", err)
	}
}

func mergeState(
	appState session.StateMap,
	userState session.StateMap,
	sess *session.Session,
) *session.Session {
	if sess == nil {
		return nil
	}
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
	}
	return sess
}
