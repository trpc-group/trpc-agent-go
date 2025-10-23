//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package database provides the relational database session service.
// It supports MySQL, PostgreSQL, and other GORM-compatible databases.
package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spaolacci/murmur3"
	"gorm.io/gorm"
	"trpc.group/trpc-go/trpc-agent-go/event"
	isession "trpc.group/trpc-go/trpc-agent-go/internal/session"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/database"
)

var _ session.Service = (*Service)(nil)

const (
	defaultSessionEventLimit = 1000
	defaultTimeout           = 2 * time.Second
	defaultChanBufferSize    = 100
	defaultAsyncPersisterNum = 10
	defaultCleanupInterval   = 5 * time.Minute

	defaultAsyncSummaryNum   = 3
	defaultSummaryQueueSize  = 256
	defaultSummaryJobTimeout = 30 * time.Second
)

// Service is the database session service.
type Service struct {
	opts            ServiceOpts
	db              *gorm.DB
	eventPairChans  []chan *sessionEventPair // channel for session events to persistence
	summaryJobChans []chan *summaryJob       // channel for summary jobs to processing
	cleanupTicker   *time.Ticker
	cleanupDone     chan struct{}
	cleanupOnce     sync.Once
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

// NewService creates a new database session service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := ServiceOpts{
		sessionEventLimit: defaultSessionEventLimit,
		autoCreateTable:   true, // Default: auto create tables if not exist
		asyncPersisterNum: defaultAsyncPersisterNum,
		asyncSummaryNum:   defaultAsyncSummaryNum,
		summaryQueueSize:  defaultSummaryQueueSize,
		summaryJobTimeout: defaultSummaryJobTimeout,
	}
	for _, option := range options {
		option(&opts)
	}

	var db *gorm.DB
	var err error
	builder := storage.GetClientBuilder()

	// if instance name set, and dsn not set, use instance name to create database client
	if opts.dsn == "" && opts.instanceName != "" {
		builderOpts, ok := storage.GetDatabaseInstance(opts.instanceName)
		if !ok {
			return nil, fmt.Errorf("database instance %s not found", opts.instanceName)
		}
		db, err = builder(builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create database client from instance name failed: %w", err)
		}
	} else {
		builderOpts := []storage.ClientBuilderOpt{
			storage.WithClientBuilderDSN(opts.dsn),
		}
		// Add driver type if specified
		if opts.driverType != "" {
			builderOpts = append(builderOpts, storage.WithDriverType(opts.driverType))
		}
		// Add extra options
		if len(opts.extraOptions) > 0 {
			builderOpts = append(builderOpts, storage.WithExtraOptions(opts.extraOptions...))
		}
		db, err = builder(builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create database client from dsn failed: %w", err)
		}
	}

	// init table: check schema, create if needed, and optionally migrate
	if err := initializeTables(db, opts.autoCreateTable, opts.autoMigrate); err != nil {
		return nil, fmt.Errorf("initialize tables failed: %w", err)
	}

	// Set default cleanup interval if any TTL is configured
	if opts.cleanupInterval <= 0 {
		if opts.sessionTTL > 0 || opts.appStateTTL > 0 || opts.userStateTTL > 0 {
			opts.cleanupInterval = defaultCleanupInterval
		}
	}

	s := &Service{
		opts:        opts,
		db:          db,
		cleanupDone: make(chan struct{}),
	}
	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}
	if opts.cleanupInterval > 0 {
		s.startCleanupRoutine()
	}
	// Always start async summary workers by default.
	s.startAsyncSummaryWorker()
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

	// Prepare state map for storage
	stateMap := make(session.StateMap)
	for k, v := range state {
		stateMap[k] = v
	}

	// Marshal session state
	stateBytes, err := json.Marshal(stateMap)
	if err != nil {
		return nil, fmt.Errorf("marshal session state failed: %w", err)
	}

	// Calculate expiration time
	var expiresAt time.Time
	if s.opts.sessionTTL > 0 {
		expiresAt = now.Add(s.opts.sessionTTL)
	}

	// Store session state in transaction
	sessionModel := &sessionStateModel{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		State:     stateBytes,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: expiresAt,
	}
	if err := s.db.WithContext(ctx).Create(sessionModel).Error; err != nil {
		return nil, fmt.Errorf("create session state failed: %w", err)
	}

	// Query app states (outside transaction for better concurrency)
	var appStates []appStateModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND (expires_at IS NULL OR expires_at > ?)", key.AppName, now).
		Find(&appStates).Error; err != nil {
		return nil, fmt.Errorf("query app states failed: %w", err)
	}

	// Query user states (outside transaction for better concurrency)
	var userStates []userStateModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND user_id = ? AND (expires_at IS NULL OR expires_at > ?)",
			key.AppName, key.UserID, now).Find(&userStates).Error; err != nil {
		return nil, fmt.Errorf("query user states failed: %w", err)
	}

	// Process app/user state
	appState := processAppStates(appStates)
	userState := processUserStates(userStates)

	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     stateMap,
		Events:    []event.Event{},
		UpdatedAt: now,
		CreatedAt: now,
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
		return nil, fmt.Errorf("database session service get session state failed: %w", err)
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
		return nil, fmt.Errorf("database session service get session list failed: %w", err)
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
		return fmt.Errorf("database session service delete session state failed: %w", err)
	}
	return nil
}

