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
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
)

var (
	_ session.Service      = (*Service)(nil)
	_ session.TrackService = (*Service)(nil)
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
	eventPairChans  []chan *sessionEventPair // channel for session events to persistence
	trackEventChans []chan *trackEventPair   // channel for track events to persistence.
	summaryJobChans []chan *summaryJob       // channel for summary jobs to processing
	persistWg       sync.WaitGroup           // wait group for persist workers
	summaryWg       sync.WaitGroup           // wait group for summary workers
	once            sync.Once                // ensure Close is called only once
}

type sessionEventPair struct {
	key   session.Key
	event *event.Event
}

type trackEventPair struct {
	key   session.Key
	event *session.TrackEvent
}

// summaryJob represents a summary job to be processed asynchronously.
type summaryJob struct {
	ctx       context.Context // Detached context preserving values but not cancel.
	filterKey string
	force     bool
	session   *session.Session
}

// NewService creates a new redis session service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderURL(opts.url),
		storage.WithExtraOptions(opts.extraOptions...),
	}
	// if instance name set, and url not set, use instance name to create redis client
	if opts.url == "" && opts.instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetRedisInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("redis instance %s not found", opts.instanceName)
		}
	}

	redisClient, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create redis client failed: %w", err)
	}

	s := &Service{
		opts:        opts,
		redisClient: redisClient,
	}
	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}
	if opts.summarizer != nil {
		s.startAsyncSummaryWorker()
	}
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
	if s.opts.sessionTTL > 0 {
		// expire session state, don't expire event list, it's still empty
		pipe.Expire(ctx, sessKey, s.opts.sessionTTL)
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
	final := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		return s.getSession(c.Context, c.Key, c.Options.EventNum, c.Options.EventTime)
	}
	sess, err := hook.RunGetSessionHooks(s.opts.getSessionHooks, hctx, final)
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
	if s.opts.appStateTTL > 0 {
		pipe.Expire(ctx, appStateKey, s.opts.appStateTTL)
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
	if s.opts.userStateTTL > 0 {
		pipe.Expire(ctx, userStateKey, s.opts.userStateTTL)
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
			return fmt.Errorf("redis session service update session state failed: %s is not allowed, use UpdateAppState instead", k)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf("redis session service update session state failed: %s is not allowed, use UpdateUserState instead", k)
		}
	}

	// Get current session state
	stateBytes, err := s.redisClient.HGet(ctx, getSessionStateKey(key), key.SessionID).Bytes()
	if err == redis.Nil {
		return fmt.Errorf("redis session service update session state failed: session not found")
	}
	if err != nil {
		return fmt.Errorf("redis session service update session state failed: get session state: %w", err)
	}

	// Unmarshal current state
	sessState := &SessionState{}
	if err := json.Unmarshal(stateBytes, sessState); err != nil {
		return fmt.Errorf("redis session service update session state failed: unmarshal state: %w", err)
	}

	// Initialize state map if nil
	if sessState.State == nil {
		sessState.State = make(session.StateMap)
	}

	// Merge new state into current state (allow temp: prefix and unprefixed keys)
	for k, v := range state {
		sessState.State[k] = v
	}

	// Update timestamp
	sessState.UpdatedAt = time.Now()

	// Marshal updated state
	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("redis session service update session state failed: marshal state: %w", err)
	}

	// Update session state in Redis
	pipe := s.redisClient.TxPipeline()
	pipe.HSet(ctx, getSessionStateKey(key), key.SessionID, string(updatedStateBytes))

	// Refresh TTL if configured
	if s.opts.sessionTTL > 0 {
		pipe.Expire(ctx, getSessionStateKey(key), s.opts.sessionTTL)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis session service update session state failed: %w", err)
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

	hctx := &session.AppendEventContext{
		Context: ctx,
		Session: sess,
		Event:   event,
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

	// persist event to redis asynchronously
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok &&
					err.Error() == "send on closed channel" {
					log.ErrorfContext(
						ctx,
						"redis session service append event failed: %v",
						r,
					)
					return
				}
				panic(r)
			}
		}()

		index := sess.Hash % len(s.eventPairChans)
		select {
		case s.eventPairChans[index] <- &sessionEventPair{key: key, event: e}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	if err := s.addEvent(ctx, key, e); err != nil {
		return fmt.Errorf("redis session service append event failed: %w", err)
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
	// Update user session with the given track event.
	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}
	// Persist track event to redis asynchronously.
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok &&
					err.Error() == "send on closed channel" {
					log.ErrorfContext(
						ctx,
						"redis session service append track event "+
							"failed: %v",
						r,
					)
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
		return fmt.Errorf("redis session service append track event failed: %w", err)
	}
	return nil
}

