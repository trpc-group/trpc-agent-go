//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"database/sql"
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
	// Query session state (MySQL syntax with ?)
	var sessState *SessionState
	stateQuery := fmt.Sprintf(`SELECT state, created_at, updated_at FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND (expires_at IS NULL OR expires_at > ?) AND deleted_at IS NULL`, s.tableSessionStates)
	stateArgs := []any{key.AppName, key.UserID, key.SessionID, time.Now()}

	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop, so we just scan directly
		var stateBytes []byte
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&stateBytes, &createdAt, &updatedAt); err != nil {
			return err
		}
		sessState = &SessionState{}
		if err := json.Unmarshal(stateBytes, sessState); err != nil {
			return fmt.Errorf("unmarshal session state failed: %w", err)
		}
		sessState.CreatedAt = createdAt
		sessState.UpdatedAt = updatedAt
		log.Debugf("getSession found session state: app=%s, user=%s, session=%s", key.AppName, key.UserID, key.SessionID)
		return nil
	}, stateQuery, stateArgs...)

	if err != nil {
		return nil, fmt.Errorf("get session state failed: %w", err)
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
	eventsList, err := s.getEventsList(ctx, []session.Key{key}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}
	events := eventsList[0]

	// Query summaries
	summaries := make(map[string]*session.Summary)
	if len(events) > 0 {
		// Batch load summaries for all sessions
		summariesList, err := s.getSummariesList(ctx, []session.Key{key})
		if err != nil {
			return nil, fmt.Errorf("get summaries failed: %w", err)
		}
		summaries = summariesList[0]
	}

	sess := session.NewSession(
		key.AppName, key.UserID, sessState.ID,
		session.WithSessionState(sessState.State),
		session.WithSessionEvents(events),
		session.WithSessionSummaries(summaries),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)

	trackEventsList, err := s.getTrackEvents(ctx, []session.Key{key}, []*SessionState{sessState}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get track events failed: %w", err)
	}
	if len(trackEventsList) > 0 && len(trackEventsList[0]) > 0 {
		sess.Tracks = make(map[session.Track]*session.TrackEvents, len(trackEventsList[0]))
		for trackName, history := range trackEventsList[0] {
			sess.Tracks[trackName] = &session.TrackEvents{
				Track:  trackName,
				Events: history,
			}
		}
	}

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

	// Query all session states for this user
	var sessStates []*SessionState
	listQuery := fmt.Sprintf(`SELECT session_id, state, created_at, updated_at FROM %s
		WHERE app_name = ? AND user_id = ?
		AND (expires_at IS NULL OR expires_at > ?)
		AND deleted_at IS NULL
		ORDER BY updated_at DESC`, s.tableSessionStates)
	listArgs := []any{key.AppName, key.UserID, time.Now()}

	err = s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop
		var sessionID string
		var stateBytes []byte
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&sessionID, &stateBytes, &createdAt, &updatedAt); err != nil {
			return err
		}
		var state SessionState
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			return fmt.Errorf("unmarshal session state failed: %w", err)
		}
		state.ID = sessionID
		state.CreatedAt = createdAt
		state.UpdatedAt = updatedAt
		sessStates = append(sessStates, &state)
		return nil
	}, listQuery, listArgs...)

	if err != nil {
		return nil, fmt.Errorf("list session states failed: %w", err)
	}

	// Build session keys for batch loading
	sessionKeys := make([]session.Key, 0, len(sessStates))
	for _, sessState := range sessStates {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: sessState.ID,
		})
	}

	// Batch load events for all sessions
	eventsList, err := s.getEventsList(ctx, sessionKeys, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events list failed: %w", err)
	}

	// Batch load summaries for all sessions
	summariesList, err := s.getSummariesList(ctx, sessionKeys)
	if err != nil {
		return nil, fmt.Errorf("get summaries list failed: %w", err)
	}

	// Batch load track events for all sessions.
	trackEvents, err := s.getTrackEvents(ctx, sessionKeys, sessStates, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get track events: %w", err)
	}
	if len(trackEvents) != len(sessStates) {
		return nil, fmt.Errorf("track events count mismatch: %d != %d", len(trackEvents), len(sessStates))
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
		if len(trackEvents[i]) > 0 {
			sess.Tracks = make(map[session.Track]*session.TrackEvents, len(trackEvents[i]))
			for trackName, history := range trackEvents[i] {
				sess.Tracks[trackName] = &session.TrackEvents{
					Track:  trackName,
					Events: history,
				}
			}
		}
		sessions = append(sessions, mergeState(appState, userState, sess))
	}

	return sessions, nil
}