// UpdateAppState updates the state by target scope and key.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	now := time.Now()
	var expiresAt time.Time
	if s.opts.appStateTTL > 0 {
		expiresAt = now.Add(s.opts.appStateTTL)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for k, v := range state {
			k = strings.TrimPrefix(k, session.StateAppPrefix)
			appState := &appStateModel{
				AppName:   appName,
				StateKey:  k,
				Value:     v,
				UpdatedAt: now,
				ExpiresAt: expiresAt,
			}
			// Use ON DUPLICATE KEY UPDATE
			if err := tx.Where("app_name = ? AND state_key = ?", appName, k).
				Assign(map[string]interface{}{
					"value":      v,
					"updated_at": now,
					"expires_at": expiresAt,
				}).
				FirstOrCreate(appState).Error; err != nil {
				return fmt.Errorf("update app state failed: %w", err)
			}
		}
		return nil
	})
}

// ListAppStates gets the app states.
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}

	var appStates []appStateModel
	now := time.Now()
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND (expires_at IS NULL OR expires_at > ?)", appName, now).
		Find(&appStates).Error; err != nil {
		return nil, fmt.Errorf("database session service list app states failed: %w", err)
	}

	return processAppStates(appStates), nil
}

// DeleteAppState deletes the state by target scope and key.
func (s *Service) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND state_key = ?", appName, key).
		Delete(&appStateModel{}).Error; err != nil {
		return fmt.Errorf("database session service delete app state failed: %w", err)
	}
	return nil
}

// UpdateUserState updates the state by target scope and key.
func (s *Service) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	now := time.Now()
	var expiresAt time.Time
	if s.opts.userStateTTL > 0 {
		expiresAt = now.Add(s.opts.userStateTTL)
	}

	// Validate state keys
	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf("database session service update user state failed: %s is not allowed", k)
		}
		if strings.HasPrefix(k, session.StateTempPrefix) {
			return fmt.Errorf("database session service update user state failed: %s is not allowed", k)
		}
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for k, v := range state {
			k = strings.TrimPrefix(k, session.StateUserPrefix)
			userState := &userStateModel{
				AppName:   userKey.AppName,
				UserID:    userKey.UserID,
				StateKey:  k,
				Value:     v,
				UpdatedAt: now,
				ExpiresAt: expiresAt,
			}
			// Use ON DUPLICATE KEY UPDATE
			if err := tx.Where("app_name = ? AND user_id = ? AND state_key = ?", userKey.AppName, userKey.UserID, k).
				Assign(map[string]interface{}{
					"value":      v,
					"updated_at": now,
					"expires_at": expiresAt,
				}).
				FirstOrCreate(userState).Error; err != nil {
				return fmt.Errorf("update user state failed: %w", err)
			}
		}
		return nil
	})
}

// ListUserStates lists the state by target scope and key.
func (s *Service) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	var userStates []userStateModel
	now := time.Now()
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND user_id = ? AND (expires_at IS NULL OR expires_at > ?)",
			userKey.AppName, userKey.UserID, now).
		Find(&userStates).Error; err != nil {
		return nil, fmt.Errorf("database session service list user states failed: %w", err)
	}

	return processUserStates(userStates), nil
}