// Close closes the service.
func (s *Service) Close() error {
	s.once.Do(func() {
		// Close redis connection.
		if s.redisClient != nil {
			s.redisClient.Close()
		}

		// Close event pair channels and wait for persist workers.
		for _, ch := range s.eventPairChans {
			close(ch)
		}
		// Close track event channels and wait for persist workers.
		for _, ch := range s.trackEventChans {
			close(ch)
		}
		s.persistWg.Wait()

		// Close summary job channels and wait for summary workers.
		for _, ch := range s.summaryJobChans {
			close(ch)
		}
		s.summaryWg.Wait()
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

func getTrackKey(key session.Key, track session.Track) string {
	return fmt.Sprintf("track:{%s}:%s:%s:%s", key.AppName, key.UserID, key.SessionID, track)
}

func getSessionStateKey(key session.Key) string {
	return fmt.Sprintf("sess:{%s}:%s", key.AppName, key.UserID)
}

func getSessionSummaryKey(key session.Key) string {
	return fmt.Sprintf("sesssum:{%s}:%s", key.AppName, key.UserID)
}

func (s *Service) fetchSessionMeta(
	ctx context.Context,
	key session.Key,
) (*SessionState, *redis.StringCmd, session.StateMap, session.StateMap, error) {
	sessKey := getSessionStateKey(key)
	userStateKey := getUserStateKey(key)
	appStateKey := getAppStateKey(key.AppName)
	sessSummaryKey := getSessionSummaryKey(key)

	pipe := s.redisClient.Pipeline()
	userStateCmd := pipe.HGetAll(ctx, userStateKey)
	appStateCmd := pipe.HGetAll(ctx, appStateKey)
	sessCmd := pipe.HGet(ctx, sessKey, key.SessionID)
	summariesCmd := pipe.HGet(ctx, sessSummaryKey, key.SessionID)

	s.appendSessionTTL(ctx, pipe, key, sessKey, sessSummaryKey, appStateKey, userStateKey)

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, nil, nil, nil, fmt.Errorf("get session state failed: %w", err)
	}

	sessState, err := processSessionStateCmd(sessCmd)
	if err != nil || sessState == nil {
		return sessState, nil, nil, nil, err
	}

	appState, err := processStateCmd(appStateCmd)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	userState, err := processStateCmd(userStateCmd)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return sessState, summariesCmd, appState, userState, nil
}

func (s *Service) appendSessionTTL(
	ctx context.Context,
	pipe redis.Pipeliner,
	key session.Key,
	sessKey string,
	sessSummaryKey string,
	appStateKey string,
	userStateKey string,
) {
	if s.opts.sessionTTL > 0 {
		pipe.Expire(ctx, sessKey, s.opts.sessionTTL)
		pipe.Expire(ctx, getEventKey(key), s.opts.sessionTTL)
		pipe.Expire(ctx, sessSummaryKey, s.opts.sessionTTL)
	}
	if s.opts.appStateTTL > 0 {
		pipe.Expire(ctx, appStateKey, s.opts.appStateTTL)
	}
	if s.opts.userStateTTL > 0 {
		pipe.Expire(ctx, userStateKey, s.opts.userStateTTL)
	}
}

func normalizeSessionEvents(events [][]event.Event) []event.Event {
	if len(events) == 0 {
		return nil
	}
	return events[0]
}

func attachTrackEvents(
	sess *session.Session,
	trackEvents []map[session.Track][]session.TrackEvent,
) {
	if len(trackEvents) == 0 || len(trackEvents[0]) == 0 {
		return
	}

	sess.Tracks = make(map[session.Track]*session.TrackEvents, len(trackEvents[0]))
	for trackName, history := range trackEvents[0] {
		sess.Tracks[trackName] = &session.TrackEvents{
			Track:  trackName,
			Events: history,
		}
	}
}

func attachSummaries(sess *session.Session, summariesCmd *redis.StringCmd) {
	if len(sess.Events) == 0 || summariesCmd == nil {
		return
	}

	if bytes, err := summariesCmd.Bytes(); err == nil && len(bytes) > 0 {
		var summaries map[string]*session.Summary
		if err := json.Unmarshal(bytes, &summaries); err == nil && len(summaries) > 0 {
			sess.Summaries = summaries
		}
	}
}

func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	sessState, summariesCmd, appState, userState, err := s.fetchSessionMeta(
		ctx, key)
	if err != nil {
		return nil, err
	}
	if sessState == nil {
		return nil, nil
	}

	events, err := s.getEventsList(ctx, []session.Key{key}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}

	sess := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(sessState.State),
		session.WithSessionEvents(normalizeSessionEvents(events)),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)

	trackEvents, err := s.getTrackEvents(ctx, []session.Key{key}, []*SessionState{sessState}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get track events failed: %w", err)
	}
	attachTrackEvents(sess, trackEvents)
	attachSummaries(sess, summariesCmd)
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
	trackEvents, err := s.getTrackEvents(ctx, sessionKeys, sessStates, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get track events: %w", err)
	}
	if len(trackEvents) != len(sessStates) {
		return nil, fmt.Errorf("track events count mismatch: %w", err)
	}

	for i, sessState := range sessStates {
		sess := session.NewSession(
			key.AppName, key.UserID, sessState.ID,
			session.WithSessionState(sessState.State),
			session.WithSessionEvents(events[i]),
			session.WithSessionCreatedAt(sessState.CreatedAt),
			session.WithSessionUpdatedAt(sessState.UpdatedAt),
		)
		if len(trackEvents[i]) > 0 {
			sess.Tracks = make(map[session.Track]*session.TrackEvents, len(trackEvents[i]))
			for trackName, history := range trackEvents[i] {
				sess.Tracks[trackName] = &session.TrackEvents{
					Track:  trackName,
					Events: history,
				}
			}
		}

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
		pipe.ZRange(ctx, getEventKey(key), 0, -1)
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
		events, err := processEventCmd(ctx, eventCmd)
		if err != nil {
			return nil, fmt.Errorf("process event cmd failed: %w", err)
		}
		sess := &session.Session{
			Events: events,
		}
		if limit <= 0 {
			limit = s.opts.sessionEventLimit
		}
		sess.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))
		sessEventsList = append(sessEventsList, sess.Events)
	}
	return sessEventsList, nil
}

