//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package v2

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

const (
	// ServiceMetaVersionKey is the key in Session.ServiceMeta to store the data version.
	ServiceMetaVersionKey = "version"
	// VersionV2 indicates the session is stored in V2 format.
	VersionV2 = "v2"
)

// Config holds configuration for V2 session storage client.
type Config struct {
	SessionTTL        time.Duration
	AppStateTTL       time.Duration
	UserStateTTL      time.Duration
	SessionEventLimit int
	// KeyPrefix is the prefix for all V2 keys. Default is "v2".
	KeyPrefix string
}

// Client implements V2 session storage logic.
type Client struct {
	client redis.UniversalClient
	keys   *keyBuilder
	cfg    Config
}

// NewClient creates a new V2 client.
func NewClient(client redis.UniversalClient, cfg Config) *Client {
	return &Client{
		client: client,
		keys:   newKeyBuilder(cfg.KeyPrefix),
		cfg:    cfg,
	}
}

// sessionMeta is the session metadata structure for V2.
type sessionMeta struct {
	ID        string           `json:"id"`
	AppName   string           `json:"appName"`
	UserID    string           `json:"userID"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// CreateSession creates a new session using V2 logic.
// SessionID must be provided by the caller; empty SessionID returns an error.
func (c *Client) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) (*session.Session, error) {
	if key.SessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}

	// Deep copy state to avoid external modifications affecting stored data
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

	if ok, err := c.client.SetNX(ctx, c.keys.SessionMetaKey(key), metaJSON, c.cfg.SessionTTL).Result(); err != nil {
		return nil, fmt.Errorf("create session (v2): %w", err)
	} else if !ok {
		return nil, fmt.Errorf("session already exists")
	}

	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sess.State = copiedState
	sess.CreatedAt = now
	sess.UpdatedAt = now

	// Inject V2 version tag into ServiceMeta (not persisted, memory only)
	sess.ServiceMeta = map[string]string{ServiceMetaVersionKey: VersionV2}

	return sess, nil
}

// GetSession retrieves a session using V2 logic with all post-processing.
// This matches V1 behavior: returns a complete session with:
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
		return nil, fmt.Errorf("get session meta (v2): %w", err)
	}

	return c.loadSessionComplete(ctx, key, metaJSON, limit, afterTime)
}

// loadSessionComplete loads session data with all post-processing (matches V1 behavior).
// This includes: events, app/user state merge, track events, summaries.
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

	// Load events (load all, then filter in memory - matches V1 behavior)
	ttlSeconds := int64(0)
	if c.cfg.SessionTTL > 0 {
		ttlSeconds = int64(c.cfg.SessionTTL.Seconds())
	}

	// reverse=0 means oldest first (chronological order)
	// limit=-1 means no limit, load all events
	result, err := luaLoadEvents.Run(ctx, c.client,
		[]string{
			c.keys.EventDataKey(key),
			c.keys.EventTimeIndexKey(key),
			c.keys.SessionMetaKey(key),
		},
		0, int64(-1), ttlSeconds, 0,
	).StringSlice()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("load events (v2): %w", err)
	}

	for _, evtJSON := range result {
		var evt event.Event
		if err := json.Unmarshal([]byte(evtJSON), &evt); err != nil {
			continue
		}
		sess.Events = append(sess.Events, evt)
	}

	// Apply event filtering (matches V1 behavior)
	sess.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))

	// Merge app/user state (matches V1 behavior)
	c.mergeAppUserState(ctx, key, sess)

	// Load and attach track events (matches V1 behavior)
	c.loadAndAttachTrackEvents(ctx, key, sess, limit, afterTime)

	// Load and attach summaries (matches V1 behavior)
	c.loadAndAttachSummaries(ctx, key, sess)

	// Refresh TTLs (matches V1 behavior)
	c.refreshRelatedTTLs(ctx, key)

	// Inject V2 version tag into ServiceMeta (not persisted, memory only)
	sess.ServiceMeta = map[string]string{ServiceMetaVersionKey: VersionV2}

	return sess, nil
}

// mergeAppUserState merges app and user state into session.
func (c *Client) mergeAppUserState(ctx context.Context, key session.Key, sess *session.Session) {
	if sess == nil {
		return
	}

	// Query and merge appState
	appState, err := c.ListAppStates(ctx, key.AppName)
	if err == nil {
		for k, v := range appState {
			sess.SetState(session.StateAppPrefix+k, v)
		}
	}

	// Query and merge userState
	userState, err := c.ListUserStates(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	if err == nil {
		for k, v := range userState {
			sess.SetState(session.StateUserPrefix+k, v)
		}
	}
}

// loadAndAttachTrackEvents loads track events for a session and attaches them.
func (c *Client) loadAndAttachTrackEvents(ctx context.Context, key session.Key, sess *session.Session, limit int, afterTime time.Time) {
	if sess == nil {
		return
	}

	// Get list of tracks from session state
	tracks, err := session.TracksFromState(sess.State)
	if err != nil || len(tracks) == 0 {
		return
	}

	// Load track events
	trackEventsMap, err := c.GetTrackEvents(ctx, key, tracks, limit, afterTime)
	if err != nil || len(trackEventsMap) == 0 {
		return
	}

	// Attach to session
	sess.Tracks = make(map[session.Track]*session.TrackEvents, len(trackEventsMap))
	for trackName, events := range trackEventsMap {
		sess.Tracks[trackName] = &session.TrackEvents{
			Track:  trackName,
			Events: events,
		}
	}
}

// loadAndAttachSummaries loads summaries for a session and attaches them.
func (c *Client) loadAndAttachSummaries(ctx context.Context, key session.Key, sess *session.Session) {
	if sess == nil || len(sess.Events) == 0 {
		return // V1 behavior: don't load summaries if no events
	}

	summaries, err := c.GetSummary(ctx, key)
	if err != nil || len(summaries) == 0 {
		return
	}

	sess.Summaries = summaries
}

// refreshRelatedTTLs refreshes TTLs for app state, user state, and summary.
func (c *Client) refreshRelatedTTLs(ctx context.Context, key session.Key) {
	_ = c.RefreshAppStateTTL(ctx, key.AppName)
	_ = c.RefreshUserStateTTL(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	_ = c.RefreshSummaryTTL(ctx, key)
}

// AppendEvent persists an event to Redis V2 storage and applies StateDelta to session state.
// Note: UpdatedAt is not updated here for performance reasons.
// The last activity time can be inferred from the latest event's timestamp.
// StateDelta from the event is atomically merged into session meta's state via Lua script.
//
// Event storage follows V1 behavior:
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

	// Determine if event should be stored in event list (matches V1 behavior)
	// Only store events with valid response content
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
		return fmt.Errorf("append event (v2): %w", err)
	}
	if result == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

// shouldStoreEventInList checks if an event should be stored in the event list.
// Matches V1 behavior: only store events with Response != nil && !IsPartial && IsValidContent().
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

// DeleteSession deletes a session in V2 storage.
func (c *Client) DeleteSession(ctx context.Context, key session.Key) error {
	keys := c.keys.SessionKeys(key)
	if _, err := luaDeleteSession.Run(ctx, c.client, keys).Result(); err != nil {
		return fmt.Errorf("delete session (v2): %w", err)
	}
	return nil
}

// TrimConversations trims the most recent N conversations from the session (V2).
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
		return nil, fmt.Errorf("trim conversations (v2): %w", err)
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

// DeleteEvent deletes a single event from the session (V2).
func (c *Client) DeleteEvent(ctx context.Context, key session.Key, eventID string) error {
	keys := []string{
		c.keys.EventDataKey(key),
		c.keys.EventTimeIndexKey(key),
	}

	if _, err := luaDeleteEvent.Run(ctx, c.client, keys, eventID).Result(); err != nil {
		return fmt.Errorf("delete event (v2): %w", err)
	}
	return nil
}

// UpdateSessionState updates the session-level state directly (V2).
func (c *Client) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	metaJSON, err := c.client.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("session not found")
		}
		return fmt.Errorf("get session meta (v2): %w", err)
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

	if err := c.client.Set(ctx, c.keys.SessionMetaKey(key), updatedJSON, c.cfg.SessionTTL).Err(); err != nil {
		return fmt.Errorf("update session state (v2): %w", err)
	}
	return nil
}

// Exists checks if session exists.
func (c *Client) Exists(ctx context.Context, key session.Key) (bool, error) {
	n, err := c.client.Exists(ctx, c.keys.SessionMetaKey(key)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ExistsPipelined adds a V2 session existence check to the pipeline.
// Returns the IntCmd that can be evaluated after pipeline execution.
func (c *Client) ExistsPipelined(ctx context.Context, pipe redis.Pipeliner, key session.Key) *redis.IntCmd {
	return pipe.Exists(ctx, c.keys.SessionMetaKey(key))
}

// Key Helpers (Exported for Facade if needed, but App/User state helpers are different)
// App/User state keys are strategy-independent in V2 keyBuilder (but they were in V1 logic too).
// We should expose KeyBuilder or provide helpers.
// Facade in `service.go` needs access to `AppStateKey` and `UserStateKey`.
// These are not session-specific, so maybe `service.go` should hold a `keyBuilder` or use `v2.Client` methods?
// `v2.Client` has `keys` (private).
// Let's expose methods on `v2.Client` to get App/User keys or perform operations.

// UpdateAppState updates app state.
func (c *Client) UpdateAppState(ctx context.Context, appName string, state session.StateMap, ttl time.Duration) error {
	key := c.keys.AppStateKey(appName)
	pipe := c.client.TxPipeline()
	for k, v := range state {
		pipe.HSet(ctx, key, k, v)
	}
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// DeleteAppState deletes app state key.
func (c *Client) DeleteAppState(ctx context.Context, appName string, key string) error {
	return c.client.HDel(ctx, c.keys.AppStateKey(appName), key).Err()
}

// ListAppStates lists app states.
func (c *Client) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	res, err := c.client.HGetAll(ctx, c.keys.AppStateKey(appName)).Result()
	if err != nil {
		if err == redis.Nil {
			return make(session.StateMap), nil
		}
		return nil, err
	}
	state := make(session.StateMap)
	for k, v := range res {
		state[k] = []byte(v)
	}
	return state, nil
}

// UpdateUserState updates user state.
func (c *Client) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap, ttl time.Duration) error {
	key := c.keys.UserStateKey(userKey.AppName, userKey.UserID)
	pipe := c.client.TxPipeline()
	for k, v := range state {
		pipe.HSet(ctx, key, k, v)
	}
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// DeleteUserState deletes user state key.
func (c *Client) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return c.client.HDel(ctx, c.keys.UserStateKey(userKey.AppName, userKey.UserID), key).Err()
}

// ListUserStates lists user states.
func (c *Client) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	res, err := c.client.HGetAll(ctx, c.keys.UserStateKey(userKey.AppName, userKey.UserID)).Result()
	if err != nil {
		if err == redis.Nil {
			return make(session.StateMap), nil
		}
		return nil, err
	}
	state := make(session.StateMap)
	for k, v := range res {
		state[k] = []byte(v)
	}
	return state, nil
}

// RefreshAppStateTTL refreshes the TTL for app state key.
func (c *Client) RefreshAppStateTTL(ctx context.Context, appName string) error {
	if c.cfg.AppStateTTL <= 0 {
		return nil
	}
	return c.client.Expire(ctx, c.keys.AppStateKey(appName), c.cfg.AppStateTTL).Err()
}

// RefreshUserStateTTL refreshes the TTL for user state key.
func (c *Client) RefreshUserStateTTL(ctx context.Context, userKey session.UserKey) error {
	if c.cfg.UserStateTTL <= 0 {
		return nil
	}
	return c.client.Expire(ctx, c.keys.UserStateKey(userKey.AppName, userKey.UserID), c.cfg.UserStateTTL).Err()
}

// RefreshSummaryTTL refreshes the TTL for session summary key.
func (c *Client) RefreshSummaryTTL(ctx context.Context, key session.Key) error {
	if c.cfg.SessionTTL <= 0 {
		return nil
	}
	return c.client.Expire(ctx, c.keys.SummaryKey(key), c.cfg.SessionTTL).Err()
}

// ListSessionsPattern returns scan pattern for listing sessions.
func (c *Client) ListSessionsPattern(userKey session.UserKey) string {
	// Need to access strategy type...
	// We can expose a method on keyBuilder.
	// But ListSessions logic in Service needs it.
	// Let's just implement ListSessionsScan here?
	// The Service ListSessions merges V1 and V2.
	// So we can have `ListSessions(userKey)` in V2 client returning `[]*session.Session`.
	return ""
}

// ListSessions scans for sessions (V2) with all post-processing.
// This matches V1 behavior:
// - Events (filtered by limit and afterTime)
// - App/User state merged (batch loaded, shared across sessions)
// - Track events loaded
// - Note: Summaries are NOT loaded in ListSessions (same as V1)
func (c *Client) ListSessions(ctx context.Context, userKey session.UserKey, limit int, afterTime time.Time) ([]*session.Session, error) {
	pattern := c.keys.SessionMetaPattern(userKey)
	var sessions []*session.Session

	iter := c.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		mKey := iter.Val()
		metaJSON, err := c.client.Get(ctx, mKey).Bytes()
		if err != nil {
			continue
		}
		// Parse meta to get session key for loading events
		var meta sessionMeta
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			continue
		}
		key := session.Key{
			AppName:   meta.AppName,
			UserID:    meta.UserID,
			SessionID: meta.ID,
		}
		sess, err := c.loadSessionBasic(ctx, key, metaJSON, limit, afterTime)
		if err == nil && sess != nil {
			sessions = append(sessions, sess)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}

	// Post-process: batch load shared app/user state and track events
	// This is more efficient than loading per-session
	if len(sessions) > 0 {
		// Batch load app/user state (shared across all sessions of the same user)
		appState, _ := c.ListAppStates(ctx, userKey.AppName)
		userState, _ := c.ListUserStates(ctx, userKey)

		for _, sess := range sessions {
			// Merge app/user state
			for k, v := range appState {
				sess.SetState(session.StateAppPrefix+k, v)
			}
			for k, v := range userState {
				sess.SetState(session.StateUserPrefix+k, v)
			}

			// Load track events for each session
			key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
			c.loadAndAttachTrackEvents(ctx, key, sess, limit, afterTime)
		}

		// Refresh shared state TTLs once
		_ = c.RefreshAppStateTTL(ctx, userKey.AppName)
		_ = c.RefreshUserStateTTL(ctx, userKey)
	}

	return sessions, nil
}

// loadSessionBasic loads session with events only (no app/user state, no track, no summary).
// Used by ListSessions where post-processing is done in batch.
func (c *Client) loadSessionBasic(
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

	// Load events
	ttlSeconds := int64(0)
	if c.cfg.SessionTTL > 0 {
		ttlSeconds = int64(c.cfg.SessionTTL.Seconds())
	}

	result, err := luaLoadEvents.Run(ctx, c.client,
		[]string{
			c.keys.EventDataKey(key),
			c.keys.EventTimeIndexKey(key),
			c.keys.SessionMetaKey(key),
		},
		0, int64(-1), ttlSeconds, 0,
	).StringSlice()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("load events (v2): %w", err)
	}

	for _, evtJSON := range result {
		var evt event.Event
		if err := json.Unmarshal([]byte(evtJSON), &evt); err != nil {
			continue
		}
		sess.Events = append(sess.Events, evt)
	}

	// Apply event filtering
	sess.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))

	// Inject V2 version tag into ServiceMeta (not persisted, memory only)
	sess.ServiceMeta = map[string]string{ServiceMetaVersionKey: VersionV2}

	return sess, nil
}

// =============================================================================
// Summary Operations
// =============================================================================

// v2SummaryHashField is the fixed hash field for V2 summary storage.
// V2 uses a per-session key, so we use a fixed field name instead of sessionID.
const v2SummaryHashField = "data"

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

	sumKey := c.keys.SummaryKey(key)
	hashField := v2SummaryHashField

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
	sumKey := c.keys.SummaryKey(key)
	hashField := v2SummaryHashField

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

// =============================================================================
// Track Event Operations
// =============================================================================

// AppendTrackEvent persists a track event to V2 storage.
// Track events are stored in a ZSet with timestamp as score.
// Format: v2:track:{appName:userID}:sessionID:trackName
func (c *Client) AppendTrackEvent(ctx context.Context, key session.Key, trackEvent *session.TrackEvent) error {
	// Get current session state to update tracks list
	metaJSON, err := c.client.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("session not found")
		}
		return fmt.Errorf("get session meta (v2): %w", err)
	}

	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return fmt.Errorf("unmarshal session meta: %w", err)
	}

	// Update session state with track list
	sess := &session.Session{
		ID:      key.SessionID,
		AppName: key.AppName,
		UserID:  key.UserID,
		State:   meta.State,
	}
	if err := sess.AppendTrackEvent(trackEvent); err != nil {
		return err
	}
	meta.State = sess.SnapshotState()
	meta.UpdatedAt = sess.UpdatedAt

	updatedMetaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal session meta: %w", err)
	}

	eventJSON, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf("marshal track event: %w", err)
	}

	trackKey := c.keys.TrackKey(key, trackEvent.Track)

	// Use pipeline for atomic update
	pipe := c.client.TxPipeline()
	// Update session meta (includes tracks list in state)
	pipe.Set(ctx, c.keys.SessionMetaKey(key), updatedMetaJSON, c.cfg.SessionTTL)
	// Add track event to ZSet
	pipe.ZAdd(ctx, trackKey, redis.Z{
		Score:  float64(trackEvent.Timestamp.UnixNano()),
		Member: eventJSON,
	})
	// Set TTL for track key
	if c.cfg.SessionTTL > 0 {
		pipe.Expire(ctx, trackKey, c.cfg.SessionTTL)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("append track event (v2): %w", err)
	}
	return nil
}

// GetTrackEvents retrieves track events for a session.
func (c *Client) GetTrackEvents(
	ctx context.Context,
	key session.Key,
	tracks []session.Track,
	limit int,
	afterTime time.Time,
) (map[session.Track][]session.TrackEvent, error) {
	if len(tracks) == 0 {
		return make(map[session.Track][]session.TrackEvent), nil
	}

	minScore := fmt.Sprintf("%d", afterTime.UnixNano())
	maxScore := fmt.Sprintf("%d", time.Now().UnixNano())

	type trackQuery struct {
		track session.Track
		cmd   *redis.StringSliceCmd
	}

	queries := make([]*trackQuery, 0, len(tracks))
	pipe := c.client.Pipeline()

	for _, track := range tracks {
		trackKey := c.keys.TrackKey(key, track)
		zrangeBy := &redis.ZRangeBy{
			Min: minScore,
			Max: maxScore,
		}
		if limit > 0 {
			zrangeBy.Offset = 0
			zrangeBy.Count = int64(limit)
		}
		cmd := pipe.ZRevRangeByScore(ctx, trackKey, zrangeBy)
		if c.cfg.SessionTTL > 0 {
			pipe.Expire(ctx, trackKey, c.cfg.SessionTTL)
		}
		queries = append(queries, &trackQuery{track: track, cmd: cmd})
	}

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get track events (v2): %w", err)
	}

	results := make(map[session.Track][]session.TrackEvent)
	for _, q := range queries {
		values, err := q.cmd.Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			return nil, fmt.Errorf("get track events: %w", err)
		}

		events := make([]session.TrackEvent, 0, len(values))
		for _, raw := range values {
			var evt session.TrackEvent
			if err := json.Unmarshal([]byte(raw), &evt); err != nil {
				continue
			}
			events = append(events, evt)
		}
		// Reverse to get chronological order (oldest first)
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
		results[q.track] = events
	}
	return results, nil
}

// ListTracksForSession returns the list of tracks from session state.
func (c *Client) ListTracksForSession(ctx context.Context, key session.Key) ([]session.Track, error) {
	metaJSON, err := c.client.Get(ctx, c.keys.SessionMetaKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get session meta (v2): %w", err)
	}

	var meta sessionMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal session meta: %w", err)
	}

	return session.TracksFromState(meta.State)
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
