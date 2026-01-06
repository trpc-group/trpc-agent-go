//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// getSession retrieves a single session with its events and summaries.
func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	// Query session state using FINAL for deduplication
	var sessState *SessionState
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT state, created_at, updated_at FROM %s FINAL 
			WHERE app_name = ? AND user_id = ? AND session_id = ? 
			AND (expires_at IS NULL OR expires_at > ?) AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID, time.Now())

	if err != nil {
		return nil, fmt.Errorf("get session state failed: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		var stateStr string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&stateStr, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		sessState = &SessionState{}
		if err := json.Unmarshal([]byte(stateStr), sessState); err != nil {
			return nil, fmt.Errorf("unmarshal session state failed: %w", err)
		}
		sessState.CreatedAt = createdAt
		sessState.UpdatedAt = updatedAt
		log.Debugf("getSession found session state: app=%s, user=%s, session=%s", key.AppName, key.UserID, key.SessionID)
	}

	if sessState == nil {
		log.Debugf("getSession found no session: app=%s, user=%s, session=%s", key.AppName, key.UserID, key.SessionID)
		return nil, nil
	}

	// Query app state
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}

	// Query user state
	userState, err := s.ListUserStates(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	if err != nil {
		return nil, err
	}

	// Batch load events for all sessions
	// Pass session created_at to filter out events from previous session instances
	eventsList, err := s.getEventsList(ctx, []session.Key{key}, []time.Time{sessState.CreatedAt}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}
	events := eventsList[0]

	// Query summaries
	summaries := make(map[string]*session.Summary)
	if len(events) > 0 {
		summaries, err = s.getSummary(ctx, key, sessState.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("get summaries failed: %w", err)
		}
	}

	sess := session.NewSession(
		key.AppName, key.UserID, sessState.ID,
		session.WithSessionState(sessState.State),
		session.WithSessionEvents(events),
		session.WithSessionSummaries(summaries),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)

	return mergeState(appState, userState, sess), nil
}

// listSessions lists all sessions for a user.
func (s *Service) listSessions(
	ctx context.Context,
	key session.UserKey,
	limit int,
	afterTime time.Time,
) ([]*session.Session, error) {
	// Query app state
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}

	// Query user state
	userState, err := s.ListUserStates(ctx, key)
	if err != nil {
		return nil, err
	}

	// Query all session states for this user using FINAL
	var sessStates []*SessionState
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT session_id, state, created_at, updated_at FROM %s FINAL
			WHERE app_name = ? AND user_id = ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND deleted_at IS NULL
			ORDER BY updated_at DESC`, s.tableSessionStates),
		key.AppName, key.UserID, time.Now())

	if err != nil {
		return nil, fmt.Errorf("list session states failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID string
		var stateStr string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&sessionID, &stateStr, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		var state SessionState
		if err := json.Unmarshal([]byte(stateStr), &state); err != nil {
			return nil, fmt.Errorf("unmarshal session state failed: %w", err)
		}
		state.ID = sessionID
		state.CreatedAt = createdAt
		state.UpdatedAt = updatedAt
		sessStates = append(sessStates, &state)
	}

	// Build session keys and created_at times for batch loading
	sessionKeys := make([]session.Key, 0, len(sessStates))
	sessionCreatedAts := make([]time.Time, 0, len(sessStates))
	for _, sessState := range sessStates {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: sessState.ID,
		})
		sessionCreatedAts = append(sessionCreatedAts, sessState.CreatedAt)
	}

	// Batch load events for all sessions
	// Pass session created_at to filter out events from previous session instances
	eventsList, err := s.getEventsList(ctx, sessionKeys, sessionCreatedAts, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events list failed: %w", err)
	}

	// Batch load summaries for all sessions
	summariesList, err := s.getSummariesList(ctx, sessionKeys, sessionCreatedAts)
	if err != nil {
		return nil, fmt.Errorf("get summaries list failed: %w", err)
	}

	sessions := make([]*session.Session, 0, len(sessStates))
	for i, sessState := range sessStates {
		var summaries map[string]*session.Summary
		if len(eventsList[i]) > 0 {
			summaries = summariesList[i]
		}
		sess := session.NewSession(
			key.AppName, key.UserID, sessState.ID,
			session.WithSessionState(sessState.State),
			session.WithSessionEvents(eventsList[i]),
			session.WithSessionSummaries(summaries),
			session.WithSessionCreatedAt(sessState.CreatedAt),
			session.WithSessionUpdatedAt(sessState.UpdatedAt),
		)
		sessions = append(sessions, mergeState(appState, userState, sess))
	}

	return sessions, nil
}

// addEvent adds an event to a session.
func (s *Service) addEvent(ctx context.Context, key session.Key, evt *event.Event) error {
	now := time.Now()

	// Get current session state using FINAL
	var stateStr string
	var createdAt time.Time
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT state, created_at FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("session not found")
	}
	if err := rows.Scan(&stateStr, &createdAt); err != nil {
		return fmt.Errorf("scan session state failed: %w", err)
	}

	var sessState SessionState
	if err := json.Unmarshal([]byte(stateStr), &sessState); err != nil {
		return fmt.Errorf("unmarshal session state failed: %w", err)
	}

	sessState.UpdatedAt = now
	sessState.CreatedAt = createdAt
	if sessState.State == nil {
		sessState.State = make(session.StateMap)
	}
	session.ApplyEventStateDeltaMap(sessState.State, evt)
	updatedStateBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session state failed: %w", err)
	}

	eventBytes, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event failed: %w", err)
	}

	expiresAt := calculateExpiresAt(s.opts.sessionTTL)

	// Insert new version of session state (ReplacingMergeTree will deduplicate)
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, state, extra_data, created_at, updated_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID, string(updatedStateBytes), "{}", sessState.CreatedAt, sessState.UpdatedAt, expiresAt)
	if err != nil {
		return fmt.Errorf("update session state failed: %w", err)
	}

	// Insert event if it has response and is not partial
	// Events do not have their own expires_at; they are filtered by session's created_at.
	// Use UnixMicro to preserve microsecond precision (ClickHouse driver has precision loss issue #1545).
	if evt.Response != nil && !evt.IsPartial && evt.IsValidContent() {
		eventNowMicro := time.Now().UnixMicro()
		err = s.chClient.Exec(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, event_id, event, extra_data, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, fromUnixTimestamp64Micro(?), fromUnixTimestamp64Micro(?))`, s.tableSessionEvents),
			key.AppName, key.UserID, key.SessionID, evt.ID, string(eventBytes), "{}", eventNowMicro, eventNowMicro)
		if err != nil {
			return fmt.Errorf("insert event failed: %w", err)
		}
	}

	return nil
}

