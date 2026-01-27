//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redisv2 provides the redis session service with improved data structure.
// V2 uses separate Hash for event data and indexes, supporting efficient modification and deletion.
// All keys use hash tags to ensure Cluster compatibility.
package redisv2

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
)

// Service is the redis session service V2.
// Storage structure:
//   - meta:{app:user:sess}           -> String (session metadata JSON)
//   - evtdata:{app:user:sess}        -> Hash (eventID -> eventJSON)
//   - evtidx:time:{app:user:sess}    -> ZSet (timestamp -> eventID)
//   - evtidx:req:{app:user:sess}     -> Hash (requestID -> eventIDs JSON array)
//   - appstate:{appName}             -> Hash (key -> value)
//   - userstate:{appName}:{userID}   -> Hash (key -> value)
type Service struct {
	opts        serviceOpts
	redisClient redis.UniversalClient
	once        sync.Once
}

// NewService creates a new Service instance.
func NewService(opts ...Option) (*Service, error) {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderURL(o.url),
		storage.WithExtraOptions(o.extraOptions...),
	}
	if o.url == "" && o.instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetRedisInstance(o.instanceName); !ok {
			return nil, fmt.Errorf("redis instance %s not found", o.instanceName)
		}
	}

	redisClient, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create redis client failed: %w", err)
	}

	return &Service{opts: o, redisClient: redisClient}, nil
}

// Close closes the service.
func (s *Service) Close() error {
	s.once.Do(func() {
		if s.redisClient != nil {
			s.redisClient.Close()
		}
	})
	return nil
}