// DeleteUserState deletes the state by target scope and key.
func (s *Service) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND user_id = ? AND state_key = ?", userKey.AppName, userKey.UserID, key).
		Delete(&userStateModel{}).Error; err != nil {
		return fmt.Errorf("database session service delete user state failed: %w", err)
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

	// persist event to database asynchronously
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && err.Error() == "send on closed channel" {
					log.Errorf("database session service append event failed: %v", r)
					return
				}
				panic(r)
			}
		}()

		// Hash-based distribution
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
		return fmt.Errorf("database session service append event failed: %w", err)
	}

	return nil
}

// Close closes the service.
func (s *Service) Close() error {
	s.once.Do(func() {
		// Close database connection
		if s.db != nil {
			if sqlDB, err := s.db.DB(); err == nil {
				sqlDB.Close()
			}
		}

		// Stop cleanup routine
		s.stopCleanupRoutine()

		// Close async persist channels
		for _, ch := range s.eventPairChans {
			close(ch)
		}

		// Close summary channels
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
	now := time.Now()

	var sessionModel sessionStateModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND user_id = ? AND session_id = ? AND (expires_at IS NULL OR expires_at > ?)",
			key.AppName, key.UserID, key.SessionID, now).
		First(&sessionModel).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get session state failed: %w", err)
	}

	var stateMap session.StateMap
	if err := json.Unmarshal(sessionModel.State, &stateMap); err != nil {
		return nil, fmt.Errorf("unmarshal session state failed: %w", err)
	}
	var appStates []appStateModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND (expires_at IS NULL OR expires_at > ?)", key.AppName, now).
		Find(&appStates).Error; err != nil {
		return nil, fmt.Errorf("query app states failed: %w", err)
	}
	var userStates []userStateModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND user_id = ? AND (expires_at IS NULL OR expires_at > ?)",
			key.AppName, key.UserID, now).
		Find(&userStates).Error; err != nil {
		return nil, fmt.Errorf("query user states failed: %w", err)
	}

	// query events
	events, err := s.getEventsList(ctx, []session.Key{key}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}

	if len(events) == 0 {
		events = make([][]event.Event, 1)
	}

	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     stateMap,
		Events:    events[0],
		UpdatedAt: sessionModel.UpdatedAt,
		CreatedAt: sessionModel.CreatedAt,
	}

	// query summaries if there are events
	if len(sess.Events) > 0 {
		var summaryModels []sessionSummaryModel
		if err := s.db.WithContext(ctx).
			Where("app_name = ? AND user_id = ? AND session_id = ? AND (expires_at IS NULL OR expires_at > ?)",
				key.AppName, key.UserID, key.SessionID, now).
			Find(&summaryModels).Error; err == nil && len(summaryModels) > 0 {
			summaries := make(map[string]*session.Summary)
			for _, sm := range summaryModels {
				var summary session.Summary
				if err := json.Unmarshal(sm.Summary, &summary); err == nil {
					summaries[sm.FilterKey] = &summary
				}
			}
			if len(summaries) > 0 {
				sess.Summaries = summaries
			}
		}
	}

	// refresh TTL if configured
	if s.opts.sessionTTL > 0 {
		expiresAt := now.Add(s.opts.sessionTTL)
		s.db.WithContext(ctx).Model(&sessionStateModel{}).
			Where("app_name = ? AND user_id = ? AND session_id = ?", key.AppName, key.UserID, key.SessionID).
			Update("expires_at", expiresAt)
	}

	// filter events to ensure they start with RoleUser
	isession.EnsureEventStartWithUser(sess)
	appState := processAppStates(appStates)
	userState := processUserStates(userStates)
	return mergeState(appState, userState, sess), nil
}

