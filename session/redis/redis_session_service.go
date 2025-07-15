//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var _ session.Service = (*Service)(nil)

const (
	defaultSessionEventLimit = 100
)

// SessionState is the state of a session.
type SessionState struct {
	ID        string           `json:"id"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// ServiceOpts is the options for the redis session service.
type ServiceOpts struct {
	sessionEventLimit int
	redisClient       redis.UniversalClient
}

// ServiceOption is the option for the redis session service.
type ServiceOption func(*ServiceOpts)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) func(*ServiceOpts) {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithRedisClient sets the redis client.
func WithRedisClient(redisClient redis.UniversalClient) func(*ServiceOpts) {
	return func(opts *ServiceOpts) {
		opts.redisClient = redisClient
	}
}

// Service is the redis session service.
// storage structure:
// AppState: appName -> hash [key -> value(json)] (expireTime)
// UserState: appName + userId -> hash [key -> value(json)]
// SessionState: appName + userId -> hash [sessionId -> SessionState(json)]
// Event: appName + userId + sessionId -> sorted set [value: Event(json) score: timestamp]
type Service struct {
	opts ServiceOpts
}

// NewService creates a new redis session service.
func NewService(options ...ServiceOption) (*Service, error) {
	opts := ServiceOpts{
		sessionEventLimit: defaultSessionEventLimit,
	}
	for _, option := range options {
		option(&opts)
	}
	if opts.redisClient == nil {
		return nil, errors.New("redis client is required")
	}
	return &Service{opts: opts}, nil
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
	if err := s.storeSessionState(ctx, key, sessState); err != nil {
		return nil, err
	}

	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     sessState.State,
		Events:    []event.Event{},
		UpdatedAt: sessState.UpdatedAt,
		CreatedAt: sessState.CreatedAt,
	}, nil
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

	pipe := s.opts.redisClient.TxPipeline()
	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		pipe.HSet(ctx, getAppStateKey(appName), k, v)
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

	appState, err := s.opts.redisClient.HGetAll(ctx, getAppStateKey(appName)).Result()
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

	pipe := s.opts.redisClient.TxPipeline()
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
	pipe := s.opts.redisClient.TxPipeline()
	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		pipe.HSet(ctx, getUserStateKey(session.Key{
			AppName: userKey.AppName,
			UserID:  userKey.UserID,
		}), k, v)
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
	userState, err := s.opts.redisClient.HGetAll(ctx, getUserStateKey(session.Key{
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

	pipe := s.opts.redisClient.TxPipeline()
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
	opt := applyOptions(opts...)
	sess.Events = append(sess.Events, *event)
	sess.UpdatedAt = time.Now()
	if opt.EventNum > 0 && len(sess.Events) > opt.EventNum {
		sess.Events = sess.Events[len(sess.Events)-opt.EventNum:]
	}
	if err := s.addEvent(ctx, key, event); err != nil {
		return fmt.Errorf("redis session service append event failed: %w", err)
	}
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

func (s *Service) storeSessionState(ctx context.Context, key session.Key, sessState *SessionState) error {
	sessKey := getSessionStateKey(key)
	sessBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session failed: %w", err)
	}
	if err := s.opts.redisClient.HSet(ctx, sessKey, key.SessionID, sessBytes).Err(); err != nil {
		return fmt.Errorf("store session failed: %w", err)
	}
	return nil
}

func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	sessKey := getSessionStateKey(key)
	pipe := s.opts.redisClient.Pipeline()
	userStateCmd := pipe.HGetAll(ctx, getUserStateKey(key))
	appStateCmd := pipe.HGetAll(ctx, getAppStateKey(key.AppName))

	sessCmd := pipe.HGet(ctx, sessKey, key.SessionID)
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
	return mergeState(appState, userState, &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     sessState.State,
		Events:    events[0],
		UpdatedAt: sessState.UpdatedAt,
		CreatedAt: sessState.CreatedAt,
	}), nil
}

func (s *Service) listSessions(
	ctx context.Context,
	key session.UserKey,
	limit int,
	afterTime time.Time,
) ([]*session.Session, error) {
	pipe := s.opts.redisClient.Pipeline()
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
	zrangeBy := &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", afterTime.Unix()),
		Max: fmt.Sprintf("%d", time.Now().Unix()),
	}
	if limit > 0 {
		zrangeBy.Offset = 0
		zrangeBy.Count = int64(limit)
	}

	pipe := s.opts.redisClient.Pipeline()
	for _, key := range sessionKeys {
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
			return nil, fmt.Errorf("unmarshal event failed: %w", err)
		}
		events = append(events, *event)
	}
	return events, nil
}

func (s *Service) addEvent(ctx context.Context, key session.Key, event *event.Event) error {
	stateBytes, err := s.opts.redisClient.HGet(ctx, getSessionStateKey(key), key.SessionID).Bytes()
	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}
	sessState := &SessionState{}
	if err := json.Unmarshal(stateBytes, sessState); err != nil {
		return fmt.Errorf("unmarshal session state failed: %w", err)
	}

	sessState.UpdatedAt = time.Now()
	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session state failed: %w", err)
	}

	eventBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event failed: %w", err)
	}

	txPipe := s.opts.redisClient.TxPipeline()
	txPipe.HSet(ctx, getSessionStateKey(key), key.SessionID, string(updatedStateBytes))
	txPipe.ZAdd(ctx, getEventKey(key), redis.Z{
		Score:  float64(event.Timestamp.Unix()),
		Member: eventBytes,
	})
	if s.opts.sessionEventLimit > 0 {
		txPipe.ZRemRangeByRank(ctx, getEventKey(key), 0, -(int64(s.opts.sessionEventLimit) + 1))
	}
	if _, err := txPipe.Exec(ctx); err != nil {
		return fmt.Errorf("store event failed: %w", err)
	}
	return nil
}

func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	txPipe := s.opts.redisClient.TxPipeline()
	txPipe.HDel(ctx, getSessionStateKey(key), key.SessionID)
	txPipe.Del(ctx, getEventKey(key))
	if _, err := txPipe.Exec(ctx); err != nil && err != redis.Nil {
		return fmt.Errorf("redis session service delete session state failed: %w", err)
	}
	return nil
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
