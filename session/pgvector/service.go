//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

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
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// Compile-time interface checks.
var _ session.Service = (*Service)(nil)
var _ session.TrackService = (*Service)(nil)
var _ session.SearchableService = (*Service)(nil)

var errServiceClosing = errors.New("service is closing")
var errEmbedderRequired = errors.New("pgvector session embedder is required")
var errEmbedderDimensionMismatch = errors.New("pgvector session embedder dimension mismatch")

// SessionState is the state of a session.
type SessionState struct {
	ID        string           `json:"id"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// sessionEventPair holds a session key and event for
// async persistence.
type sessionEventPair struct {
	key   session.Key
	event *event.Event
}

// trackEventPair holds a session key and track event
// for async persistence.
type trackEventPair struct {
	key   session.Key
	event *session.TrackEvent
}

// Service is the pgvector session service with built-in
// vector search capability. It implements all session
// CRUD operations directly and adds embedding-based
// semantic search.
type Service struct {
	opts            ServiceOpts
	pgClient        storage.Client
	eventPairChans  []chan *sessionEventPair
	trackEventChans []chan *trackEventPair
	asyncWorker     *isummary.AsyncSummaryWorker
	cleanupTicker   *time.Ticker
	cleanupDone     chan struct{}
	cleanupOnce     sync.Once
	persistWg       sync.WaitGroup
	indexerWg       sync.WaitGroup
	once            sync.Once

	// Table names with prefix applied.
	tableSessionStates    string
	tableSessionEvents    string
	tableSessionTracks    string
	tableSessionSummaries string
	tableAppStates        string
	tableUserStates       string
}

// buildConnString builds a PostgreSQL connection string.
func buildConnString(opts ServiceOpts) string {
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
	connString := fmt.Sprintf(
		"host=%s port=%d dbname=%s sslmode=%s",
		host, port, database, sslMode,
	)
	if opts.user != "" {
		connString += fmt.Sprintf(" user=%s", opts.user)
	}
	if opts.password != "" {
		connString += fmt.Sprintf(
			" password=%s", opts.password,
		)
	}
	return connString
}

// NewService creates a new pgvector session service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}
	if opts.embedder == nil {
		return nil, errEmbedderRequired
	}
	if err := validateEmbedderDimensions(&opts); err != nil {
		return nil, err
	}

	// Set default cleanup interval if any TTL is
	// configured.
	if opts.cleanupInterval <= 0 {
		if opts.sessionTTL > 0 ||
			opts.appStateTTL > 0 ||
			opts.userStateTTL > 0 {
			opts.cleanupInterval =
				defaultCleanupIntervalSecond
		}
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithExtraOptions(opts.extraOptions...),
	}
	if opts.dsn != "" {
		builderOpts = append(builderOpts,
			storage.WithClientConnString(opts.dsn))
	} else if opts.host != "" {
		builderOpts = append(builderOpts,
			storage.WithClientConnString(
				buildConnString(opts),
			))
	} else if opts.instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetPostgresInstance(
			opts.instanceName,
		); !ok {
			return nil, fmt.Errorf(
				"postgres instance %s not found",
				opts.instanceName,
			)
		}
	} else {
		builderOpts = append(builderOpts,
			storage.WithClientConnString(
				buildConnString(opts),
			))
	}

	pgClient, err := storage.GetClientBuilder()(
		context.Background(), builderOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"create postgres client failed: %w", err,
		)
	}

	s := &Service{
		opts:        opts,
		pgClient:    pgClient,
		cleanupDone: make(chan struct{}),
		tableSessionStates: sqldb.BuildTableNameWithSchema(
			opts.schema, opts.tablePrefix,
			sqldb.TableNameSessionStates,
		),
		tableSessionEvents: sqldb.BuildTableNameWithSchema(
			opts.schema, opts.tablePrefix,
			sqldb.TableNameSessionEvents,
		),
		tableSessionTracks: sqldb.BuildTableNameWithSchema(
			opts.schema, opts.tablePrefix,
			sqldb.TableNameSessionTrackEvents,
		),
		tableSessionSummaries: sqldb.BuildTableNameWithSchema(
			opts.schema, opts.tablePrefix,
			sqldb.TableNameSessionSummaries,
		),
		tableAppStates: sqldb.BuildTableNameWithSchema(
			opts.schema, opts.tablePrefix,
			sqldb.TableNameAppStates,
		),
		tableUserStates: sqldb.BuildTableNameWithSchema(
			opts.schema, opts.tablePrefix,
			sqldb.TableNameUserStates,
		),
	}

	if !opts.skipDBInit {
		if err := s.initDB(context.Background()); err != nil {
			_ = pgClient.Close()
			return nil, err
		}
	}

	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}

	if opts.summarizer != nil &&
		opts.asyncSummaryNum > 0 {
		s.asyncWorker = isummary.NewAsyncSummaryWorker(
			isummary.AsyncSummaryConfig{
				Summarizer:        opts.summarizer,
				AsyncSummaryNum:   opts.asyncSummaryNum,
				SummaryQueueSize:  opts.summaryQueueSize,
				SummaryJobTimeout: opts.summaryJobTimeout,
				SummaryDispatchPolicy: isummary.NewSummaryDispatchPolicy(
					opts.summaryFilterAllowlist,
					opts.shouldCascadeFullSessionSummary(),
				),
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

func validateEmbedderDimensions(opts *ServiceOpts) error {
	if opts == nil || opts.embedder == nil {
		return nil
	}
	dim := opts.embedder.GetDimensions()
	if dim <= 0 || dim == opts.indexDimension {
		return nil
	}
	return fmt.Errorf(
		"%w: embedder=%d configured=%d",
		errEmbedderDimensionMismatch,
		dim,
		opts.indexDimension,
	)
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
		return nil, fmt.Errorf(
			"marshal session failed: %w", err,
		)
	}

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := now.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	// Check if session already exists.
	var sessionExists bool
	var existingExpiresAt sql.NullTime
	err = s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			if rows.Next() {
				sessionExists = true
				return rows.Scan(&existingExpiresAt)
			}
			return nil
		},
		fmt.Sprintf(
			`SELECT expires_at FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND session_id = $3
			AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName, key.UserID, key.SessionID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"check existing session failed: %w", err,
		)
	}

	if sessionExists {
		if !existingExpiresAt.Valid {
			return nil, fmt.Errorf(
				"session already exists and has not expired",
			)
		}
		if existingExpiresAt.Time.After(now) {
			return nil, fmt.Errorf(
				"session already exists and has not expired",
			)
		}
		log.InfofContext(ctx,
			"found expired session "+
				"(app=%s, user=%s, session=%s), "+
				"triggering cleanup",
			key.AppName, key.UserID, key.SessionID,
		)
		s.cleanupExpiredForUser(ctx,
			session.UserKey{
				AppName: key.AppName,
				UserID:  key.UserID,
			},
		)
	}

	_, err = s.pgClient.ExecContext(ctx,
		fmt.Sprintf(
			`INSERT INTO %s
			(app_name, user_id, session_id, state,
			 created_at, updated_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			s.tableSessionStates,
		),
		key.AppName, key.UserID, key.SessionID,
		sessBytes, sessState.CreatedAt,
		sessState.UpdatedAt, expiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"create session failed: %w", err,
		)
	}

	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, fmt.Errorf(
			"list app states failed: %w", err,
		)
	}
	userState, err := s.ListUserStates(ctx,
		session.UserKey{
			AppName: key.AppName,
			UserID:  key.UserID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"list user states failed: %w", err,
		)
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
		_ func() (*session.Session, error),
	) (*session.Session, error) {
		sess, err := s.getSession(
			c.Context, c.Key,
			c.Options.EventNum, c.Options.EventTime,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"pgvector session service get session "+
					"failed: %w", err,
			)
		}
		if sess != nil && s.opts.sessionTTL > 0 {
			if err := s.refreshSessionTTL(
				c.Context, c.Key,
			); err != nil {
				log.WarnfContext(c.Context,
					"failed to refresh session TTL: %v",
					err,
				)
			}
		}
		return sess, nil
	}
	return hook.RunGetSessionHooks(
		s.opts.getSessionHooks, hctx, final,
	)
}

// ListSessions lists all sessions by user scope.
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	sessList, err := s.listSessions(
		ctx, userKey, opt.EventNum, opt.EventTime,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"pgvector session service list sessions "+
				"failed: %w", err,
		)
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
		return fmt.Errorf(
			"pgvector session service delete session "+
				"failed: %w", err,
		)
	}
	return nil
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
	now := time.Now()
	var expiresAt *time.Time
	if s.opts.appStateTTL > 0 {
		t := now.Add(s.opts.appStateTTL)
		expiresAt = &t
	}
	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		_, err := s.pgClient.ExecContext(ctx,
			fmt.Sprintf(
				`INSERT INTO %s
				(app_name, key, value, updated_at,
				 expires_at, deleted_at)
				VALUES ($1, $2, $3, $4, $5, NULL)
				ON CONFLICT (app_name, key)
				WHERE deleted_at IS NULL
				DO UPDATE SET
				  value = EXCLUDED.value,
				  updated_at = EXCLUDED.updated_at,
				  expires_at = EXCLUDED.expires_at`,
				s.tableAppStates,
			),
			appName, k, v, now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf(
				"pgvector session service update app "+
					"state failed: %w", err,
			)
		}
	}
	return nil
}

// ListAppStates gets the app states.
func (s *Service) ListAppStates(
	ctx context.Context, appName string,
) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}
	appStateMap := make(session.StateMap)
	err := s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var key string
				var value []byte
				if err := rows.Scan(
					&key, &value,
				); err != nil {
					return err
				}
				appStateMap[key] = value
			}
			return nil
		},
		fmt.Sprintf(
			`SELECT key, value FROM %s
			WHERE app_name = $1
			AND (expires_at IS NULL
				OR expires_at > NOW() AT TIME ZONE 'localtime')
			AND deleted_at IS NULL`,
			s.tableAppStates,
		),
		appName,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"pgvector session service list app "+
				"states failed: %w", err,
		)
	}
	return appStateMap, nil
}

// DeleteAppState deletes an app state key.
func (s *Service) DeleteAppState(
	ctx context.Context, appName string, key string,
) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}
	var err error
	if s.opts.softDelete {
		_, err = s.pgClient.ExecContext(ctx,
			fmt.Sprintf(
				`UPDATE %s SET deleted_at = $1
				WHERE app_name = $2 AND key = $3
				AND deleted_at IS NULL`,
				s.tableAppStates,
			),
			time.Now(), appName, key,
		)
	} else {
		_, err = s.pgClient.ExecContext(ctx,
			fmt.Sprintf(
				`DELETE FROM %s
				WHERE app_name = $1 AND key = $2`,
				s.tableAppStates,
			),
			appName, key,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"pgvector session service delete app "+
				"state failed: %w", err,
		)
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
	now := time.Now()
	var expiresAt *time.Time
	if s.opts.userStateTTL > 0 {
		t := now.Add(s.opts.userStateTTL)
		expiresAt = &t
	}
	for k, v := range state {
		k = strings.TrimPrefix(
			k, session.StateUserPrefix,
		)
		_, err := s.pgClient.ExecContext(ctx,
			fmt.Sprintf(
				`INSERT INTO %s
				(app_name, user_id, key, value,
				 updated_at, expires_at, deleted_at)
				VALUES ($1, $2, $3, $4, $5, $6, NULL)
				ON CONFLICT (app_name, user_id, key)
				WHERE deleted_at IS NULL
				DO UPDATE SET
				  value = EXCLUDED.value,
				  updated_at = EXCLUDED.updated_at,
				  expires_at = EXCLUDED.expires_at`,
				s.tableUserStates,
			),
			userKey.AppName, userKey.UserID,
			k, v, now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf(
				"pgvector session service update user "+
					"state failed: %w", err,
			)
		}
	}
	return nil
}

// ListUserStates lists user states.
func (s *Service) ListUserStates(
	ctx context.Context, userKey session.UserKey,
) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	userStateMap := make(session.StateMap)
	err := s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var key string
				var value []byte
				if err := rows.Scan(
					&key, &value,
				); err != nil {
					return err
				}
				userStateMap[key] = value
			}
			return nil
		},
		fmt.Sprintf(
			`SELECT key, value FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND (expires_at IS NULL
				OR expires_at > NOW() AT TIME ZONE 'localtime')
			AND deleted_at IS NULL`,
			s.tableUserStates,
		),
		userKey.AppName, userKey.UserID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"pgvector session service list user "+
				"states failed: %w", err,
		)
	}
	return userStateMap, nil
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
	var err error
	if s.opts.softDelete {
		_, err = s.pgClient.ExecContext(ctx,
			fmt.Sprintf(
				`UPDATE %s SET deleted_at = $1
				WHERE app_name = $2
				AND user_id = $3 AND key = $4
				AND deleted_at IS NULL`,
				s.tableUserStates,
			),
			time.Now(),
			userKey.AppName, userKey.UserID, key,
		)
	} else {
		_, err = s.pgClient.ExecContext(ctx,
			fmt.Sprintf(
				`DELETE FROM %s
				WHERE app_name = $1
				AND user_id = $2 AND key = $3`,
				s.tableUserStates,
			),
			userKey.AppName, userKey.UserID, key,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"pgvector session service delete user "+
				"state failed: %w", err,
		)
	}
	return nil
}

// UpdateSessionState updates session-level state
// directly without appending an event.
func (s *Service) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	for k := range state {
		if strings.HasPrefix(
			k, session.StateAppPrefix,
		) {
			return fmt.Errorf(
				"pgvector session service update session "+
					"state failed: %s is not allowed, "+
					"use UpdateAppState instead", k,
			)
		}
		if strings.HasPrefix(
			k, session.StateUserPrefix,
		) {
			return fmt.Errorf(
				"pgvector session service update session "+
					"state failed: %s is not allowed, "+
					"use UpdateUserState instead", k,
			)
		}
	}

	var currentStateBytes []byte
	err := s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			if rows.Next() {
				return rows.Scan(&currentStateBytes)
			}
			return sql.ErrNoRows
		},
		fmt.Sprintf(
			`SELECT state FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND session_id = $3
			AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName, key.UserID, key.SessionID,
	)
	if err == sql.ErrNoRows {
		return fmt.Errorf(
			"pgvector session service update session " +
				"state failed: session not found",
		)
	}
	if err != nil {
		return fmt.Errorf(
			"pgvector session service update session "+
				"state failed: get session state: %w",
			err,
		)
	}

	var sessState SessionState
	if len(currentStateBytes) > 0 {
		if err := json.Unmarshal(
			currentStateBytes, &sessState,
		); err != nil {
			return fmt.Errorf(
				"pgvector session service update session "+
					"state failed: unmarshal state: %w",
				err,
			)
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
		return fmt.Errorf(
			"pgvector session service update session "+
				"state failed: marshal state: %w",
			err,
		)
	}

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := now.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	_, err = s.pgClient.ExecContext(ctx,
		fmt.Sprintf(
			`UPDATE %s SET state = $1,
			 updated_at = $2, expires_at = $3
			WHERE app_name = $4 AND user_id = $5
			AND session_id = $6
			AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		updatedStateBytes, now, expiresAt,
		key.AppName, key.UserID, key.SessionID,
	)
	if err != nil {
		return fmt.Errorf(
			"pgvector session service update session "+
				"state failed: %w", err,
		)
	}
	return nil
}

// AppendEvent appends an event to a session, then
// asynchronously generates and stores the embedding.
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
	final := func(
		c *session.AppendEventContext,
		_ func() error,
	) error {
		return s.appendEventInternal(
			c.Context, c.Session, c.Event, c.Key,
			opts...,
		)
	}
	return hook.RunAppendEventHooks(
		s.opts.appendEventHooks, hctx, final,
	)
}

// appendEventInternal is the internal implementation
// of AppendEvent.
func (s *Service) appendEventInternal(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	key session.Key,
	opts ...session.Option,
) (retErr error) {
	sess.UpdateUserSession(e, opts...)

	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				retErr = handleClosedChannelPanic(
					ctx,
					"pgvector session service append event failed: %v",
					r,
				)
			}
		}()

		index := sess.Hash % len(s.eventPairChans)
		select {
		case s.eventPairChans[index] <- &sessionEventPair{
			key: key, event: e,
		}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	if err := s.addEvent(ctx, key, e); err != nil {
		return fmt.Errorf(
			"pgvector session service append event "+
				"failed: %w", err,
		)
	}
	s.indexEventAfterPersist(sess, e)
	return nil
}

// AppendTrackEvent appends a track event to a session.
func (s *Service) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) (retErr error) {
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	if err := sess.AppendTrackEvent(
		trackEvent, opts...,
	); err != nil {
		return err
	}

	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				retErr = handleClosedChannelPanic(
					ctx,
					"pgvector session service append track event failed: %v",
					r,
				)
			}
		}()

		hKey := fmt.Sprintf(
			"%s:%s:%s:%s",
			key.AppName, key.UserID,
			key.SessionID, trackEvent.Track,
		)
		n := len(s.trackEventChans)
		index := session.HashString(hKey) % n
		select {
		case s.trackEventChans[index] <- &trackEventPair{
			key: key, event: trackEvent,
		}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	if err := s.addTrackEvent(
		ctx, key, trackEvent,
	); err != nil {
		return fmt.Errorf(
			"pgvector session service append track "+
				"event failed: %w", err,
		)
	}
	return nil
}

// Close closes the service.
func (s *Service) Close() error {
	s.once.Do(func() {
		s.stopCleanupRoutine()

		for _, ch := range s.eventPairChans {
			close(ch)
		}
		for _, ch := range s.trackEventChans {
			close(ch)
		}
		s.persistWg.Wait()
		s.indexerWg.Wait()

		if s.asyncWorker != nil {
			s.asyncWorker.Stop()
		}
		if s.pgClient != nil {
			s.pgClient.Close()
		}
	})
	return nil
}

// shouldPersistEvent reports whether the event will be
// stored in `session_events` and can therefore be
// indexed safely.
func shouldPersistEvent(evt *event.Event) bool {
	return evt != nil && evt.Response != nil &&
		!evt.IsPartial && evt.IsValidContent()
}

// extractEventText extracts indexable text and role from
// an event. Returns empty string for events that should
// not be indexed (tool calls, partials, empty content).
func extractEventText(
	evt *event.Event,
) (string, model.Role) {
	if !shouldPersistEvent(evt) {
		return "", ""
	}
	if len(evt.Response.Choices) == 0 {
		return "", ""
	}
	msg := evt.Response.Choices[0].Message
	if msg.Role == model.RoleTool || msg.ToolID != "" {
		return "", ""
	}
	if len(msg.ToolCalls) > 0 {
		return "", ""
	}
	content := msg.Content
	if content == "" && len(msg.ContentParts) > 0 {
		var sb strings.Builder
		for _, p := range msg.ContentParts {
			if p.Text != nil {
				sb.WriteString(*p.Text)
				sb.WriteString(" ")
			}
		}
		content = strings.TrimSpace(sb.String())
	}
	if content == "" {
		return "", ""
	}
	role := msg.Role
	if role == "" {
		role = model.RoleAssistant
	}
	return content, role
}

func (s *Service) buildIndexText(
	sess *session.Session,
	evt *event.Event,
) (string, model.Role) {
	text, role := extractEventText(evt)
	if text == "" {
		return "", ""
	}
	if s.opts.indexTextBuilder != nil {
		text = strings.TrimSpace(
			s.opts.indexTextBuilder(
				sess, evt, text, role,
			),
		)
	}
	if text == "" {
		return "", ""
	}
	return text, role
}

// triggerAsyncIndexEvent detaches indexing work from the
// request context so request cancellation does not skip
// embedding write-back.
func (s *Service) triggerAsyncIndexEvent(
	sess *session.Session,
	evt *event.Event,
) {
	if !shouldPersistEvent(evt) {
		return
	}
	s.indexerWg.Add(1)
	go func() {
		defer s.indexerWg.Done()
		ctx, cancel := context.WithTimeout(
			context.Background(),
			s.opts.embedTimeout,
		)
		defer cancel()
		s.asyncIndexEvent(ctx, sess, evt)
	}()
}

func handleClosedChannelPanic(
	ctx context.Context,
	format string,
	panicValue any,
) error {
	switch v := panicValue.(type) {
	case error:
		if v.Error() == "send on closed channel" {
			log.ErrorfContext(ctx, format, panicValue)
			return errServiceClosing
		}
	case string:
		if v == "send on closed channel" {
			log.ErrorfContext(ctx, format, panicValue)
			return errServiceClosing
		}
	}
	panic(panicValue)
}

func (s *Service) indexEventAfterPersist(
	sess *session.Session,
	evt *event.Event,
) {
	if !shouldPersistEvent(evt) {
		return
	}
	if s.opts.syncIndexing {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			s.opts.embedTimeout,
		)
		defer cancel()
		s.asyncIndexEvent(ctx, sess, evt)
		return
	}
	s.triggerAsyncIndexEvent(sess, evt)
}

// asyncIndexEvent generates embedding and updates the
// matching persisted event row. Non-blocking: errors are
// logged but do not affect the main path.
func (s *Service) asyncIndexEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
) {
	text, role := s.buildIndexText(sess, evt)
	if text == "" {
		return
	}
	if s.opts.embedder == nil {
		return
	}
	emb, err := s.opts.embedder.GetEmbedding(ctx, text)
	if err != nil {
		log.WarnfContext(ctx,
			"pgvector session: embedding failed: %v",
			err,
		)
		return
	}
	if len(emb) == 0 {
		log.WarnfContext(ctx,
			"pgvector session: empty embedding returned",
		)
		return
	}
	if err := s.updateEventEmbedding(
		ctx, sess, evt, text, string(role), emb,
	); err != nil {
		log.WarnfContext(ctx,
			"pgvector session: update embedding "+
				"failed: %v", err,
		)
	}
}

// mergeState merges app and user states into a session.
func mergeState(
	appState, userState session.StateMap,
	sess *session.Session,
) *session.Session {
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
	}
	return sess
}

// applyOptions applies session options.
func applyOptions(
	opts ...session.Option,
) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}

// startCleanupRoutine starts the background cleanup.
func (s *Service) startCleanupRoutine() {
	s.cleanupTicker = time.NewTicker(
		s.opts.cleanupInterval,
	)
	ticker := s.cleanupTicker
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

// stopCleanupRoutine stops the background cleanup.
func (s *Service) stopCleanupRoutine() {
	s.cleanupOnce.Do(func() {
		if s.cleanupTicker != nil {
			close(s.cleanupDone)
			s.cleanupTicker = nil
		}
	})
}

// cleanupExpired removes expired sessions and states.
func (s *Service) cleanupExpired() {
	ctx := context.Background()
	s.cleanupExpiredData(ctx, nil)
}

// cleanupExpiredForUser removes expired sessions for
// a specific user.
func (s *Service) cleanupExpiredForUser(
	ctx context.Context, userKey session.UserKey,
) {
	s.cleanupExpiredData(ctx, &userKey)
}

// cleanupExpiredData is the unified cleanup function.
func (s *Service) cleanupExpiredData(
	ctx context.Context, userKey *session.UserKey,
) {
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

	var validTasks []cleanupTask
	for _, task := range tasks {
		if task.ttl <= 0 {
			continue
		}
		if userKey != nil &&
			task.tableName == s.tableAppStates {
			continue
		}
		validTasks = append(validTasks, task)
	}

	if len(validTasks) > 0 {
		err := s.pgClient.Transaction(ctx,
			func(tx *sql.Tx) error {
				for _, task := range validTasks {
					if s.opts.softDelete {
						if err := s.softDeleteExpiredInTx(
							ctx, tx, task.tableName,
							now, userKey,
						); err != nil {
							return err
						}
					} else {
						if err := s.hardDeleteExpiredInTx(
							ctx, tx, task.tableName,
							now, userKey,
						); err != nil {
							return err
						}
					}
				}
				return nil
			},
		)
		if err != nil {
			log.ErrorfContext(ctx,
				"cleanup expired tables failed: %v", err,
			)
		}
	}
}

// softDeleteExpiredInTx soft-deletes expired rows.
func (s *Service) softDeleteExpiredInTx(
	ctx context.Context,
	tx *sql.Tx,
	tableName string,
	now time.Time,
	userKey *session.UserKey,
) error {
	var query string
	var args []any
	if userKey != nil {
		query = fmt.Sprintf(
			`UPDATE %s SET deleted_at = $1
			WHERE app_name = $2 AND user_id = $3
			AND expires_at IS NOT NULL
			AND expires_at <= $1
			AND deleted_at IS NULL`,
			tableName,
		)
		args = []any{now, userKey.AppName, userKey.UserID}
	} else {
		query = fmt.Sprintf(
			`UPDATE %s SET deleted_at = $1
			WHERE expires_at IS NOT NULL
			AND expires_at <= $1
			AND deleted_at IS NULL`,
			tableName,
		)
		args = []any{now}
	}
	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf(
			"soft delete expired %s: %w",
			tableName, err,
		)
	}
	return nil
}

// hardDeleteExpiredInTx hard-deletes expired rows.
func (s *Service) hardDeleteExpiredInTx(
	ctx context.Context,
	tx *sql.Tx,
	tableName string,
	now time.Time,
	userKey *session.UserKey,
) error {
	var query string
	var args []any
	if userKey != nil {
		query = fmt.Sprintf(
			`DELETE FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND expires_at IS NOT NULL
			AND expires_at <= $3`,
			tableName,
		)
		args = []any{
			userKey.AppName, userKey.UserID, now,
		}
	} else {
		query = fmt.Sprintf(
			`DELETE FROM %s
			WHERE expires_at IS NOT NULL
			AND expires_at <= $1`,
			tableName,
		)
		args = []any{now}
	}
	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf(
			"hard delete expired %s: %w",
			tableName, err,
		)
	}
	return nil
}
