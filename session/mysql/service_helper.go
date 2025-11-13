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
	stateArgs := []interface{}{key.AppName, key.UserID, key.SessionID, time.Now()}

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

	// Query events
	events := []event.Event{}
	now := time.Now()
	var eventQuery string
	var eventArgs []interface{}

	if limit > 0 {
		eventQuery = fmt.Sprintf(`SELECT event FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND created_at > ?
			AND deleted_at IS NULL
			ORDER BY created_at DESC
			LIMIT ?`, s.tableSessionEvents)
		eventArgs = []interface{}{key.AppName, key.UserID, key.SessionID, now, afterTime, limit}
	} else {
		eventQuery = fmt.Sprintf(`SELECT event FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND created_at > ?
			AND deleted_at IS NULL
			ORDER BY created_at DESC`, s.tableSessionEvents)
		eventArgs = []interface{}{key.AppName, key.UserID, key.SessionID, now, afterTime}
	}

	err = s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop, so we just scan directly
		var eventBytes []byte
		if err := rows.Scan(&eventBytes); err != nil {
			return err
		}
		var evt event.Event
		if err := json.Unmarshal(eventBytes, &evt); err != nil {
			return fmt.Errorf("unmarshal event failed: %w", err)
		}
		events = append(events, evt)
		return nil
	}, eventQuery, eventArgs...)

	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}

	// Reverse events to get chronological order
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	// Query summaries
	summaries := make(map[string]*session.Summary)
	summaryQuery := fmt.Sprintf(`SELECT filter_key, summary FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND (expires_at IS NULL OR expires_at > ?)
		AND deleted_at IS NULL`, s.tableSessionSummaries)
	summaryArgs := []interface{}{key.AppName, key.UserID, key.SessionID, time.Now()}

	err = s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop, so we just scan directly
		var filterKey string
		var summaryBytes []byte
		if err := rows.Scan(&filterKey, &summaryBytes); err != nil {
			return err
		}
		var sum session.Summary
		if err := json.Unmarshal(summaryBytes, &sum); err != nil {
			return fmt.Errorf("unmarshal summary failed: %w", err)
		}
		summaries[filterKey] = &sum
		return nil
	}, summaryQuery, summaryArgs...)

	if err != nil {
		return nil, fmt.Errorf("get summaries failed: %w", err)
	}

	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     sessState.State,
		Events:    events,
		Summaries: summaries,
		UpdatedAt: sessState.UpdatedAt,
		CreatedAt: sessState.CreatedAt,
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
	listArgs := []interface{}{key.AppName, key.UserID, time.Now()}

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

	sessions := make([]*session.Session, 0, len(sessStates))
	for i, sessState := range sessStates {
		sess := &session.Session{
			ID:        sessState.ID,
			AppName:   key.AppName,
			UserID:    key.UserID,
			State:     sessState.State,
			Events:    eventsList[i],
			Summaries: summariesList[i],
			UpdatedAt: sessState.UpdatedAt,
			CreatedAt: sessState.CreatedAt,
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
				fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, event, created_at, updated_at, expires_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`, s.tableSessionEvents),
				key.AppName, key.UserID, key.SessionID, eventBytes, now, now, expiresAt)
			if err != nil {
				return fmt.Errorf("insert event failed: %w", err)
			}

			// Enforce event limit if configured
			if s.opts.sessionEventLimit > 0 {
				if err := s.enforceEventLimit(ctx, tx, key, now); err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("store event failed: %w", err)
	}
	return nil
}

// enforceEventLimit removes old events beyond the configured limit (MySQL syntax).
func (s *Service) enforceEventLimit(ctx context.Context, tx *sql.Tx, key session.Key, now time.Time) error {
	// MySQL approach: use NOT IN with IDs from subquery to avoid same-table restrictions
	// First, get IDs of events to keep (Nth newest)
	var cutoffCreatedAt time.Time
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf("SELECT created_at FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1 OFFSET ?",
			s.tableSessionEvents),
		key.AppName, key.UserID, key.SessionID, s.opts.sessionEventLimit).Scan(&cutoffCreatedAt)

	// If no cutoff time found (fewer events than limit), nothing to delete
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get cutoff time failed: %w", err)
	}

	if s.opts.softDelete {
		// Soft delete: mark events older than the cutoff time
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET deleted_at = ? WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL AND created_at < ?",
				s.tableSessionEvents),
			now, key.AppName, key.UserID, key.SessionID, cutoffCreatedAt)
		if err != nil {
			return fmt.Errorf("soft delete old events failed: %w", err)
		}
	} else {
		// Hard delete: physically remove events older than the cutoff time
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND created_at < ?",
				s.tableSessionEvents),
			key.AppName, key.UserID, key.SessionID, cutoffCreatedAt)
		if err != nil {
			return fmt.Errorf("hard delete old events failed: %w", err)
		}
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
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("delete session state failed: %w", err)
	}
	return nil
}

// getEventsList批量加载多个 session 的 events.
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
	args := make([]interface{}, 0, len(sessionKeys)*3+2)

	for i, key := range sessionKeys {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}

	// Add additional args
	args = append(args, time.Now(), afterTime)

	var query string
	if limit > 0 {
		// Note: This query gets all events and we'll need to split by session
		query = fmt.Sprintf(`SELECT app_name, user_id, session_id, event FROM %s
			WHERE (app_name, user_id, session_id) IN (%s)
			AND (expires_at IS NULL OR expires_at > ?)
			AND created_at > ?
			AND deleted_at IS NULL
			ORDER BY app_name, user_id, session_id, created_at DESC`,
			s.tableSessionEvents, strings.Join(placeholders, ","))
	} else {
		query = fmt.Sprintf(`SELECT app_name, user_id, session_id, event FROM %s
			WHERE (app_name, user_id, session_id) IN (%s)
			AND (expires_at IS NULL OR expires_at > ?)
			AND created_at > ?
			AND deleted_at IS NULL
			ORDER BY app_name, user_id, session_id, created_at DESC`,
			s.tableSessionEvents, strings.Join(placeholders, ","))
	}

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

	// Build result in same order as sessionKeys
	result := make([][]event.Event, len(sessionKeys))
	for i, key := range sessionKeys {
		keyStr := fmt.Sprintf("%s:%s:%s", key.AppName, key.UserID, key.SessionID)
		events := eventsMap[keyStr]

		// Apply limit per session if configured
		if limit > 0 && len(events) > limit {
			events = events[:limit]
		}

		// Reverse to get chronological order
		for j, k := 0, len(events)-1; j < k; j, k = j+1, k-1 {
			events[j], events[k] = events[k], events[j]
		}

		result[i] = events
	}

	return result, nil
}

// getSummariesList 批量加载多个 session 的 summaries.
func (s *Service) getSummariesList(
	ctx context.Context,
	sessionKeys []session.Key,
) ([]map[string]*session.Summary, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	// Build IN clause for batch query
	placeholders := make([]string, len(sessionKeys))
	args := make([]interface{}, 0, len(sessionKeys)*3+1)

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
