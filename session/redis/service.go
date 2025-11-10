//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis provides the redis session service.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/spaolacci/murmur3"
	"trpc.group/trpc-go/trpc-agent-go/event"
	isession "trpc.group/trpc-go/trpc-agent-go/internal/session"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
)

var _ session.Service = (*Service)(nil)

const (
	defaultSessionEventLimit = 1000
	defaultTimeout           = 2 * time.Second
	defaultChanBufferSize    = 100
	defaultAsyncPersisterNum = 10

	defaultAsyncSummaryNum  = 3
	defaultSummaryQueueSize = 100
)

// SessionState is the state of a session.
type SessionState struct {
	ID        string           `json:"id"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// Service is the redis session service.
// storage structure:
// AppState: appName -> hash [key -> value(json)] (expireTime)
// UserState: appName + userId -> hash [key -> value(json)]
// SessionState: appName + userId -> hash [sessionId -> SessionState(json)]
// Event: appName + userId + sessionId -> sorted set [value: Event(json) score: timestamp]
type Service struct {
	opts            ServiceOpts
	redisClient     redis.UniversalClient
	sessionTTL      time.Duration            // TTL for session state and event list
	appStateTTL     time.Duration            // TTL for app state
	userStateTTL    time.Duration            // TTL for user state
	eventPairChans  []chan *sessionEventPair // channel for session events to persistence
	summaryJobChans []chan *summaryJob       // channel for summary jobs to processing
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

// NewService creates a new redis session service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := ServiceOpts{
		sessionEventLimit:  defaultSessionEventLimit,
		sessionTTL:         0,
		appStateTTL:        0,
		userStateTTL:       0,
		asyncPersisterNum:  defaultAsyncPersisterNum,
		enableAsyncPersist: false,
		asyncSummaryNum:    defaultAsyncSummaryNum,
		summaryQueueSize:   defaultSummaryQueueSize,
		summaryJobTimeout:  30 * time.Second,
	}
	for _, option := range options {
		option(&opts)
	}

	var redisClient redis.UniversalClient
	var err error
	builder := storage.GetClientBuilder()

	// if instance name set, and url not set, use instance name to create redis client
	if opts.url == "" && opts.instanceName != "" {
		builderOpts, ok := storage.GetRedisInstance(opts.instanceName)
		if !ok {
			return nil, fmt.Errorf("redis instance %s not found", opts.instanceName)
		}
		redisClient, err = builder(builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create redis client from instance name failed: %w", err)
		}
		s := &Service{
			opts:         opts,
			redisClient:  redisClient,
			sessionTTL:   opts.sessionTTL,
			appStateTTL:  opts.appStateTTL,
			userStateTTL: opts.userStateTTL,
		}
		if opts.enableAsyncPersist {
			s.startAsyncPersistWorker()
		}
		// Always start async summary workers by default.
		s.startAsyncSummaryWorker()
		return s, nil
	}

	redisClient, err = builder(
		storage.WithClientBuilderURL(opts.url),
		storage.WithExtraOptions(opts.extraOptions...),
	)
	if err != nil {
		return nil, fmt.Errorf("create redis client from url failed: %w", err)
	}

	s := &Service{
		opts:         opts,
		redisClient:  redisClient,
		sessionTTL:   opts.sessionTTL,
		appStateTTL:  opts.appStateTTL,
		userStateTTL: opts.userStateTTL,
	}
	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
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

	sessState := &SessionState{
		ID:        key.SessionID,
		State:     make(session.StateMap),
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}
	for k, v := range state {
		sessState.State[k] = v
	}

	// Use pipeline to store session and query states
	sessKey := getSessionStateKey(key)
	userStateKey := getUserStateKey(key)
	appStateKey := getAppStateKey(key.AppName)

	sessBytes, err := json.Marshal(sessState)
	if err != nil {
		return nil, fmt.Errorf("marshal session failed: %w", err)
	}

	pipe := s.redisClient.Pipeline()
	// Store session state
	pipe.HSet(ctx, sessKey, key.SessionID, sessBytes)
	if s.sessionTTL > 0 {
		// expire session state, don't expire event list, it's still empty
		pipe.Expire(ctx, sessKey, s.sessionTTL)
	}
	// Query app and user states
	userStateCmd := pipe.HGetAll(ctx, userStateKey)
	appStateCmd := pipe.HGetAll(ctx, appStateKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("create session failed: %w", err)
	}

	// Process app state
	appState, err := processStateCmd(appStateCmd)
	if err != nil {
		return nil, err
	}

	// Process user state
	userState, err := processStateCmd(userStateCmd)
	if err != nil {
		return nil, err
	}

	// Create session with merged states
	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     sessState.State,
		Events:    []event.Event{},
		UpdatedAt: sessState.UpdatedAt,
		CreatedAt: sessState.CreatedAt,
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
		return nil, fmt.Errorf("redis session service get session state failed: %w", err)
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
		return nil, fmt.Errorf("redis session service get session list failed: %w", err)
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
		return fmt.Errorf("redis session service delete session state failed: %w", err)
	}
	return nil
}

// UpdateAppState updates the state by target scope and key.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	pipe := s.redisClient.TxPipeline()
	appStateKey := getAppStateKey(appName)
	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		pipe.HSet(ctx, appStateKey, k, v)
	}
	// Set TTL for app state if configured
	if s.appStateTTL > 0 {
		pipe.Expire(ctx, appStateKey, s.appStateTTL)
	}

	// should not return redis.Nil error
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis session service update app state failed: %w", err)
	}
	return nil
}

// ListAppStates gets the app states.
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}

	appState, err := s.redisClient.HGetAll(ctx, getAppStateKey(appName)).Result()
	// key not found, return empty state map
	if err == redis.Nil {
		return make(session.StateMap), nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis session service list app states failed: %w", err)
	}
	appStateMap := make(session.StateMap)
	for k, v := range appState {
		appStateMap[k] = []byte(v)
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

	pipe := s.redisClient.TxPipeline()
	pipe.HDel(ctx, getAppStateKey(appName), key)

	// should not return redis.Nil error
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis session service delete app state failed: %w", err)
	}
	return nil
}

// UpdateUserState updates the state by target scope and key.
func (s *Service) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	pipe := s.redisClient.TxPipeline()
	userStateKey := getUserStateKey(session.Key{
		AppName: userKey.AppName,
		UserID:  userKey.UserID,
	})
	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		pipe.HSet(ctx, userStateKey, k, v)
	}
	// Set TTL for user state if configured
	if s.userStateTTL > 0 {
		pipe.Expire(ctx, userStateKey, s.userStateTTL)
	}

	// should not return redis.Nil error
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis session service update user state failed: %w", err)
	}
	return nil
}

// ListUserStates lists the state by target scope and key.
func (s *Service) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	userState, err := s.redisClient.HGetAll(ctx, getUserStateKey(session.Key{
		AppName: userKey.AppName,
		UserID:  userKey.UserID,
	})).Result()
	if err == redis.Nil {
		return make(session.StateMap), nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis session service list user states failed: %w", err)
	}
	userStateMap := make(session.StateMap)
	for k, v := range userState {
		userStateMap[k] = []byte(v)
	}
	return userStateMap, nil
}

// DeleteUserState deletes the state by target scope and key.
func (s *Service) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	pipe := s.redisClient.TxPipeline()
	pipe.HDel(ctx, getUserStateKey(session.Key{
		AppName: userKey.AppName,
		UserID:  userKey.UserID,
	}), key)

	// should not return redis.Nil error
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis session service delete user state failed: %w", err)
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

	// persist event to redis asynchronously
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && err.Error() == "send on closed channel" {
					log.Errorf("redis session service append event failed: %v", r)
					return
				}
				panic(r)
			}
		}()

		// TODO: Init hash index at session creation to prevent duplicate computation.
		hKey := getEventKey(key)
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
		return fmt.Errorf("redis session service append event failed: %w", err)
	}

	return nil
}

// Close closes the service.
func (s *Service) Close() error {
	s.once.Do(func() {
		// close redis connection
		if s.redisClient != nil {
			s.redisClient.Close()
		}

		for _, ch := range s.eventPairChans {
			close(ch)
		}

		for _, ch := range s.summaryJobChans {
			close(ch)
		}
	})

	return nil
}

func getAppStateKey(appName string) string {
	return fmt.Sprintf("appstate:{%s}", appName)
}

func getUserStateKey(key session.Key) string {
	return fmt.Sprintf("userstate:{%s}:%s", key.AppName, key.UserID)
}

func getEventKey(key session.Key) string {
	return fmt.Sprintf("event:{%s}:%s:%s", key.AppName, key.UserID, key.SessionID)
}

func getSessionStateKey(key session.Key) string {
	return fmt.Sprintf("sess:{%s}:%s", key.AppName, key.UserID)
}

func getSessionSummaryKey(key session.Key) string {
	return fmt.Sprintf("sesssum:{%s}:%s", key.AppName, key.UserID)
}

func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	sessKey := getSessionStateKey(key)
	userStateKey := getUserStateKey(key)
	appStateKey := getAppStateKey(key.AppName)
	sessSummaryKey := getSessionSummaryKey(key)
	pipe := s.redisClient.Pipeline()
	userStateCmd := pipe.HGetAll(ctx, userStateKey)
	appStateCmd := pipe.HGetAll(ctx, appStateKey)

	sessCmd := pipe.HGet(ctx, sessKey, key.SessionID)
	// Read summaries from separate hash in the same pipeline.
	summariesCmd := pipe.HGet(ctx, sessSummaryKey, key.SessionID)
	// Add TTL refresh commands to the same pipeline if configured
	if s.sessionTTL > 0 {
		pipe.Expire(ctx, sessKey, s.sessionTTL)
		pipe.Expire(ctx, getEventKey(key), s.sessionTTL)
		pipe.Expire(ctx, sessSummaryKey, s.sessionTTL)
	}
	if s.appStateTTL > 0 {
		pipe.Expire(ctx, appStateKey, s.appStateTTL)
	}
	if s.userStateTTL > 0 {
		pipe.Expire(ctx, userStateKey, s.userStateTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get session state failed: %w", err)
	}

	// query session state
	sessState, err := processSessionStateCmd(sessCmd)
	if err != nil {
		return nil, err
	}
	if sessState == nil {
		return nil, nil
	}

	// query app state
	appState, err := processStateCmd(appStateCmd)
	if err != nil {
		return nil, err
	}

	// query user state
	userState, err := processStateCmd(userStateCmd)
	if err != nil {
		return nil, err
	}

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
		State:     sessState.State,
		Events:    events[0],
		UpdatedAt: sessState.UpdatedAt,
		CreatedAt: sessState.CreatedAt,
	}

	// Attach summaries only if there are events to summarize.
	// Since summaries are generated based on the filtered events (sess.Events),
	// we only need to check if sess.Events is non-empty.
	if len(sess.Events) > 0 {
		if bytes, err := summariesCmd.Bytes(); err == nil && len(bytes) > 0 {
			var summaries map[string]*session.Summary
			if err := json.Unmarshal(bytes, &summaries); err == nil && len(summaries) > 0 {
				sess.Summaries = summaries
			}
		}
	}

	// filter events to ensure they start with RoleUser
	isession.EnsureEventStartWithUser(sess)
	return mergeState(appState, userState, sess), nil
}

func (s *Service) listSessions(
	ctx context.Context,
	key session.UserKey,
	limit int,
	afterTime time.Time,
) ([]*session.Session, error) {
	pipe := s.redisClient.Pipeline()
	sessKey := session.Key{
		AppName: key.AppName,
		UserID:  key.UserID,
	}
	userStateCmd := pipe.HGetAll(ctx, getUserStateKey(sessKey))
	appStateCmd := pipe.HGetAll(ctx, getAppStateKey(sessKey.AppName))
	sessStatesCmd := pipe.HGetAll(ctx, getSessionStateKey(sessKey))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get session state failed: %w", err)
	}

	// process session states list
	sessStates, err := processSessStateCmdList(sessStatesCmd)
	if err == redis.Nil || len(sessStates) == 0 {
		return []*session.Session{}, nil
	}
	if err != nil {
		return nil, err
	}

	// process app state
	appState, err := processStateCmd(appStateCmd)
	if err != nil {
		return nil, err
	}

	// process user state
	userState, err := processStateCmd(userStateCmd)
	if err != nil {
		return nil, err
	}

	// query events list
	sessList := make([]*session.Session, 0, len(sessStates))
	sessionKeys := make([]session.Key, 0, len(sessStates))
	for _, sessState := range sessStates {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: sessState.ID,
		})
	}
	events, err := s.getEventsList(ctx, sessionKeys, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}

	for i, sessState := range sessStates {
		sess := &session.Session{
			ID:        sessState.ID,
			AppName:   key.AppName,
			UserID:    key.UserID,
			State:     sessState.State,
			Events:    events[i],
			UpdatedAt: sessState.UpdatedAt,
			CreatedAt: sessState.CreatedAt,
		}

		// filter events to ensure they start with RoleUser
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
	pipe := s.redisClient.Pipeline()
	for _, key := range sessionKeys {
		zrangeBy := &redis.ZRangeBy{
			Min: fmt.Sprintf("%d", afterTime.UnixNano()),
			Max: fmt.Sprintf("%d", time.Now().UnixNano()),
		}
		if limit > 0 {
			zrangeBy.Offset = 0
			zrangeBy.Count = int64(limit)
		}
		pipe.ZRevRangeByScore(ctx, getEventKey(key), zrangeBy)
	}
	cmds, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}

	sessEventsList := make([][]event.Event, 0, len(cmds))
	for _, cmd := range cmds {
		eventCmd, ok := cmd.(*redis.StringSliceCmd)
		if !ok {
			return nil, fmt.Errorf("get events failed: %w", err)
		}
		events, err := processEventCmd(eventCmd)
		if err != nil {
			return nil, fmt.Errorf("process event cmd failed: %w", err)
		}

		// reverse events to get chronological order (oldest first)
		if len(events) > 1 {
			for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
				events[i], events[j] = events[j], events[i]
			}
		}
		sessEventsList = append(sessEventsList, events)
	}
	return sessEventsList, nil
}

func processStateCmd(cmd *redis.MapStringStringCmd) (session.StateMap, error) {
	bytes, err := cmd.Result()
	if err == redis.Nil {
		return make(session.StateMap), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get state failed: %w", err)
	}
	userState := make(session.StateMap)
	for k, v := range bytes {
		userState[k] = []byte(v)
	}
	return userState, nil
}

func processSessionStateCmd(cmd *redis.StringCmd) (*SessionState, error) {
	bytes, err := cmd.Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session state failed: %w", err)
	}
	sessState := &SessionState{}
	if err := json.Unmarshal(bytes, sessState); err != nil {
		return nil, fmt.Errorf("unmarshal session state failed: %w", err)
	}
	return sessState, nil
}

func processSessStateCmdList(cmd *redis.MapStringStringCmd) ([]*SessionState, error) {
	statesBytes, err := cmd.Result()
	if err == redis.Nil || len(statesBytes) == 0 {
		return []*SessionState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis session service get session states failed: %w", err)
	}
	sessStates := make([]*SessionState, 0, len(statesBytes))
	for _, sessState := range statesBytes {
		state := &SessionState{}
		if err := json.Unmarshal([]byte(sessState), state); err != nil {
			return nil, fmt.Errorf("unmarshal session state failed: %w", err)
		}
		sessStates = append(sessStates, state)
	}
	return sessStates, nil
}

func processEventCmd(cmd *redis.StringSliceCmd) ([]event.Event, error) {
	eventsBytes, err := cmd.Result()
	if err == redis.Nil || len(eventsBytes) == 0 {
		return []event.Event{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}
	events := make([]event.Event, 0, len(eventsBytes))
	for _, eventBytes := range eventsBytes {
		event := &event.Event{}
		if err := json.Unmarshal([]byte(eventBytes), &event); err != nil {
			// Skip malformed or legacy-format events to avoid breaking the whole session fetch.
			// Log and continue so that readable events can still be returned.
			// Common root causes include: historical []byte fields encoded as plain string
			// which triggers base64 decoding errors during JSON unmarshal.
			log.Warnf("skip malformed event in redis history: %v", err)
			continue
		}
		events = append(events, *event)
	}
	return events, nil
}

func (s *Service) addEvent(ctx context.Context, key session.Key, event *event.Event) error {
	stateBytes, err := s.redisClient.HGet(ctx, getSessionStateKey(key), key.SessionID).Bytes()
	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}
	sessState := &SessionState{}
	if err := json.Unmarshal(stateBytes, sessState); err != nil {
		return fmt.Errorf("unmarshal session state failed: %w", err)
	}

	sessState.UpdatedAt = time.Now()
	if sessState.State == nil {
		sessState.State = make(session.StateMap)
	}
	isession.ApplyEventStateDeltaMap(sessState.State, event)
	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session state failed: %w", err)
	}

	eventBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event failed: %w", err)
	}

	txPipe := s.redisClient.TxPipeline()

	// update session state
	txPipe.HSet(ctx, getSessionStateKey(key), key.SessionID, string(updatedStateBytes))
	// Set TTL for session state and event list if configured
	if s.sessionTTL > 0 {
		txPipe.Expire(ctx, getSessionStateKey(key), s.sessionTTL)
	}

	// update event list if the event has response and is not partial
	if event.Response != nil && !event.IsPartial && event.IsValidContent() {
		txPipe.ZAdd(ctx, getEventKey(key), redis.Z{
			Score:  float64(event.Timestamp.UnixNano()),
			Member: eventBytes,
		})
		if s.opts.sessionEventLimit > 0 {
			txPipe.ZRemRangeByRank(ctx, getEventKey(key), 0, -(int64(s.opts.sessionEventLimit) + 1))
		}
		// Set TTL for session state and event list if configured
		if s.sessionTTL > 0 {
			txPipe.Expire(ctx, getEventKey(key), s.sessionTTL)
		}
	}

	if _, err := txPipe.Exec(ctx); err != nil {
		return fmt.Errorf("store event failed: %w", err)
	}
	return nil
}

func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	txPipe := s.redisClient.TxPipeline()
	txPipe.HDel(ctx, getSessionStateKey(key), key.SessionID)
	txPipe.HDel(ctx, getSessionSummaryKey(key), key.SessionID)
	txPipe.Del(ctx, getEventKey(key))
	if _, err := txPipe.Exec(ctx); err != nil && err != redis.Nil {
		return fmt.Errorf("redis session service delete session state failed: %w", err)
	}
	return nil
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
				log.Debugf("Session persistence queue monitoring: channel capacity: %d, current length: %d, session key:%s",
					cap(eventPairChan), len(eventPairChan), getSessionStateKey(eventPair.key))
				if err := s.addEvent(ctx, eventPair.key, eventPair.event); err != nil {
					log.Errorf("redis session service persistence event failed: %w", err)
				}
				cancel()
			}
		}(eventPairChan)
	}
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