// refreshSessionTTL updates the session's updated_at and expires_at timestamps.
func (s *Service) refreshSessionTTL(ctx context.Context, key session.Key) error {
	now := time.Now()
	expiresAt := now.Add(s.opts.sessionTTL)

	// Get current session state to preserve other fields
	var stateStr string
	var createdAt time.Time
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT state, created_at FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("session not found")
	}
	if err := rows.Scan(&stateStr, &createdAt); err != nil {
		return fmt.Errorf("scan session state failed: %w", err)
	}

	// Insert new version with updated timestamps
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, state, extra_data, created_at, updated_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID, stateStr, "{}", createdAt, now, expiresAt)

	if err != nil {
		return fmt.Errorf("refresh session TTL failed: %w", err)
	}
	return nil
}

// deleteSessionState soft-deletes a session and its related data.
// It inserts new versions with deleted_at set, which ReplacingMergeTree will use for deduplication.
func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	now := time.Now()

	// Get current session state to preserve fields for soft delete
	var stateStr string
	var createdAt, updatedAt time.Time
	var expiresAt *time.Time
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT state, created_at, updated_at, expires_at FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("get session state for delete failed: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		// Session not found or already deleted
		return nil
	}
	if err := rows.Scan(&stateStr, &createdAt, &updatedAt, &expiresAt); err != nil {
		return fmt.Errorf("scan session state failed: %w", err)
	}

	// Soft delete: INSERT new version with deleted_at set
	// ReplacingMergeTree will keep the latest version (with deleted_at)
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, state, extra_data, created_at, updated_at, expires_at, deleted_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID, stateStr, "{}", createdAt, now, expiresAt, now)
	if err != nil {
		return fmt.Errorf("soft delete session state failed: %w", err)
	}

	// Soft delete session events
	// Use INSERT INTO ... SELECT ... for batch soft delete
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, event_id, event, extra_data, created_at, updated_at, expires_at, deleted_at)
			SELECT app_name, user_id, session_id, event_id, event, extra_data, created_at, ? AS updated_at, expires_at, ? AS deleted_at
			FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionEvents, s.tableSessionEvents),
		now, now, key.AppName, key.UserID, key.SessionID)
	if err != nil {
		return fmt.Errorf("soft delete session events failed: %w", err)
	}

	// Soft delete session summaries
	// Use INSERT INTO ... SELECT ... for batch soft delete
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, created_at, updated_at, expires_at, deleted_at)
			SELECT app_name, user_id, session_id, filter_key, summary, created_at, ? AS updated_at, expires_at, ? AS deleted_at
			FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionSummaries, s.tableSessionSummaries),
		now, now, key.AppName, key.UserID, key.SessionID)
	if err != nil {
		return fmt.Errorf("soft delete session summaries failed: %w", err)
	}

	return nil
}