// addEvent adds an event to a session (MySQL syntax).
func (s *Service) addEvent(ctx context.Context, key session.Key, event *event.Event) error {
	now := time.Now()

	// Get current session state
	var stateBytes []byte
	var currentExpiresAt sql.NullTime
	err := s.mysqlClient.QueryRow(ctx,
		[]any{&stateBytes, &currentExpiresAt},
		fmt.Sprintf(`SELECT state, expires_at FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err == sql.ErrNoRows {
		return fmt.Errorf("session not found")
	}
	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}

	var sessState SessionState
	if err := json.Unmarshal(stateBytes, &sessState); err != nil {
		return fmt.Errorf("unmarshal session state failed: %w", err)
	}

	// Check if session is expired
	if currentExpiresAt.Valid && currentExpiresAt.Time.Before(now) {
		log.Infof("appending event to expired session (app=%s, user=%s, session=%s), will extend expires_at",
			key.AppName, key.UserID, key.SessionID)
	}

	sessState.UpdatedAt = now
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

	expiresAt := calculateExpiresAt(s.sessionTTL)

	// Use transaction to update session state and insert event
	err = s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		// Update session state
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET state = ?, updated_at = ?, expires_at = ?
			 WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
			updatedStateBytes, sessState.UpdatedAt, expiresAt,
			key.AppName, key.UserID, key.SessionID)
		if err != nil {
			return fmt.Errorf("update session state failed: %w", err)
		}

		// Insert event if it has response and is not partial
		if event.Response != nil && !event.IsPartial && event.IsValidContent() {
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, event, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?)`, s.tableSessionEvents),
				key.AppName, key.UserID, key.SessionID, eventBytes, now, now)
			if err != nil {
				return fmt.Errorf("insert event failed: %w", err)
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("store event failed: %w", err)
	}
	return nil
}