type trackQuery struct {
	sessionIdx int
	track      session.Track
	cmd        *redis.StringSliceCmd
}

func validateSessionTrackInputs(
	sessionKeys []session.Key,
	sessionStates []*SessionState,
) error {
	if len(sessionStates) != len(sessionKeys) {
		return fmt.Errorf(
			"session states count mismatch: %d != %d",
			len(sessionStates),
			len(sessionKeys),
		)
	}
	return nil
}

func buildTrackLists(sessionStates []*SessionState) ([][]session.Track, error) {
	trackLists := make([][]session.Track, len(sessionStates))
	for i := range sessionStates {
		tracks, err := session.TracksFromState(sessionStates[i].State)
		if err != nil {
			return nil, fmt.Errorf("get track list failed: %w", err)
		}
		trackLists[i] = tracks
	}
	return trackLists, nil
}

func (s *Service) buildTrackQueries(
	ctx context.Context,
	sessionKeys []session.Key,
	trackLists [][]session.Track,
	limit int,
	afterTime time.Time,
) ([]*trackQuery, redis.Pipeliner) {
	queries := make([]*trackQuery, 0)
	dataPipe := s.redisClient.Pipeline()
	minScore := fmt.Sprintf("%d", afterTime.UnixNano())
	maxScore := fmt.Sprintf("%d", time.Now().UnixNano())

	for i, key := range sessionKeys {
		tracks := trackLists[i]
		for _, track := range tracks {
			trackKey := getTrackKey(key, track)
			zrangeBy := &redis.ZRangeBy{
				Min: minScore,
				Max: maxScore,
			}
			if limit > 0 {
				zrangeBy.Offset = 0
				zrangeBy.Count = int64(limit)
			}
			cmd := dataPipe.ZRevRangeByScore(ctx, trackKey, zrangeBy)
			s.appendTrackTTL(ctx, dataPipe, trackKey)
			queries = append(queries, &trackQuery{
				sessionIdx: i,
				track:      track,
				cmd:        cmd,
			})
		}
	}

	return queries, dataPipe
}