func (s *Service) listSessions(
	ctx context.Context,
	userKey session.UserKey,
	limit int,
	afterTime time.Time,
) ([]*session.Session, error) {
	now := time.Now()
	// query session states
	var sessionModels []sessionStateModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND user_id = ? AND (expires_at IS NULL OR expires_at > ?)",
			userKey.AppName, userKey.UserID, now).
		Find(&sessionModels).Error; err != nil {
		return nil, fmt.Errorf("get session states failed: %w", err)
	}

	if len(sessionModels) == 0 {
		return []*session.Session{}, nil
	}

	// query app states
	var appStates []appStateModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND (expires_at IS NULL OR expires_at > ?)", userKey.AppName, now).
		Find(&appStates).Error; err != nil {
		return nil, fmt.Errorf("query app states failed: %w", err)
	}

	// query user states
	var userStates []userStateModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND user_id = ? AND (expires_at IS NULL OR expires_at > ?)",
			userKey.AppName, userKey.UserID, now).
		Find(&userStates).Error; err != nil {
		return nil, fmt.Errorf("query user states failed: %w", err)
	}

	// process app and user states
	appState := processAppStates(appStates)
	userState := processUserStates(userStates)

	// query events list
	sessionKeys := make([]session.Key, 0, len(sessionModels))
	for _, sm := range sessionModels {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			SessionID: sm.SessionID,
		})
	}
	events, err := s.getEventsList(ctx, sessionKeys, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}

	sessList := make([]*session.Session, 0, len(sessionModels))
	for i, sm := range sessionModels {
		var stateMap session.StateMap
		if err := json.Unmarshal(sm.State, &stateMap); err != nil {
			return nil, fmt.Errorf("unmarshal session state failed: %w", err)
		}

		sess := &session.Session{
			ID:        sm.SessionID,
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			State:     stateMap,
			Events:    events[i],
			UpdatedAt: sm.UpdatedAt,
			CreatedAt: sm.CreatedAt,
		}

		// filter events to ensure they start with role user
		isession.EnsureEventStartWithUser(sess)
		sessList = append(sessList, mergeState(appState, userState, sess))
	}

	return sessList, nil
}

func (s *Service) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	limit int,
	afterTime time.Time,
) ([][]event.Event, error) {
	sessEventsList := make([][]event.Event, len(sessionKeys))

	for i, key := range sessionKeys {
		query := s.db.WithContext(ctx).
			Where("app_name = ? AND user_id = ? AND session_id = ? AND timestamp >= ?",
				key.AppName, key.UserID, key.SessionID, afterTime).
			Order("timestamp DESC")

		if limit > 0 {
			query = query.Limit(limit)
		}

		var eventModels []sessionEventModel
		if err := query.Find(&eventModels).Error; err != nil {
			return nil, fmt.Errorf("get events failed: %w", err)
		}

		events := make([]event.Event, 0, len(eventModels))
		for _, em := range eventModels {
			var evt event.Event
			if err := json.Unmarshal(em.EventData, &evt); err != nil {
				return nil, fmt.Errorf("unmarshal event failed: %w", err)
			}
			events = append(events, evt)
		}

		// reverse events to get oldest first order
		if len(events) > 1 {
			for j, k := 0, len(events)-1; j < k; j, k = j+1, k-1 {
				events[j], events[k] = events[k], events[j]
			}
		}
		sessEventsList[i] = events
	}

	return sessEventsList, nil
}

func (s *Service) addEvent(ctx context.Context, key session.Key, event *event.Event) error {
	now := time.Now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// update session state
		var sessionModel sessionStateModel
		if err := tx.Where("app_name = ? AND user_id = ? AND session_id = ?",
			key.AppName, key.UserID, key.SessionID).
			First(&sessionModel).Error; err != nil {
			return fmt.Errorf("get session state failed: %w", err)
		}
		var stateMap session.StateMap
		if err := json.Unmarshal(sessionModel.State, &stateMap); err != nil {
			return fmt.Errorf("unmarshal session state failed: %w", err)
		}

		// apply event state delta
		isession.ApplyEventStateDeltaMap(stateMap, event)
		updatedStateBytes, err := json.Marshal(stateMap)
		if err != nil {
			return fmt.Errorf("marshal session state failed: %w", err)
		}
		sessionModel.State = updatedStateBytes
		sessionModel.UpdatedAt = now
		if s.opts.sessionTTL > 0 {
			sessionModel.ExpiresAt = now.Add(s.opts.sessionTTL)
		}

		if err := tx.Save(&sessionModel).Error; err != nil {
			return fmt.Errorf("update session state failed: %w", err)
		}

		// Add event if it has response and is not partial
		if event.Response != nil && !event.IsPartial && event.IsValidContent() {
			eventBytes, err := json.Marshal(event)
			if err != nil {
				return fmt.Errorf("marshal event failed: %w", err)
			}

			var expiresAt time.Time
			if s.opts.sessionTTL > 0 {
				expiresAt = now.Add(s.opts.sessionTTL)
			}

			eventModel := &sessionEventModel{
				AppName:   key.AppName,
				UserID:    key.UserID,
				SessionID: key.SessionID,
				EventData: eventBytes,
				Timestamp: event.Timestamp,
				CreatedAt: now,
				ExpiresAt: expiresAt,
			}

			if err := tx.Create(eventModel).Error; err != nil {
				return fmt.Errorf("create event failed: %w", err)
			}

			// Enforce event limit
			if s.opts.sessionEventLimit > 0 {
				var count int64
				if err := tx.Model(&sessionEventModel{}).
					Where("app_name = ? AND user_id = ? AND session_id = ?",
						key.AppName, key.UserID, key.SessionID).
					Count(&count).Error; err != nil {
					return fmt.Errorf("count events failed: %w", err)
				}

				if count > int64(s.opts.sessionEventLimit) {
					// Delete oldest events
					if err := tx.Where("app_name = ? AND user_id = ? AND session_id = ?",
						key.AppName, key.UserID, key.SessionID).
						Order("timestamp ASC").
						Limit(int(count - int64(s.opts.sessionEventLimit))).
						Delete(&sessionEventModel{}).Error; err != nil {
						return fmt.Errorf("delete old events failed: %w", err)
					}
				}
			}
		}

		return nil
	})
}