// sessionMeta is the session metadata structure.
type sessionMeta struct {
	ID        string           `json:"id"`
	AppName   string           `json:"appName"`
	UserID    string           `json:"userID"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// CreateSession creates a new session.
func (s *Service) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	if key.SessionID == "" {
		key.SessionID = uuid.New().String()
	}

	now := time.Now()
	meta := sessionMeta{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     state,
		CreatedAt: now,
		UpdatedAt: now,
	}

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal session meta: %w", err)
	}

	if err := s.redisClient.Set(ctx, metaKey(key), metaJSON, s.opts.sessionTTL).Err(); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sess.State = state
	sess.CreatedAt = now
	sess.UpdatedAt = now
	return sess, nil
}

// GetSession gets a session.
func (s *Service) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	var opts session.Options
	for _, opt := range options {
		opt(&opts)
	}

	metaJSON, err := s.redisClient.Get(ctx, metaKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // session not found
		}
		return nil, fmt.Errorf("get session meta: %w", err)
	}

	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal session meta: %w", err)
	}

	sess := session.NewSession(meta.AppName, meta.UserID, meta.ID)
	sess.State = meta.State
	sess.CreatedAt = meta.CreatedAt
	sess.UpdatedAt = meta.UpdatedAt

	// Load events
	limit := int64(-1)
	if opts.EventNum > 0 {
		limit = int64(opts.EventNum)
	}

	result, err := luaLoadEvents.Run(ctx, s.redisClient,
		[]string{eventDataKey(key), eventTimeIndexKey(key)},
		0, limit,
	).StringSlice()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("load events: %w", err)
	}

	for _, evtJSON := range result {
		var evt event.Event
		if err := json.Unmarshal([]byte(evtJSON), &evt); err != nil {
			continue
		}
		sess.Events = append(sess.Events, evt)
	}

	return sess, nil
}

// DeleteSession deletes a session.
func (s *Service) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	keys := []string{
		metaKey(key),
		eventDataKey(key),
		eventTimeIndexKey(key),
		eventReqIndexKey(key),
		summaryKey(key),
	}
	if _, err := luaDeleteSession.Run(ctx, s.redisClient, keys).Result(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// ListSessions lists all sessions by user scope.
func (s *Service) ListSessions(ctx context.Context, userKey session.UserKey, options ...session.Option) ([]*session.Session, error) {
	pattern := fmt.Sprintf("meta:{%s:%s:*}", userKey.AppName, userKey.UserID)
	var sessions []*session.Session

	iter := s.redisClient.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		mKey := iter.Val()
		metaJSON, err := s.redisClient.Get(ctx, mKey).Bytes()
		if err != nil {
			continue
		}

		var meta sessionMeta
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			continue
		}

		sess := session.NewSession(meta.AppName, meta.UserID, meta.ID)
		sess.State = meta.State
		sess.CreatedAt = meta.CreatedAt
		sess.UpdatedAt = meta.UpdatedAt
		sessions = append(sessions, sess)
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan sessions: %w", err)
	}
	return sessions, nil
}

// AppendEvent appends an event to a session.
func (s *Service) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event, options ...session.Option) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if evt == nil {
		return fmt.Errorf("event is nil")
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}

	evtJSON, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	ttlSeconds := int64(0)
	if s.opts.sessionTTL > 0 {
		ttlSeconds = int64(s.opts.sessionTTL.Seconds())
	}

	keys := []string{
		metaKey(key),
		eventDataKey(key),
		eventTimeIndexKey(key),
		eventReqIndexKey(key),
	}
	args := []interface{}{
		evt.ID,
		string(evtJSON),
		evt.Timestamp.UnixNano(),
		evt.RequestID,
		ttlSeconds,
		s.opts.maxEventsPerSession,
		s.opts.evictionBatchSize,
	}

	if _, err := luaAppendEvent.Run(ctx, s.redisClient, keys, args...).Result(); err != nil {
		return fmt.Errorf("append event: %w", err)
	}

	// Update session state if event has state delta
	if len(evt.StateDelta) > 0 {
		sess.EventMu.Lock()
		for k, v := range evt.StateDelta {
			sess.State[k] = v
		}
		sess.EventMu.Unlock()

		// Update meta in Redis
		now := time.Now()
		meta := sessionMeta{
			ID:        sess.ID,
			AppName:   sess.AppName,
			UserID:    sess.UserID,
			State:     sess.State,
			CreatedAt: sess.CreatedAt,
			UpdatedAt: now,
		}
		metaJSON, _ := json.Marshal(meta)
		s.redisClient.Set(ctx, metaKey(key), metaJSON, s.opts.sessionTTL)
	}

	sess.EventMu.Lock()
	sess.Events = append(sess.Events, *evt)
	sess.EventMu.Unlock()

	return nil
}

// UpdateAppState updates the app-level state.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	key := appStateKey(appName)
	pipe := s.redisClient.TxPipeline()
	for k, v := range state {
		pipe.HSet(ctx, key, k, v)
	}
	if s.opts.appStateTTL > 0 {
		pipe.Expire(ctx, key, s.opts.appStateTTL)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("update app state: %w", err)
	}
	return nil
}

// DeleteAppState deletes a key from app-level state.
func (s *Service) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	if err := s.redisClient.HDel(ctx, appStateKey(appName), key).Err(); err != nil {
		return fmt.Errorf("delete app state: %w", err)
	}
	return nil
}

// ListAppStates lists all app-level state.
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}

	result, err := s.redisClient.HGetAll(ctx, appStateKey(appName)).Result()
	if err != nil {
		return nil, fmt.Errorf("list app states: %w", err)
	}

	state := make(session.StateMap, len(result))
	for k, v := range result {
		state[k] = []byte(v)
	}
	return state, nil
}

// UpdateUserState updates the user-level state.
func (s *Service) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	key := userStateKey(userKey.AppName, userKey.UserID)
	pipe := s.redisClient.TxPipeline()
	for k, v := range state {
		pipe.HSet(ctx, key, k, v)
	}
	if s.opts.userStateTTL > 0 {
		pipe.Expire(ctx, key, s.opts.userStateTTL)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("update user state: %w", err)
	}
	return nil
}

// ListUserStates lists all user-level state.
func (s *Service) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	result, err := s.redisClient.HGetAll(ctx, userStateKey(userKey.AppName, userKey.UserID)).Result()
	if err != nil {
		return nil, fmt.Errorf("list user states: %w", err)
	}

	state := make(session.StateMap, len(result))
	for k, v := range result {
		state[k] = []byte(v)
	}
	return state, nil
}

// DeleteUserState deletes a key from user-level state.
func (s *Service) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	if err := s.redisClient.HDel(ctx, userStateKey(userKey.AppName, userKey.UserID), key).Err(); err != nil {
		return fmt.Errorf("delete user state: %w", err)
	}
	return nil
}

// UpdateSessionState updates the session-level state directly.
func (s *Service) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	metaJSON, err := s.redisClient.Get(ctx, metaKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("session not found")
		}
		return fmt.Errorf("get session meta: %w", err)
	}

	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return fmt.Errorf("unmarshal session meta: %w", err)
	}

	if meta.State == nil {
		meta.State = make(session.StateMap)
	}
	for k, v := range state {
		meta.State[k] = v
	}
	meta.UpdatedAt = time.Now()

	updatedJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal session meta: %w", err)
	}

	if err := s.redisClient.Set(ctx, metaKey(key), updatedJSON, s.opts.sessionTTL).Err(); err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	return nil
}

// Compile-time interface check.
var _ session.Service = (*Service)(nil)

// TrimConversations trims the most recent N conversations from the session.
// This is a V2-specific extension method not part of session.Service interface.
// Returns the deleted events in chronological order.
func (s *Service) TrimConversations(ctx context.Context, key session.Key, count int) ([]event.Event, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 1
	}

	keys := []string{
		eventDataKey(key),
		eventTimeIndexKey(key),
		eventReqIndexKey(key),
	}

	result, err := luaTrimConversations.Run(ctx, s.redisClient, keys, count).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("trim conversations: %w", err)
	}

	var events []event.Event
	for _, evtJSON := range result {
		var evt event.Event
		if err := json.Unmarshal([]byte(evtJSON), &evt); err != nil {
			continue
		}
		events = append(events, evt)
	}
	return events, nil
}

// GetEventsByRequestID retrieves all events for a given RequestID.
// This is a V2-specific extension method not part of session.Service interface.
func (s *Service) GetEventsByRequestID(ctx context.Context, key session.Key, requestID string) ([]event.Event, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	if requestID == "" {
		return nil, fmt.Errorf("requestID is required")
	}

	keys := []string{
		eventDataKey(key),
		eventReqIndexKey(key),
	}

	result, err := luaGetEventsByRequestID.Run(ctx, s.redisClient, keys, requestID).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("get events by request id: %w", err)
	}

	var events []event.Event
	for _, evtJSON := range result {
		var evt event.Event
		if err := json.Unmarshal([]byte(evtJSON), &evt); err != nil {
			continue
		}
		events = append(events, evt)
	}
	return events, nil
}

// DeleteEvent deletes a single event from the session.
// This is a V2-specific extension method not part of session.Service interface.
func (s *Service) DeleteEvent(ctx context.Context, key session.Key, eventID string, requestID string) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	if eventID == "" {
		return fmt.Errorf("eventID is required")
	}

	keys := []string{
		eventDataKey(key),
		eventTimeIndexKey(key),
		eventReqIndexKey(key),
	}

	if _, err := luaDeleteEvent.Run(ctx, s.redisClient, keys, eventID, requestID).Result(); err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	return nil
}