func (s *Service) appendTrackTTL(
	ctx context.Context,
	pipe redis.Pipeliner,
	trackKey string,
) {
	if s.opts.sessionTTL > 0 {
		pipe.Expire(ctx, trackKey, s.opts.sessionTTL)
	}
}

func newTrackResults(count int) []map[session.Track][]session.TrackEvent {
	results := make([]map[session.Track][]session.TrackEvent, count)
	for i := range results {
		results[i] = make(map[session.Track][]session.TrackEvent)
	}
	return results
}

func collectTrackQueryResults(
	queries []*trackQuery,
	sessionCount int,
) ([]map[session.Track][]session.TrackEvent, error) {
	results := newTrackResults(sessionCount)

	for _, query := range queries {
		values, err := query.cmd.Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			return nil, fmt.Errorf("get track events: %w", err)
		}

		events := make([]session.TrackEvent, 0, len(values))
		for _, raw := range values {
			var event session.TrackEvent
			if err := json.Unmarshal([]byte(raw), &event); err != nil {
				return nil, fmt.Errorf("unmarshal track event: %w", err)
			}
			events = append(events, event)
		}
		// Reverse events to get chronological order (oldest first).
		if len(events) > 1 {
			slices.Reverse(events)
		}

		results[query.sessionIdx][query.track] = events
	}
	return results, nil
}

func (s *Service) getTrackEvents(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionStates []*SessionState,
	limit int,
	afterTime time.Time,
) ([]map[session.Track][]session.TrackEvent, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	if err := validateSessionTrackInputs(sessionKeys, sessionStates); err != nil {
		return nil, err
	}

	trackLists, err := buildTrackLists(sessionStates)
	if err != nil {
		return nil, err
	}

	queries, dataPipe := s.buildTrackQueries(
		ctx, sessionKeys, trackLists, limit, afterTime)
	if len(queries) == 0 {
		return newTrackResults(len(sessionKeys)), nil
	}

	if _, err := dataPipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get track events: %w", err)
	}
	return collectTrackQueryResults(queries, len(sessionKeys))
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

func processEventCmd(
	ctx context.Context,
	cmd *redis.StringSliceCmd,
) ([]event.Event, error) {
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
			// Skip malformed or legacy-format events to avoid breaking the
			// whole session fetch. Log and continue so that readable events
			// can still be returned. Common root causes include: historical
			// []byte fields encoded as plain string which triggers base64
			// decoding errors during JSON unmarshal.
			log.WarnfContext(
				ctx,
				"skip malformed event in redis history: %v",
				err,
			)
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
	session.ApplyEventStateDeltaMap(sessState.State, event)
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
	if s.opts.sessionTTL > 0 {
		txPipe.Expire(ctx, getSessionStateKey(key), s.opts.sessionTTL)
	}

	// update event list if the event has response and is not partial
	if event.Response != nil && !event.IsPartial && event.IsValidContent() {
		txPipe.ZAdd(ctx, getEventKey(key), redis.Z{
			Score:  float64(event.Timestamp.UnixNano()),
			Member: eventBytes,
		})
		// Set TTL for session state and event list if configured
		if s.opts.sessionTTL > 0 {
			txPipe.Expire(ctx, getEventKey(key), s.opts.sessionTTL)
		}
	}

	if _, err := txPipe.Exec(ctx); err != nil {
		return fmt.Errorf("store event failed: %w", err)
	}
	return nil
}