// getEventsList loads events for multiple sessions in batch.
// sessionCreatedAts contains the created_at time for each session, used to filter out
// events from previous session instances (when a session expires and is recreated with the same ID).
func (s *Service) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionCreatedAts []time.Time,
	limit int,
	afterTime time.Time,
) ([][]event.Event, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	// Build query with multiple conditions
	// Each condition includes session key AND event.created_at >= session.created_at
	conditions := make([]string, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys)*4)

	for i, key := range sessionKeys {
		conditions[i] = "(app_name = ? AND user_id = ? AND session_id = ? AND created_at >= ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID, sessionCreatedAts[i])
	}

	query := fmt.Sprintf(`SELECT app_name, user_id, session_id, event FROM %s FINAL
		WHERE (%s)
		AND deleted_at IS NULL
		ORDER BY app_name, user_id, session_id, created_at ASC`,
		s.tableSessionEvents, strings.Join(conditions, " OR "))

	// Map to collect events by session
	eventsMap := make(map[string][]event.Event)

	rows, err := s.chClient.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch get events failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var appName, userID, sessionID string
		var eventStr string
		if err := rows.Scan(&appName, &userID, &sessionID, &eventStr); err != nil {
			return nil, err
		}
		var evt event.Event
		if err := json.Unmarshal([]byte(eventStr), &evt); err != nil {
			return nil, fmt.Errorf("unmarshal event failed: %w", err)
		}
		key := fmt.Sprintf("%s:%s:%s", appName, userID, sessionID)
		eventsMap[key] = append(eventsMap[key], evt)
	}

	if limit <= 0 {
		limit = s.opts.sessionEventLimit
	}

	// Build result in same order as sessionKeys
	result := make([][]event.Event, len(sessionKeys))
	for i, key := range sessionKeys {
		keyStr := fmt.Sprintf("%s:%s:%s", key.AppName, key.UserID, key.SessionID)
		sess := session.Session{
			Events: eventsMap[keyStr],
		}
		sess.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))
		result[i] = sess.Events
	}

	return result, nil
}

// getSummary loads summaries for a single session.
func (s *Service) getSummary(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
) (map[string]*session.Summary, error) {
	summaries := make(map[string]*session.Summary)

	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT filter_key, summary FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND updated_at >= ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, sessionCreatedAt, time.Now())

	if err != nil {
		return nil, fmt.Errorf("get summaries failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var filterKey string
		var summaryStr string
		if err := rows.Scan(&filterKey, &summaryStr); err != nil {
			return nil, err
		}
		var sum session.Summary
		if err := json.Unmarshal([]byte(summaryStr), &sum); err != nil {
			return nil, fmt.Errorf("unmarshal summary failed: %w", err)
		}
		summaries[filterKey] = &sum
	}

	return summaries, nil
}

// getSummariesList loads summaries for multiple sessions of the same user in batch.
// It queries by app_name + user_id, then filters in memory by each session's createdAt.
func (s *Service) getSummariesList(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionCreatedAts []time.Time,
) ([]map[string]*session.Summary, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}
	if len(sessionKeys) != len(sessionCreatedAts) {
		return nil, fmt.Errorf("session keys and createdAts length mismatch")
	}

	// All sessions belong to the same user (from listSessions context)
	appName := sessionKeys[0].AppName
	userID := sessionKeys[0].UserID

	// Build sessionCreatedAt lookup map for filtering
	sessionCreatedAtMap := make(map[string]time.Time, len(sessionKeys))
	for i, key := range sessionKeys {
		sessionCreatedAtMap[key.SessionID] = sessionCreatedAts[i]
	}

	// Query all summaries for this user, filter by session createdAt in memory
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT session_id, filter_key, summary, updated_at FROM %s FINAL
			WHERE app_name = ? AND user_id = ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND deleted_at IS NULL`, s.tableSessionSummaries),
		appName, userID, time.Now())

	if err != nil {
		return nil, fmt.Errorf("batch get summaries failed: %w", err)
	}
	defer rows.Close()

	// Map to collect summaries by session
	summariesMap := make(map[string]map[string]*session.Summary)

	for rows.Next() {
		var sessionID, filterKey string
		var summaryStr string
		var updatedAt time.Time
		if err := rows.Scan(&sessionID, &filterKey, &summaryStr, &updatedAt); err != nil {
			return nil, err
		}

		// Filter by session createdAt to avoid cross-session leakage
		createdAt, exists := sessionCreatedAtMap[sessionID]
		if !exists || updatedAt.Before(createdAt) {
			continue
		}

		var sum session.Summary
		if err := json.Unmarshal([]byte(summaryStr), &sum); err != nil {
			return nil, fmt.Errorf("unmarshal summary failed: %w", err)
		}
		if summariesMap[sessionID] == nil {
			summariesMap[sessionID] = make(map[string]*session.Summary)
		}
		summariesMap[sessionID][filterKey] = &sum
	}

	// Build result in same order as sessionKeys
	result := make([]map[string]*session.Summary, len(sessionKeys))
	for i, key := range sessionKeys {
		summaries := summariesMap[key.SessionID]
		if summaries == nil {
			summaries = make(map[string]*session.Summary)
		}
		result[i] = summaries
	}

	return result, nil
}