func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete session state
		if err := tx.Where("app_name = ? AND user_id = ? AND session_id = ?",
			key.AppName, key.UserID, key.SessionID).
			Delete(&sessionStateModel{}).Error; err != nil {
			return fmt.Errorf("delete session state failed: %w", err)
		}

		// Delete session events
		if err := tx.Where("app_name = ? AND user_id = ? AND session_id = ?",
			key.AppName, key.UserID, key.SessionID).
			Delete(&sessionEventModel{}).Error; err != nil {
			return fmt.Errorf("delete session events failed: %w", err)
		}

		// Delete session summaries
		if err := tx.Where("app_name = ? AND user_id = ? AND session_id = ?",
			key.AppName, key.UserID, key.SessionID).
			Delete(&sessionSummaryModel{}).Error; err != nil {
			return fmt.Errorf("delete session summaries failed: %w", err)
		}

		return nil
	})
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
			for eventPair := range eventPairChan {
				ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
				log.Debugf("Session persistence queue monitoring: channel capacity: %d, current length: %d, session key:%s:%s:%s",
					cap(eventPairChan), len(eventPairChan), eventPair.key.AppName, eventPair.key.UserID, eventPair.key.SessionID)
				if err := s.addEvent(ctx, eventPair.key, eventPair.event); err != nil {
					log.Errorf("database session service persistence event failed: %w", err)
				}
				cancel()
			}
		}(eventPairChan)
	}
}

// startCleanupRoutine starts the background cleanup routine.
func (s *Service) startCleanupRoutine() {
	s.cleanupTicker = time.NewTicker(s.opts.cleanupInterval)
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

// stopCleanupRoutine stops the background cleanup routine.
func (s *Service) stopCleanupRoutine() {
	s.cleanupOnce.Do(func() {
		if s.cleanupTicker != nil {
			close(s.cleanupDone)
			s.cleanupTicker = nil
		}
	})
}

// cleanupExpired removes all expired sessions and states.
func (s *Service) cleanupExpired() {
	ctx := context.Background()
	now := time.Now()

	// Clean expired session states
	s.db.WithContext(ctx).Where("expires_at IS NOT NULL AND expires_at <= ?", now).
		Delete(&sessionStateModel{})

	// Clean expired session events
	s.db.WithContext(ctx).Where("expires_at IS NOT NULL AND expires_at <= ?", now).
		Delete(&sessionEventModel{})

	// Clean expired session summaries
	s.db.WithContext(ctx).Where("expires_at IS NOT NULL AND expires_at <= ?", now).
		Delete(&sessionSummaryModel{})

	// Clean expired app states
	s.db.WithContext(ctx).Where("expires_at IS NOT NULL AND expires_at <= ?", now).
		Delete(&appStateModel{})

	// Clean expired user states
	s.db.WithContext(ctx).Where("expires_at IS NOT NULL AND expires_at <= ?", now).
		Delete(&userStateModel{})
}

func processAppStates(appStates []appStateModel) session.StateMap {
	stateMap := make(session.StateMap)
	for _, as := range appStates {
		stateMap[as.StateKey] = as.Value
	}
	return stateMap
}

func processUserStates(userStates []userStateModel) session.StateMap {
	stateMap := make(session.StateMap)
	for _, us := range userStates {
		stateMap[us.StateKey] = us.Value
	}
	return stateMap
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