func (s *Service) addTrackEvent(ctx context.Context, key session.Key, trackEvent *session.TrackEvent) error {
	stateBytes, err := s.redisClient.HGet(ctx, getSessionStateKey(key), key.SessionID).Bytes()
	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}
	sessState := &SessionState{}
	if err := json.Unmarshal(stateBytes, sessState); err != nil {
		return fmt.Errorf("unmarshal session state failed: %w", err)
	}

	sess := &session.Session{
		ID:      key.SessionID,
		AppName: key.AppName,
		UserID:  key.UserID,
		State:   sessState.State,
	}
	if err := sess.AppendTrackEvent(trackEvent); err != nil {
		return err
	}
	sessState.State = sess.State
	sessState.UpdatedAt = sess.UpdatedAt

	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session state failed: %w", err)
	}

	eventBytes, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf("marshal track event failed: %w", err)
	}

	txPipe := s.redisClient.TxPipeline()

	// Update session state.
	txPipe.HSet(ctx, getSessionStateKey(key), key.SessionID, string(updatedStateBytes))
	// Set TTL for session state if configured.
	if s.opts.sessionTTL > 0 {
		txPipe.Expire(ctx, getSessionStateKey(key), s.opts.sessionTTL)
	}

	// Update track event list.
	trackKey := getTrackKey(key, trackEvent.Track)
	txPipe.ZAdd(ctx, trackKey, redis.Z{
		Score:  float64(trackEvent.Timestamp.UnixNano()),
		Member: eventBytes,
	})
	// Set TTL for track event list if configured.
	if s.opts.sessionTTL > 0 {
		txPipe.Expire(ctx, trackKey, s.opts.sessionTTL)
	}

	if _, err := txPipe.Exec(ctx); err != nil {
		return fmt.Errorf("store track event failed: %w", err)
	}
	return nil
}

func (s *Service) listTracksForSession(ctx context.Context, key session.Key) ([]session.Track, error) {
	bytes, err := s.redisClient.HGet(ctx, getSessionStateKey(key), key.SessionID).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("get session state failed: %w", err)
	}
	sessState := &SessionState{}
	if err := json.Unmarshal(bytes, sessState); err != nil {
		return nil, fmt.Errorf("unmarshal session state failed: %w", err)
	}
	return session.TracksFromState(sessState.State)
}

func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	txPipe := s.redisClient.TxPipeline()
	txPipe.HDel(ctx, getSessionStateKey(key), key.SessionID)
	txPipe.HDel(ctx, getSessionSummaryKey(key), key.SessionID)
	txPipe.Del(ctx, getEventKey(key))
	tracks, err := s.listTracksForSession(ctx, key)
	if err != nil {
		return fmt.Errorf("list session tracks: %w", err)
	}
	for _, track := range tracks {
		txPipe.Del(ctx, getTrackKey(key, track))
	}
	if _, err := txPipe.Exec(ctx); err != nil && err != redis.Nil {
		return fmt.Errorf("redis session service delete session state failed: %w", err)
	}
	return nil
}

func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	// init event pair chan.
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(chan *sessionEventPair, defaultChanBufferSize)
	}
	// init track job chan.
	s.trackEventChans = make([]chan *trackEventPair, persisterNum)
	for i := 0; i < persisterNum; i++ {
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
						"session key:%s",
					cap(eventPairChan),
					len(eventPairChan),
					getSessionStateKey(eventPair.key),
				)
				if err := s.addEvent(ctx, eventPair.key, eventPair.event); err != nil {
					log.ErrorfContext(
						ctx,
						"redis session service persistence event "+
							"failed: %w",
						err,
					)
				}
				cancel()
			}
		}(eventPairChan)
	}
	for _, trackEventChan := range s.trackEventChans {
		go func(trackEventChan chan *trackEventPair) {
			defer s.persistWg.Done()
			for trackEvent := range trackEventChan {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				log.DebugfContext(
					ctx,
					"Session track persistence queue monitoring: "+
						"channel capacity: %d, current length: %d, "+
						"session key:%s, track key:%s",
					cap(trackEventChan),
					len(trackEventChan),
					getSessionStateKey(trackEvent.key),
					getTrackKey(
						trackEvent.key,
						trackEvent.event.Track,
					),
				)
				if err := s.addTrackEvent(ctx, trackEvent.key, trackEvent.event); err != nil {
					log.ErrorfContext(
						ctx,
						"redis session service persistence track event "+
							"failed: %w",
						err,
					)
				}
				cancel()
			}
		}(trackEventChan)
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
