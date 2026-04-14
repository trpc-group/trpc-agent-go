//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

// Config holds configuration for HashIdx session storage client.
type Config struct {
	SessionTTL        time.Duration
	AppStateTTL       time.Duration
	UserStateTTL      time.Duration
	SessionEventLimit int
	// KeyPrefix is the optional prefix for all HashIdx keys.
	KeyPrefix string
	// EnableUserSessionIndex enables the per-user session index Hash.
	// When true, CreateSession writes an index entry and ListSessions uses HSCAN
	// on the user session index.
	// When false (default), no index is maintained and ListSessions falls back to SCAN.
	EnableUserSessionIndex bool
}

// Client implements HashIdx session storage logic.
type Client struct {
	client redis.UniversalClient
	keys   *keyBuilder
	cfg    Config
}

// NewClient creates a new HashIdx client.
func NewClient(client redis.UniversalClient, cfg Config) *Client {
	return &Client{
		client: client,
		keys:   newKeyBuilder(cfg.KeyPrefix),
		cfg:    cfg,
	}
}

// sessionMeta is the session metadata structure for HashIdx.
type sessionMeta struct {
	ID        string           `json:"id"`
	AppName   string           `json:"appName"`
	UserID    string           `json:"userID"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// CreateSession creates a new session using HashIdx logic.
// SessionID must be provided by the caller; empty SessionID returns an error.
// When EnableUserSessionIndex is true, atomically writes both the session meta key
// and the session index Hash entry via Lua script.
func (c *Client) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) (*session.Session, error) {
	if key.SessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}

	copiedState := deepCopyState(state)

	now := time.Now()
	meta := sessionMeta{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     copiedState,
		CreatedAt: now,
		UpdatedAt: now,
	}

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal session meta: %w", err)
	}

	if c.cfg.EnableUserSessionIndex {
		if err := c.addSessionToUserIndex(ctx, key, metaJSON, now); err != nil {
			return nil, err
		}
	} else {
		if ok, err := c.client.SetNX(ctx, c.keys.SessionMetaKey(key), metaJSON, c.cfg.SessionTTL).Result(); err != nil {
			return nil, fmt.Errorf("create session: %w", err)
		} else if !ok {
			return nil, fmt.Errorf("session already exists")
		}
	}

	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sess.State = copiedState
	sess.CreatedAt = now
	sess.UpdatedAt = now

	sess.ServiceMeta = map[string]string{util.ServiceMetaStorageTypeKey: util.StorageTypeHashIdx}

	return sess, nil
}

// GetSession retrieves a session using HashIdx logic with all post-processing.
// This matches zset behavior: returns a complete session with:
// - Events (filtered by limit and afterTime)
// - App/User state merged
// - Track events loaded
// - Summaries loaded
// - TTL refreshed for app state, user state, and summary
func (c *Client) GetSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	metaJSON, err := c.client.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("get session meta: %w", err)
	}

	return c.loadSessionComplete(ctx, key, metaJSON, limit, afterTime)
}

// loadSessionComplete loads session data with all post-processing (matches zset behavior).
// This includes: events, app/user state merge, track events, summaries.
//
// Uses 3 Redis round-trips:
//
//	RT1: luaLoadSessionData — events + userState + summary (same {userID} slot)
//	RT2: pipeline ZRANGE for each track (same {userID} slot)
//	RT3: pipeline HGETALL for appState (different {appName} slot)
func (c *Client) loadSessionComplete(
	ctx context.Context,
	key session.Key,
	metaJSON []byte,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal session meta: %w", err)
	}

	sess := session.NewSession(meta.AppName, meta.UserID, meta.ID)
	sess.State = meta.State
	sess.CreatedAt = meta.CreatedAt
	sess.UpdatedAt = meta.UpdatedAt

	// Parse track names from session state (pure memory, no Redis call)
	tracks, _ := session.TracksFromState(meta.State)

	// --- RT1: Lua script for events + userState + summary + TTL refresh ---
	sessionData, err := c.loadSessionDataViaLua(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("load session data: %w", err)
	}

	// Populate events
	for _, evtJSON := range sessionData.parseEvents() {
		var evt event.Event
		if err := json.Unmarshal([]byte(evtJSON), &evt); err != nil {
			continue
		}
		sess.Events = append(sess.Events, evt)
	}

	// Apply event filtering (matches zset behavior)
	sess.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))

	// Merge user state from Lua result
	for k, v := range sessionData.UserState {
		sess.SetState(session.StateUserPrefix+k, []byte(v))
	}

	// Attach summaries (only if events exist, matches zset behavior)
	if len(sess.Events) > 0 && sessionData.Summary != "" {
		var summaries map[string]*session.Summary
		if err := json.Unmarshal([]byte(sessionData.Summary), &summaries); err == nil && len(summaries) > 0 {
			sess.Summaries = summaries
		}
	}

	// --- RT2: load track events (same {userID} slot) ---
	c.loadAndAttachTrackEvents(ctx, key, sess, tracks, limit, afterTime)

	// --- RT3: appState (different hash tag {appName}, cannot be in same Lua) ---
	c.loadAndMergeAppState(ctx, key, sess)

	// Inject HashIdx version tag into ServiceMeta (not persisted, memory only)
	sess.ServiceMeta = map[string]string{util.ServiceMetaStorageTypeKey: util.StorageTypeHashIdx}

	return sess, nil
}

// sessionDataResult holds the decoded result from luaLoadSessionData.
// Events use json.RawMessage because Lua cjson encodes empty arrays as {} (JSON objects).
// Tracks are no longer in this result — they are loaded via a separate pipeline call.
type sessionDataResult struct {
	Events    json.RawMessage   `json:"events"`
	Summary   string            `json:"summary"`
	UserState map[string]string `json:"userState"`
}

// parseEvents parses the events field from the Lua result.
// Handles Lua cjson's empty-array-as-object quirk for []string.
func (r *sessionDataResult) parseEvents() []string {
	if len(r.Events) == 0 {
		return nil
	}
	var result []string
	if err := json.Unmarshal(r.Events, &result); err == nil {
		return result
	}
	// Empty object {} from cjson = empty array
	return nil
}

// loadSessionDataViaLua executes luaLoadSessionData to load events, userState,
// and summary in a single Redis round-trip (RT1).
// Track events are loaded separately via pipeline (RT2).
func (c *Client) loadSessionDataViaLua(
	ctx context.Context,
	key session.Key,
) (*sessionDataResult, error) {
	keys := []string{
		c.keys.EventDataKey(key),
		c.keys.EventTimeIndexKey(key),
		c.keys.SessionMetaKey(key),
		c.keys.SummaryKey(key),
		c.keys.UserStateKey(key.AppName, key.UserID),
	}

	raw, err := luaLoadSessionData.Run(ctx, c.client, keys).Text()
	if err != nil {
		return nil, err
	}

	var result sessionDataResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("unmarshal lua result: %w", err)
	}
	return &result, nil
}

// loadAndMergeAppState loads and merges app state.
// This is a separate round-trip because appState uses {appName} hash tag.
func (c *Client) loadAndMergeAppState(ctx context.Context, key session.Key, sess *session.Session) {
	if sess == nil {
		return
	}
	pipe := c.client.Pipeline()
	appStateCmd := pipe.HGetAll(ctx, c.keys.AppStateKey(key.AppName))
	_, _ = pipe.Exec(ctx)

	if res, err := appStateCmd.Result(); err == nil {
		for k, v := range res {
			sess.SetState(session.StateAppPrefix+k, []byte(v))
		}
	}
}

// loadAndAttachTrackEvents loads track events for a session and attaches them.
// If tracks is nil, it will be resolved from session state.
func (c *Client) loadAndAttachTrackEvents(
	ctx context.Context,
	key session.Key,
	sess *session.Session,
	tracks []session.Track,
	limit int,
	afterTime time.Time,
) {
	if sess == nil {
		return
	}

	// Resolve tracks from session state if not provided
	if tracks == nil {
		var err error
		tracks, err = session.TracksFromState(sess.State)
		if err != nil || len(tracks) == 0 {
			return
		}
	}
	if len(tracks) == 0 {
		return
	}

	trackEventsMap, err := c.GetTrackEvents(ctx, key, tracks, limit, afterTime)
	if err != nil || len(trackEventsMap) == 0 {
		return
	}

	sess.Tracks = make(map[session.Track]*session.TrackEvents, len(trackEventsMap))
	for trackName, events := range trackEventsMap {
		sess.Tracks[trackName] = &session.TrackEvents{
			Track:  trackName,
			Events: events,
		}
	}
}

// AppendEvent persists an event to Redis HashIdx storage and applies StateDelta to session state.
// Note: UpdatedAt is not updated here for performance reasons.
// The last activity time can be inferred from the latest event's timestamp.
// StateDelta from the event is atomically merged into session meta's state via Lua script.
//
// Event storage follows zset behavior:
//   - StateDelta is always applied to session state (regardless of event content)
//   - Event is only stored in event list if: Response != nil && !IsPartial && IsValidContent()
func (c *Client) AppendEvent(ctx context.Context, key session.Key, evt *event.Event) error {
	evtJSON, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	ttlSeconds := int64(0)
	if c.cfg.SessionTTL > 0 {
		ttlSeconds = int64(c.cfg.SessionTTL.Seconds())
	}

	shouldStoreEvent := shouldStoreEventInList(evt)

	keys := []string{
		c.keys.SessionMetaKey(key),
		c.keys.EventDataKey(key),
		c.keys.EventTimeIndexKey(key),
	}
	args := []any{
		evt.ID,
		string(evtJSON),
		evt.Timestamp.UnixNano(),
		ttlSeconds,
		boolToInt(shouldStoreEvent),
	}

	result, err := luaAppendEvent.Run(ctx, c.client, keys, args...).Int()
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	if result == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

// shouldStoreEventInList checks if an event should be stored in the event list.
// Matches zset behavior: only store events with Response != nil && !IsPartial && IsValidContent().
func shouldStoreEventInList(evt *event.Event) bool {
	if evt == nil || evt.Response == nil || evt.IsPartial {
		return false
	}
	return evt.Response.IsValidContent()
}

// boolToInt converts a boolean to int (1 for true, 0 for false) for Lua script.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DeleteSession deletes a session and all associated data in HashIdx storage,
// including track keys discovered from session state.
// When EnableUserSessionIndex is true, also removes the session index entry.
func (c *Client) DeleteSession(ctx context.Context, key session.Key) error {
	keys := c.keys.SessionKeys(key)

	tracks, _ := c.ListTracksForSession(ctx, key)
	for _, t := range tracks {
		keys = append(keys, c.keys.TrackKeys(key, t)...)
	}

	if c.cfg.EnableUserSessionIndex {
		if err := c.removeSessionFromUserIndex(ctx, keys, key); err != nil {
			return err
		}
	} else {
		if _, err := luaDeleteSessionLegacy.Run(ctx, c.client, keys).Result(); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
	}
	return nil
}

// TrimConversations trims the most recent N conversations from the session (HashIdx).
func (c *Client) TrimConversations(ctx context.Context, key session.Key, count int) ([]event.Event, error) {
	if count <= 0 {
		count = 1
	}

	keys := []string{
		c.keys.EventDataKey(key),
		c.keys.EventTimeIndexKey(key),
	}

	result, err := luaTrimConversations.Run(ctx, c.client, keys, count).StringSlice()
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

// DeleteEvent deletes a single event from the session (HashIdx).
func (c *Client) DeleteEvent(ctx context.Context, key session.Key, eventID string) error {
	keys := []string{
		c.keys.EventDataKey(key),
		c.keys.EventTimeIndexKey(key),
	}

	if _, err := luaDeleteEvent.Run(ctx, c.client, keys, eventID).Result(); err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	return nil
}

// RefreshSummaryTTL refreshes the TTL for session summary key.
func (c *Client) RefreshSummaryTTL(ctx context.Context, key session.Key) error {
	if c.cfg.SessionTTL <= 0 {
		return nil
	}
	return c.client.Expire(ctx, c.keys.SummaryKey(key), c.cfg.SessionTTL).Err()
}

// ListSessions lists sessions (HashIdx) with all post-processing.
// Uses HSCAN on the user session index when enabled.
// Falls back to SCAN session meta keys when user session index is disabled.
// This matches zset behavior:
// - Events (filtered by limit and afterTime)
// - App/User state merged (batch loaded, shared across sessions)
// - Track events loaded
// - Note: Summaries are NOT loaded in ListSessions (same as zset)
func (c *Client) ListSessions(ctx context.Context, userKey session.UserKey, limit int, afterTime time.Time, listOnlyMeta bool) ([]*session.Session, error) {
	var sessions []*session.Session
	var err error

	if c.cfg.EnableUserSessionIndex {
		sessions, err = c.listSessionsFromUserIndex(ctx, userKey, limit, afterTime, listOnlyMeta)
	} else {
		sessions, err = c.listSessionsByScan(ctx, userKey, limit, afterTime, listOnlyMeta)
	}
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}

	if len(sessions) > 0 {
		appState, _ := c.ListAppStates(ctx, userKey.AppName)
		userState, _ := c.ListUserStates(ctx, userKey)

		for _, sess := range sessions {
			for k, v := range appState {
				sess.SetState(session.StateAppPrefix+k, v)
			}
			for k, v := range userState {
				sess.SetState(session.StateUserPrefix+k, v)
			}

			if !listOnlyMeta {
				key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
				c.loadAndAttachTrackEvents(ctx, key, sess, nil, limit, afterTime)
			}
		}

		_ = c.RefreshAppStateTTL(ctx, userKey.AppName)
		_ = c.RefreshUserStateTTL(ctx, userKey)
	}

	return sessions, nil
}

func (c *Client) listSessionsByScan(
	ctx context.Context,
	userKey session.UserKey,
	limit int,
	afterTime time.Time,
	listOnlyMeta bool,
) ([]*session.Session, error) {
	pattern := c.keys.SessionMetaPattern(userKey)
	iter := c.client.Scan(ctx, 0, pattern, 100).Iterator()

	var sessions []*session.Session
	for iter.Next(ctx) {
		metaJSON, err := c.client.Get(ctx, iter.Val()).Bytes()
		if err != nil {
			continue
		}

		var meta sessionMeta
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			continue
		}

		key := session.Key{
			AppName:   meta.AppName,
			UserID:    meta.UserID,
			SessionID: meta.ID,
		}
		sess, err := c.loadSessionBasic(ctx, key, metaJSON, limit, afterTime, listOnlyMeta)
		if err == nil && sess != nil {
			sessions = append(sessions, sess)
		}
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan session meta keys: %w", err)
	}

	return sessions, nil
}

func (c *Client) listSessionsFromUserIndex(
	ctx context.Context,
	userKey session.UserKey,
	limit int,
	afterTime time.Time,
	listOnlyMeta bool,
) ([]*session.Session, error) {
	sessionIDs, err := c.listSessionIDsFromUserIndex(ctx, userKey)
	if err != nil {
		return nil, fmt.Errorf("scan session index: %w", err)
	}
	if len(sessionIDs) == 0 {
		return nil, nil
	}

	pipe := c.client.Pipeline()
	metaCmds := make(map[string]*redis.StringCmd, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		key := session.Key{AppName: userKey.AppName, UserID: userKey.UserID, SessionID: sessionID}
		metaCmds[sessionID] = pipe.Get(ctx, c.keys.SessionMetaKey(key))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("batch get session meta: %w", err)
	}

	var staleIDs []string
	var sessions []*session.Session
	for sessionID, cmd := range metaCmds {
		metaJSON, err := cmd.Bytes()
		if err != nil {
			if err == redis.Nil {
				staleIDs = append(staleIDs, sessionID)
			}
			continue
		}

		var meta sessionMeta
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			continue
		}

		key := session.Key{
			AppName:   meta.AppName,
			UserID:    meta.UserID,
			SessionID: meta.ID,
		}
		sess, err := c.loadSessionBasic(ctx, key, metaJSON, limit, afterTime, listOnlyMeta)
		if err == nil && sess != nil {
			sessions = append(sessions, sess)
		}
	}

	c.cleanupStaleUserSessionIndexEntries(ctx, userKey, staleIDs)
	return sessions, nil
}

// loadSessionBasic loads session with events only (no app/user state, no track, no summary).
// Used by ListSessions where post-processing is done in batch.
// When listOnlyMeta is true, events are not loaded from Redis.
func (c *Client) loadSessionBasic(
	ctx context.Context,
	key session.Key,
	metaJSON []byte,
	limit int,
	afterTime time.Time,
	listOnlyMeta bool,
) (*session.Session, error) {
	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal session meta: %w", err)
	}

	sess := session.NewSession(meta.AppName, meta.UserID, meta.ID)
	sess.State = meta.State
	sess.CreatedAt = meta.CreatedAt
	sess.UpdatedAt = meta.UpdatedAt

	if !listOnlyMeta {
		result, err := luaLoadEvents.Run(ctx, c.client,
			[]string{
				c.keys.EventDataKey(key),
				c.keys.EventTimeIndexKey(key),
			},
			0, int64(-1), 0,
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

		sess.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))
	}

	sess.ServiceMeta = map[string]string{util.ServiceMetaStorageTypeKey: util.StorageTypeHashIdx}

	return sess, nil
}

// deepCopyState creates a deep copy of the state map to prevent external modifications.
// Returns an empty map (not nil) if input is nil to ensure State is always initialized.
func deepCopyState(state session.StateMap) session.StateMap {
	if state == nil {
		return make(session.StateMap)
	}
	copied := make(session.StateMap, len(state))
	for k, v := range state {
		if v == nil {
			copied[k] = nil
			continue
		}
		val := make([]byte, len(v))
		copy(val, v)
		copied[k] = val
	}
	return copied
}