// addTrackEvent adds a track event to a session (MySQL syntax).
func (s *Service) addTrackEvent(ctx context.Context, key session.Key, trackEvent *session.TrackEvent) error {
	now := time.Now()

	// Get current session state.
	var stateBytes []byte
	var currentExpiresAt sql.NullTime
	err := s.mysqlClient.QueryRow(ctx,
		[]any{&stateBytes, &currentExpiresAt},
		fmt.Sprintf(`SELECT state, expires_at FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err == sql.ErrNoRows {
		return fmt.Errorf("session not found")
	}
	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}

	var sessState SessionState
	if err := json.Unmarshal(stateBytes, &sessState); err != nil {
		return fmt.Errorf("unmarshal session state failed: %w", err)
	}

	// Check if session is expired.
	if currentExpiresAt.Valid && currentExpiresAt.Time.Before(now) {
		log.Infof("appending track event to expired session (app=%s, user=%s, session=%s), will extend expires_at",
			key.AppName, key.UserID, key.SessionID)
	}

	// Update session state.
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

	expiresAt := calculateExpiresAt(s.sessionTTL)

	// Use transaction to update session state and insert track event.
	err = s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		// Update session state.
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET state = ?, updated_at = ?, expires_at = ?
			 WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
			updatedStateBytes, sessState.UpdatedAt, expiresAt,
			key.AppName, key.UserID, key.SessionID)
		if err != nil {
			return fmt.Errorf("update session state failed: %w", err)
		}

		// Insert track event.
		_, err = tx.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, track, event, created_at, updated_at, expires_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionTracks),
			key.AppName, key.UserID, key.SessionID, trackEvent.Track, eventBytes,
			trackEvent.Timestamp, trackEvent.Timestamp, expiresAt)
		if err != nil {
			return fmt.Errorf("insert track event failed: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store track event failed: %w", err)
	}
	return nil
}

// refreshSessionTTL updates the session's updated_at and expires_at timestamps.
func (s *Service) refreshSessionTTL(ctx context.Context, key session.Key) error {
	now := time.Now()
	expiresAt := now.Add(s.sessionTTL)

	_, err := s.mysqlClient.Exec(ctx,
		fmt.Sprintf(`UPDATE %s
		SET updated_at = ?, expires_at = ?
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND deleted_at IS NULL`, s.tableSessionStates),
		now, expiresAt, key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("refresh session TTL failed: %w", err)
	}
	return nil
}

// deleteSessionState deletes a session and its related data.
func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		if s.opts.softDelete {
			// Soft delete: set deleted_at timestamp
			now := time.Now()

			// Soft delete session state
			_, err := tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = ?
				 WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionStates),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session summaries
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = ?
				 WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionSummaries),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session events
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = ?
				 WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionEvents),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session track events.
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = ?
				 WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`, s.tableSessionTracks),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}
		} else {
			// Hard delete: permanently remove records

			// Delete session state
			_, err := tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = ? AND user_id = ? AND session_id = ?`, s.tableSessionStates),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session summaries
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = ? AND user_id = ? AND session_id = ?`, s.tableSessionSummaries),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session events
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = ? AND user_id = ? AND session_id = ?`, s.tableSessionEvents),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session track events.
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = ? AND user_id = ? AND session_id = ?`, s.tableSessionTracks),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("delete session state failed: %w", err)
	}
	return nil
}

// getEventsList loads events for multiple sessions in batch.
func (s *Service) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	limit int,
	afterTime time.Time,
) ([][]event.Event, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	// Build IN clause for batch query (MySQL doesn't support arrays like PostgreSQL)
	// We'll use (app_name, user_id, session_id) IN (...) pattern
	placeholders := make([]string, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys)*3)

	for i, key := range sessionKeys {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}

	// Note: We cannot apply LIMIT in SQL because we're querying multiple sessions
	// The limit is applied per session in memory after grouping by session key
	query := fmt.Sprintf(`SELECT app_name, user_id, session_id, event FROM %s
		WHERE (app_name, user_id, session_id) IN (%s)
		AND deleted_at IS NULL
		ORDER BY app_name, user_id, session_id, created_at ASC`,
		s.tableSessionEvents, strings.Join(placeholders, ","))

	// Map to collect events by session
	eventsMap := make(map[string][]event.Event)

	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop
		var appName, userID, sessionID string
		var eventBytes []byte
		if err := rows.Scan(&appName, &userID, &sessionID, &eventBytes); err != nil {
			return err
		}
		var evt event.Event
		if err := json.Unmarshal(eventBytes, &evt); err != nil {
			return fmt.Errorf("unmarshal event failed: %w", err)
		}
		key := fmt.Sprintf("%s:%s:%s", appName, userID, sessionID)
		eventsMap[key] = append(eventsMap[key], evt)
		return nil
	}, query, args...)

	if err != nil {
		return nil, fmt.Errorf("batch get events failed: %w", err)
	}

	if limit <= 0 {
		limit = s.opts.sessionEventLimit
	}
	if afterTime.IsZero() && s.opts.sessionTTL > 0 {
		afterTime = time.Now().Add(-s.opts.sessionTTL)
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

// getTrackEvents loads track events for multiple sessions in batch.
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
	if len(sessionStates) != len(sessionKeys) {
		return nil, fmt.Errorf("session states count mismatch: %d != %d", len(sessionStates), len(sessionKeys))
	}

	type trackQuery struct {
		sessionIdx int
		track      session.Track
		query      string
		args       []any
	}

	queries := make([]*trackQuery, 0)
	now := time.Now()
	for i, key := range sessionKeys {
		tracks, err := session.TracksFromState(sessionStates[i].State)
		if err != nil {
			return nil, fmt.Errorf("get track list failed: %w", err)
		}
		for _, track := range tracks {
			var query string
			var args []any
			if limit > 0 {
				query = fmt.Sprintf(`SELECT event FROM %s
					WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ?
					AND (expires_at IS NULL OR expires_at > ?)
					AND created_at > ?
					AND deleted_at IS NULL
					ORDER BY created_at DESC
					LIMIT ?`, s.tableSessionTracks)
				args = []any{key.AppName, key.UserID, key.SessionID, track, now, afterTime, limit}
			} else {
				query = fmt.Sprintf(`SELECT event FROM %s
					WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ?
					AND (expires_at IS NULL OR expires_at > ?)
					AND created_at > ?
					AND deleted_at IS NULL
					ORDER BY created_at DESC`, s.tableSessionTracks)
				args = []any{key.AppName, key.UserID, key.SessionID, track, now, afterTime}
			}
			queries = append(queries, &trackQuery{
				sessionIdx: i,
				track:      track,
				query:      query,
				args:       args,
			})
		}
	}

	results := make([]map[session.Track][]session.TrackEvent, len(sessionKeys))
	for _, q := range queries {
		events := make([]session.TrackEvent, 0)
		err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
			var eventBytes []byte
			if err := rows.Scan(&eventBytes); err != nil {
				return err
			}
			var evt session.TrackEvent
			if err := json.Unmarshal(eventBytes, &evt); err != nil {
				return fmt.Errorf("unmarshal track event failed: %w", err)
			}
			events = append(events, evt)
			return nil
		}, q.query, q.args...)
		if err != nil {
			return nil, fmt.Errorf("query track events failed: %w", err)
		}

		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
		if results[q.sessionIdx] == nil {
			results[q.sessionIdx] = make(map[session.Track][]session.TrackEvent)
		}
		results[q.sessionIdx][q.track] = events
	}
	for i := range results {
		if results[i] == nil {
			results[i] = make(map[session.Track][]session.TrackEvent)
		}
	}
	return results, nil
}

