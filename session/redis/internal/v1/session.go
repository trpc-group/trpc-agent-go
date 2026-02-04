//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package v1 implements the V1 Redis session storage logic.
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

const (
	// ServiceMetaVersionKey is the key in Session.ServiceMeta to store the data version.
	ServiceMetaVersionKey = "version"
	// VersionV1 indicates the session is stored in V1 format.
	VersionV1 = "v1"
)

// Config holds configuration for V1 session storage client.
type Config struct {
	SessionTTL        time.Duration
	AppStateTTL       time.Duration
	UserStateTTL      time.Duration
	SessionEventLimit int
	KeyPrefix         string // Prefix for legacy keys
}

// Client implements V1 session storage logic.
type Client struct {
	client redis.UniversalClient
	cfg    Config
}

// NewClient creates a new V1 client.
func NewClient(client redis.UniversalClient, cfg Config) *Client {
	return &Client{
		client: client,
		cfg:    cfg,
	}
}

// SessionState is the state of a session (V1 structure).
type SessionState struct {
	ID        string           `json:"id"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// CreateSession creates a new session using V1 logic.
// SessionID must be provided by the caller; empty SessionID returns an error.
func (c *Client) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) (*session.Session, error) {
	if key.SessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}

	sessState := &SessionState{
		ID:        key.SessionID,
		State:     make(session.StateMap),
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
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

	sessKey := c.sessionStateKey(key)
	userStateKey := c.userStateKey(key)
	appStateKey := c.appStateKey(key.AppName)

	sessBytes, err := json.Marshal(sessState)
	if err != nil {
		return nil, fmt.Errorf("marshal session failed: %w", err)
	}

	pipe := c.client.Pipeline()
	pipe.HSet(ctx, sessKey, key.SessionID, sessBytes)
	if c.cfg.SessionTTL > 0 {
		pipe.Expire(ctx, sessKey, c.cfg.SessionTTL)
	}
	userStateCmd := pipe.HGetAll(ctx, userStateKey)
	appStateCmd := pipe.HGetAll(ctx, appStateKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("create session (v1) failed: %w", err)
	}

	appState, err := util.ProcessStateCmd(appStateCmd)
	if err != nil {
		return nil, err
	}
	userState, err := util.ProcessStateCmd(userStateCmd)
	if err != nil {
		return nil, err
	}

	sess := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(sessState.State),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)
	// Inject V1 version tag into ServiceMeta (not persisted, memory only)
	sess.ServiceMeta = map[string]string{ServiceMetaVersionKey: VersionV1}

	return util.MergeState(appState, userState, sess), nil
}

// GetSession retrieves a session using V1 logic.
func (c *Client) GetSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	sessState, summariesCmd, appState, userState, err := c.fetchSessionMeta(ctx, key)
	if err != nil {
		return nil, err
	}
	if sessState == nil {
		return nil, nil // Not found
	}

	events, err := c.getEventsList(ctx, []session.Key{key}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events (v1) failed: %w", err)
	}

	sess := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(sessState.State),
		session.WithSessionEvents(util.NormalizeSessionEvents(events)),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)

	trackEvents, err := c.getTrackEvents(ctx, []session.Key{key}, []*SessionState{sessState}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get track events (v1) failed: %w", err)
	}
	util.AttachTrackEvents(sess, trackEvents)
	util.AttachSummaries(sess, summariesCmd)

	// Inject V1 version tag into ServiceMeta (not persisted, memory only)
	sess.ServiceMeta = map[string]string{ServiceMetaVersionKey: VersionV1}

	return util.MergeState(appState, userState, sess), nil
}

// Exists checks if a session exists in V1 storage.
func (c *Client) Exists(ctx context.Context, key session.Key) (bool, error) {
	exists, err := c.client.HExists(ctx, c.sessionStateKey(key), key.SessionID).Result()
	if err != nil {
		return false, fmt.Errorf("check session exists (v1): %w", err)
	}
	return exists, nil
}

// ExistsPipelined adds a V1 session existence check to the pipeline.
// Returns the BoolCmd that can be evaluated after pipeline execution.
func (c *Client) ExistsPipelined(ctx context.Context, pipe redis.Pipeliner, key session.Key) *redis.BoolCmd {
	return pipe.HExists(ctx, c.sessionStateKey(key), key.SessionID)
}

// AppendEvent persists an event to V1 storage.
func (c *Client) AppendEvent(ctx context.Context, key session.Key, event *event.Event) error {
	stateBytes, err := c.client.HGet(ctx, c.sessionStateKey(key), key.SessionID).Bytes()
	if err != nil {
		return fmt.Errorf("get session state (v1) failed: %w", err)
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

	txPipe := c.client.TxPipeline()

	txPipe.HSet(ctx, c.sessionStateKey(key), key.SessionID, string(updatedStateBytes))
	if c.cfg.SessionTTL > 0 {
		txPipe.Expire(ctx, c.sessionStateKey(key), c.cfg.SessionTTL)
	}

	if event.Response != nil && !event.IsPartial && event.IsValidContent() {
		txPipe.ZAdd(ctx, c.eventKey(key), redis.Z{
			Score:  float64(event.Timestamp.UnixNano()),
			Member: eventBytes,
		})
		if c.cfg.SessionTTL > 0 {
			txPipe.Expire(ctx, c.eventKey(key), c.cfg.SessionTTL)
		}
	}

	if _, err := txPipe.Exec(ctx); err != nil {
		return fmt.Errorf("store event (v1) failed: %w", err)
	}
	return nil
}

// ListSessions lists sessions in V1.
func (c *Client) ListSessions(
	ctx context.Context,
	key session.UserKey,
	limit int,
	afterTime time.Time,
) ([]*session.Session, error) {
	pipe := c.client.Pipeline()
	sessKey := session.Key{AppName: key.AppName, UserID: key.UserID}
	userStateCmd := pipe.HGetAll(ctx, c.userStateKey(sessKey))
	appStateCmd := pipe.HGetAll(ctx, c.appStateKey(sessKey.AppName))
	sessStatesCmd := pipe.HGetAll(ctx, c.sessionStateKey(sessKey))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get session state failed: %w", err)
	}

	sessStates, err := processSessStateCmdList(sessStatesCmd)
	if err == redis.Nil || len(sessStates) == 0 {
		return []*session.Session{}, nil
	}
	if err != nil {
		return nil, err
	}

	appState, err := util.ProcessStateCmd(appStateCmd)
	if err != nil {
		return nil, err
	}
	userState, err := util.ProcessStateCmd(userStateCmd)
	if err != nil {
		return nil, err
	}

	sessList := make([]*session.Session, 0, len(sessStates))
	sessionKeys := make([]session.Key, 0, len(sessStates))
	for _, sessState := range sessStates {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: sessState.ID,
		})
	}
	events, err := c.getEventsList(ctx, sessionKeys, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}
	trackEvents, err := c.getTrackEvents(ctx, sessionKeys, sessStates, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get track events: %w", err)
	}

	for i, sessState := range sessStates {
		sess := session.NewSession(
			key.AppName, key.UserID, sessState.ID,
			session.WithSessionState(sessState.State),
			session.WithSessionEvents(events[i]),
			session.WithSessionCreatedAt(sessState.CreatedAt),
			session.WithSessionUpdatedAt(sessState.UpdatedAt),
		)
		util.AttachTrackEvents(sess, []map[session.Track][]session.TrackEvent{trackEvents[i]}) // Helper expects a slice but we process one by one in helper... no helper processes slice of maps. Wait.
		// util.AttachTrackEvents expects []map...
		// In service.go logic:
		/*
			if len(trackEvents[i]) > 0 {
				sess.Tracks = make ...
			}
		*/
		// util.AttachTrackEvents(sess, trackEvents) <-- this was wrong in my thought process if trackEvents is per session?
		// getTrackEvents returns []map[session.Track][]session.TrackEvent.
		// So trackEvents[i] is map[session.Track][]session.TrackEvent.
		// util.AttachTrackEvents signature: func(sess, []map...)
		// It expects slice of maps because original code processed batch results.
		// Let's pass a slice of 1 map.
		util.AttachTrackEvents(sess, []map[session.Track][]session.TrackEvent{trackEvents[i]})

		// Inject V1 version tag into ServiceMeta (not persisted, memory only)
		sess.ServiceMeta = map[string]string{ServiceMetaVersionKey: VersionV1}

		sessList = append(sessList, util.MergeState(appState, userState, sess))
	}
	return sessList, nil
}

// UpdateSessionState updates session state in V1.
func (c *Client) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	stateBytes, err := c.client.HGet(ctx, c.sessionStateKey(key), key.SessionID).Bytes()
	if err == redis.Nil {
		return fmt.Errorf("session not found")
	}
	if err != nil {
		return fmt.Errorf("get session state: %w", err)
	}

	sessState := &SessionState{}
	if err := json.Unmarshal(stateBytes, sessState); err != nil {
		return fmt.Errorf("unmarshal state: %w", err)
	}

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
	sessState.UpdatedAt = time.Now()

	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	pipe := c.client.TxPipeline()
	pipe.HSet(ctx, c.sessionStateKey(key), key.SessionID, string(updatedStateBytes))
	if c.cfg.SessionTTL > 0 {
		pipe.Expire(ctx, c.sessionStateKey(key), c.cfg.SessionTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("update session state failed: %w", err)
	}
	return nil
}

// DeleteSession deletes a session in V1.
func (c *Client) DeleteSession(ctx context.Context, key session.Key) error {
	txPipe := c.client.TxPipeline()
	txPipe.HDel(ctx, c.sessionStateKey(key), key.SessionID)
	txPipe.HDel(ctx, c.sessionSummaryKey(key), key.SessionID)
	txPipe.Del(ctx, c.eventKey(key))

	tracks, err := c.listTracksForSession(ctx, key)
	if err != nil {
		return fmt.Errorf("list tracks: %w", err)
	}
	for _, track := range tracks {
		txPipe.Del(ctx, c.trackKey(key, track))
	}

	if _, err := txPipe.Exec(ctx); err != nil && err != redis.Nil {
		return fmt.Errorf("delete session state failed: %w", err)
	}
	return nil
}

// AppendTrackEvent persists a track event to V1 storage.
func (c *Client) AppendTrackEvent(ctx context.Context, key session.Key, trackEvent *session.TrackEvent) error {
	stateBytes, err := c.client.HGet(ctx, c.sessionStateKey(key), key.SessionID).Bytes()
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
	sessState.State = sess.SnapshotState()
	sessState.UpdatedAt = sess.UpdatedAt

	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session state failed: %w", err)
	}

	eventBytes, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf("marshal track event failed: %w", err)
	}

	txPipe := c.client.TxPipeline()
	txPipe.HSet(ctx, c.sessionStateKey(key), key.SessionID, string(updatedStateBytes))
	if c.cfg.SessionTTL > 0 {
		txPipe.Expire(ctx, c.sessionStateKey(key), c.cfg.SessionTTL)
	}
	trackKey := c.trackKey(key, trackEvent.Track)
	txPipe.ZAdd(ctx, trackKey, redis.Z{
		Score:  float64(trackEvent.Timestamp.UnixNano()),
		Member: eventBytes,
	})
	if c.cfg.SessionTTL > 0 {
		txPipe.Expire(ctx, trackKey, c.cfg.SessionTTL)
	}
	if _, err := txPipe.Exec(ctx); err != nil {
		return fmt.Errorf("store track event failed: %w", err)
	}
	return nil
}

// Internal methods

func (c *Client) fetchSessionMeta(
	ctx context.Context,
	key session.Key,
) (*SessionState, *redis.StringCmd, session.StateMap, session.StateMap, error) {
	sessKey := c.sessionStateKey(key)
	userStateKey := c.userStateKey(key)
	appStateKey := c.appStateKey(key.AppName)
	sessSummaryKey := c.sessionSummaryKey(key)

	pipe := c.client.Pipeline()
	userStateCmd := pipe.HGetAll(ctx, userStateKey)
	appStateCmd := pipe.HGetAll(ctx, appStateKey)
	sessCmd := pipe.HGet(ctx, sessKey, key.SessionID)
	summariesCmd := pipe.HGet(ctx, sessSummaryKey, key.SessionID)

	c.appendSessionTTL(ctx, pipe, key, sessKey, sessSummaryKey, appStateKey, userStateKey)

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, nil, nil, nil, fmt.Errorf("get session state failed: %w", err)
	}

	sessState, err := processSessionStateCmd(sessCmd)
	if err != nil || sessState == nil {
		return sessState, nil, nil, nil, err
	}

	appState, err := util.ProcessStateCmd(appStateCmd)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	userState, err := util.ProcessStateCmd(userStateCmd)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return sessState, summariesCmd, appState, userState, nil
}

func (c *Client) appendSessionTTL(
	ctx context.Context,
	pipe redis.Pipeliner,
	key session.Key,
	sessKey string,
	sessSummaryKey string,
	appStateKey string,
	userStateKey string,
) {
	if c.cfg.SessionTTL > 0 {
		pipe.Expire(ctx, sessKey, c.cfg.SessionTTL)
		pipe.Expire(ctx, c.eventKey(key), c.cfg.SessionTTL)
		pipe.Expire(ctx, sessSummaryKey, c.cfg.SessionTTL)
	}
	if c.cfg.AppStateTTL > 0 {
		pipe.Expire(ctx, appStateKey, c.cfg.AppStateTTL)
	}
	if c.cfg.UserStateTTL > 0 {
		pipe.Expire(ctx, userStateKey, c.cfg.UserStateTTL)
	}
}

func (c *Client) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	limit int,
	afterTime time.Time,
) ([][]event.Event, error) {
	pipe := c.client.Pipeline()
	for _, key := range sessionKeys {
		pipe.ZRange(ctx, c.eventKey(key), 0, -1)
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
		events, err := util.ProcessEventCmd(ctx, eventCmd)
		if err != nil {
			return nil, fmt.Errorf("process event cmd failed: %w", err)
		}
		sess := &session.Session{
			Events: events,
		}
		if limit <= 0 {
			limit = c.cfg.SessionEventLimit
		}
		sess.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))
		sessEventsList = append(sessEventsList, sess.Events)
	}
	return sessEventsList, nil
}

func (c *Client) listTracksForSession(ctx context.Context, key session.Key) ([]session.Track, error) {
	bytes, err := c.client.HGet(ctx, c.sessionStateKey(key), key.SessionID).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	sessState := &SessionState{}
	if err := json.Unmarshal(bytes, sessState); err != nil {
		return nil, err
	}
	return session.TracksFromState(sessState.State)
}

func (c *Client) getTrackEvents(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionStates []*SessionState,
	limit int,
	afterTime time.Time,
) ([]map[session.Track][]session.TrackEvent, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}
	trackLists, err := buildTrackLists(sessionStates)
	if err != nil {
		return nil, err
	}

	queries := make([]*trackQuery, 0)
	dataPipe := c.client.Pipeline()
	minScore := fmt.Sprintf("%d", afterTime.UnixNano())
	maxScore := fmt.Sprintf("%d", time.Now().UnixNano())

	for i, key := range sessionKeys {
		tracks := trackLists[i]
		for _, track := range tracks {
			trackKey := c.trackKey(key, track)
			zrangeBy := &redis.ZRangeBy{
				Min: minScore,
				Max: maxScore,
			}
			if limit > 0 {
				zrangeBy.Offset = 0
				zrangeBy.Count = int64(limit)
			}
			cmd := dataPipe.ZRevRangeByScore(ctx, trackKey, zrangeBy)
			if c.cfg.SessionTTL > 0 {
				dataPipe.Expire(ctx, trackKey, c.cfg.SessionTTL)
			}
			queries = append(queries, &trackQuery{
				sessionIdx: i,
				track:      track,
				cmd:        cmd,
			})
		}
	}

	if len(queries) == 0 {
		return newTrackResults(len(sessionKeys)), nil
	}

	if _, err := dataPipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get track events: %w", err)
	}
	return collectTrackQueryResults(queries, len(sessionKeys))
}

// Helpers - Key generation functions

// prefixedKey adds the configured key prefix to the given base key.
func (c *Client) prefixedKey(base string) string {
	if c.cfg.KeyPrefix != "" {
		return c.cfg.KeyPrefix + ":" + base
	}
	return base
}

// appStateKey returns the Redis key for app state (with prefix).
func (c *Client) appStateKey(appName string) string {
	return c.prefixedKey(fmt.Sprintf("appstate:{%s}", appName))
}

// userStateKey returns the Redis key for user state (with prefix).
func (c *Client) userStateKey(key session.Key) string {
	return c.prefixedKey(fmt.Sprintf("userstate:{%s}:%s", key.AppName, key.UserID))
}

// eventKey returns the Redis key for session events (with prefix).
func (c *Client) eventKey(key session.Key) string {
	return c.prefixedKey(fmt.Sprintf("event:{%s}:%s:%s", key.AppName, key.UserID, key.SessionID))
}

// trackKey returns the Redis key for track events (with prefix).
func (c *Client) trackKey(key session.Key, track session.Track) string {
	return c.prefixedKey(fmt.Sprintf("track:{%s}:%s:%s:%s", key.AppName, key.UserID, key.SessionID, track))
}

// sessionStateKey returns the Redis key for session state (with prefix).
func (c *Client) sessionStateKey(key session.Key) string {
	return c.prefixedKey(fmt.Sprintf("sess:{%s}:%s", key.AppName, key.UserID))
}

// sessionSummaryKey returns the Redis key for session summaries (with prefix).
func (c *Client) sessionSummaryKey(key session.Key) string {
	return c.prefixedKey(fmt.Sprintf("sesssum:{%s}:%s", key.AppName, key.UserID))
}

// UpdateAppState updates app-level state in V1.
func (c *Client) UpdateAppState(ctx context.Context, appName string, state session.StateMap, ttl time.Duration) error {
	pipe := c.client.TxPipeline()
	appStateKey := c.appStateKey(appName)
	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		pipe.HSet(ctx, appStateKey, k, v)
	}
	if ttl > 0 {
		pipe.Expire(ctx, appStateKey, ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("update app state (v1): %w", err)
	}
	return nil
}

// ListAppStates lists app-level states in V1.
func (c *Client) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	appState, err := c.client.HGetAll(ctx, c.appStateKey(appName)).Result()
	if err == redis.Nil {
		return make(session.StateMap), nil
	}
	if err != nil {
		return nil, fmt.Errorf("list app states (v1): %w", err)
	}
	result := make(session.StateMap)
	for k, v := range appState {
		result[k] = []byte(v)
	}
	return result, nil
}

// DeleteAppState deletes a key from app-level state in V1.
func (c *Client) DeleteAppState(ctx context.Context, appName string, key string) error {
	if _, err := c.client.HDel(ctx, c.appStateKey(appName), key).Result(); err != nil {
		return fmt.Errorf("delete app state (v1): %w", err)
	}
	return nil
}

// UpdateUserState updates user-level state in V1.
func (c *Client) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap, ttl time.Duration) error {
	pipe := c.client.TxPipeline()
	userStateKey := c.userStateKey(session.Key{AppName: userKey.AppName, UserID: userKey.UserID})
	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		pipe.HSet(ctx, userStateKey, k, v)
	}
	if ttl > 0 {
		pipe.Expire(ctx, userStateKey, ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("update user state (v1): %w", err)
	}
	return nil
}

// ListUserStates lists user-level states in V1.
func (c *Client) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	userState, err := c.client.HGetAll(ctx, c.userStateKey(session.Key{AppName: userKey.AppName, UserID: userKey.UserID})).Result()
	if err == redis.Nil {
		return make(session.StateMap), nil
	}
	if err != nil {
		return nil, fmt.Errorf("list user states (v1): %w", err)
	}
	result := make(session.StateMap)
	for k, v := range userState {
		result[k] = []byte(v)
	}
	return result, nil
}

// DeleteUserState deletes a key from user-level state in V1.
func (c *Client) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if _, err := c.client.HDel(ctx, c.userStateKey(session.Key{AppName: userKey.AppName, UserID: userKey.UserID}), key).Result(); err != nil {
		return fmt.Errorf("delete user state (v1): %w", err)
	}
	return nil
}

// GetAppStateKey returns the Redis key for app state (without prefix, for external use).
func GetAppStateKey(appName string) string {
	return fmt.Sprintf("appstate:{%s}", appName)
}

// GetUserStateKey returns the Redis key for user state (without prefix, for external use).
func GetUserStateKey(key session.Key) string {
	return fmt.Sprintf("userstate:{%s}:%s", key.AppName, key.UserID)
}

// GetEventKey returns the Redis key for session events (without prefix, for external use).
func GetEventKey(key session.Key) string {
	return fmt.Sprintf("event:{%s}:%s:%s", key.AppName, key.UserID, key.SessionID)
}

// GetTrackKey returns the Redis key for track events (without prefix, for external use).
func GetTrackKey(key session.Key, track session.Track) string {
	return fmt.Sprintf("track:{%s}:%s:%s:%s", key.AppName, key.UserID, key.SessionID, track)
}

// GetSessionStateKey returns the Redis key for session state (without prefix, for external use).
func GetSessionStateKey(key session.Key) string {
	return fmt.Sprintf("sess:{%s}:%s", key.AppName, key.UserID)
}

// GetSessionSummaryKey returns the Redis key for session summaries (without prefix, for external use).
func GetSessionSummaryKey(key session.Key) string {
	return fmt.Sprintf("sesssum:{%s}:%s", key.AppName, key.UserID)
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

type trackQuery struct {
	sessionIdx int
	track      session.Track
	cmd        *redis.StringSliceCmd
}

func newTrackResults(count int) []map[session.Track][]session.TrackEvent {
	results := make([]map[session.Track][]session.TrackEvent, count)
	for i := range results {
		results[i] = make(map[session.Track][]session.TrackEvent)
	}
	return results
}

func collectTrackQueryResults(queries []*trackQuery, sessionCount int) ([]map[session.Track][]session.TrackEvent, error) {
	results := newTrackResults(sessionCount)
	for _, query := range queries {
		values, err := query.cmd.Result()
		if err != nil {
			if err == redis.Nil {
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
		if len(events) > 1 {
			slices.Reverse(events)
		}
		results[query.sessionIdx][query.track] = events
	}
	return results, nil
}

// =============================================================================
// TrimConversations
// =============================================================================

const trimScanBatchSize = 100

// TrimConversations trims recent conversations and returns the deleted events.
// A conversation is defined as all events sharing the same RequestID.
func (c *Client) TrimConversations(ctx context.Context, key session.Key, count int) ([]event.Event, error) {
	if count <= 0 {
		count = 1
	}

	eventKey := c.eventKey(key)
	targetReqIDs := make(map[string]struct{})
	var toDelete []any
	var deletedEvents []event.Event
	var offset int64
	stop := false

	for !stop {
		batch, err := c.client.ZRevRange(ctx, eventKey, offset, offset+trimScanBatchSize-1).Result()
		if err != nil && err != redis.Nil {
			return nil, fmt.Errorf("trim events: load events: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		for _, raw := range batch {
			var evt event.Event
			if err := json.Unmarshal([]byte(raw), &evt); err != nil {
				return nil, fmt.Errorf("trim events: unmarshal event: %w", err)
			}
			if evt.RequestID == "" {
				continue
			}

			if _, ok := targetReqIDs[evt.RequestID]; !ok {
				if len(targetReqIDs) >= count {
					stop = true
					break
				}
				targetReqIDs[evt.RequestID] = struct{}{}
			}

			toDelete = append(toDelete, raw)
			deletedEvents = append(deletedEvents, evt)
		}

		if stop {
			break
		}
		offset += trimScanBatchSize
	}

	if len(toDelete) == 0 {
		return nil, nil
	}

	// Batch remove from ZSet and refresh TTL.
	pipe := c.client.TxPipeline()
	pipe.ZRem(ctx, eventKey, toDelete...)

	sessKey := c.sessionStateKey(key)
	sumKey := c.sessionSummaryKey(key)
	appStateKey := c.appStateKey(key.AppName)
	userStateKey := c.userStateKey(key)
	c.appendSessionTTL(ctx, pipe, key, sessKey, sumKey, appStateKey, userStateKey)

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("trim events: remove events: %w", err)
	}

	// Reverse to return events in chronological order.
	slices.Reverse(deletedEvents)
	return deletedEvents, nil
}

// =============================================================================
// Summary Operations
// =============================================================================

// CreateSummary creates or updates a summary for the session.
// Uses Lua script to atomically merge filterKey summary only if newer.
func (c *Client) CreateSummary(
	ctx context.Context,
	key session.Key,
	filterKey string,
	sum *session.Summary,
	ttl time.Duration,
) error {
	payload, err := json.Marshal(sum)
	if err != nil {
		return fmt.Errorf("marshal summary failed: %w", err)
	}

	sumKey := c.sessionSummaryKey(key)
	hashField := key.SessionID

	if _, err := util.LuaSummariesSetIfNewer.Run(
		ctx, c.client, []string{sumKey}, hashField, filterKey, string(payload),
	).Result(); err != nil {
		return fmt.Errorf("store summary (lua) failed: %w", err)
	}

	if ttl > 0 {
		if err := c.client.Expire(ctx, sumKey, ttl).Err(); err != nil {
			return fmt.Errorf("expire summary failed: %w", err)
		}
	}

	return nil
}

// GetSummary retrieves summaries for the session.
func (c *Client) GetSummary(ctx context.Context, key session.Key) (map[string]*session.Summary, error) {
	sumKey := c.sessionSummaryKey(key)
	hashField := key.SessionID

	bytes, err := c.client.HGet(ctx, sumKey, hashField).Bytes()
	if err == redis.Nil || len(bytes) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get summary failed: %w", err)
	}

	var summaries map[string]*session.Summary
	if err := json.Unmarshal(bytes, &summaries); err != nil {
		return nil, fmt.Errorf("unmarshal summary failed: %w", err)
	}

	return summaries, nil
}