// getSummariesList loads summaries for multiple sessions in batch.
func (s *Service) getSummariesList(
	ctx context.Context,
	sessionKeys []session.Key,
) ([]map[string]*session.Summary, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	// Build IN clause for batch query
	placeholders := make([]string, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys)*3+1)

	for i, key := range sessionKeys {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}

	args = append(args, time.Now())

	query := fmt.Sprintf(`SELECT app_name, user_id, session_id, filter_key, summary FROM %s
		WHERE (app_name, user_id, session_id) IN (%s)
		AND (expires_at IS NULL OR expires_at > ?)
		AND deleted_at IS NULL`,
		s.tableSessionSummaries, strings.Join(placeholders, ","))

	// Map to collect summaries by session
	summariesMap := make(map[string]map[string]*session.Summary)

	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop
		var appName, userID, sessionID, filterKey string
		var summaryBytes []byte
		if err := rows.Scan(&appName, &userID, &sessionID, &filterKey, &summaryBytes); err != nil {
			return err
		}
		var sum session.Summary
		if err := json.Unmarshal(summaryBytes, &sum); err != nil {
			return fmt.Errorf("unmarshal summary failed: %w", err)
		}
		key := fmt.Sprintf("%s:%s:%s", appName, userID, sessionID)
		if summariesMap[key] == nil {
			summariesMap[key] = make(map[string]*session.Summary)
		}
		summariesMap[key][filterKey] = &sum
		return nil
	}, query, args...)

	if err != nil {
		return nil, fmt.Errorf("batch get summaries failed: %w", err)
	}

	// Build result in same order as sessionKeys
	result := make([]map[string]*session.Summary, len(sessionKeys))
	for i, key := range sessionKeys {
		keyStr := fmt.Sprintf("%s:%s:%s", key.AppName, key.UserID, key.SessionID)
		summaries := summariesMap[keyStr]
		if summaries == nil {
			summaries = make(map[string]*session.Summary)
		}
		result[i] = summaries
	}

	return result, nil
}
