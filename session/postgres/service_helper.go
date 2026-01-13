//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	// Query session state (always filter deleted records)
	var sessState *SessionState
	stateQuery := fmt.Sprintf(`SELECT state, created_at, updated_at FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND (expires_at IS NULL OR expires_at > $4)
		AND deleted_at IS NULL`, s.tableSessionStates)
	stateArgs := []any{key.AppName, key.UserID, key.SessionID, time.Now()}

	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
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
		}
		return nil
	}, stateQuery, stateArgs...)

	if err != nil {
		return nil, fmt.Errorf("get session state failed: %w", err)
	}
	if sessState == nil {
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

	// Query events (always filter deleted records)
	// Note: limit here only controls how many events to return, not delete from database
	eventsList, err := s.getEventsList(ctx, []session.Key{key}, limit, afterTime)
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}
	events := eventsList[0]

	// Query summaries (always filter deleted records)
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
		key.AppName, key.UserID, key.SessionID,
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
			trackHistory := &session.TrackEvents{
				Track:  trackName,
				Events: history,
			}
			sess.Tracks[trackName] = trackHistory
		}
	}

	return mergeState(appState, userState, sess), nil
}

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

	// Query all session states for this user (always filter deleted records)
	var sessStates []*SessionState
	listQuery := fmt.Sprintf(`SELECT session_id, state, created_at, updated_at FROM %s
		WHERE app_name = $1 AND user_id = $2
		AND (expires_at IS NULL OR expires_at > $3)
		AND deleted_at IS NULL
		ORDER BY updated_at DESC`, s.tableSessionStates)
	listArgs := []any{key.AppName, key.UserID, time.Now()}

	err = s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
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
		}
		return nil
	}, listQuery, listArgs...)

	if err != nil {
		return nil, fmt.Errorf("list session states failed: %w", err)
	}

	// Build session keys for batch loading events and summaries
	sessionKeys := make([]session.Key, 0, len(sessStates))
	for _, sessState := range sessStates {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: sessState.ID,
		})
	}

	// Batch load events for all sessions
	// Note: limit here only controls how many events to return per session, not delete from database
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

func (s *Service) addEvent(ctx context.Context, key session.Key, event *event.Event) error {
	now := time.Now()

	// Get current session state (always filter deleted records, but allow expired sessions).
	var sessState *SessionState
	var currentExpiresAt *time.Time
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			var stateBytes []byte
			if err := rows.Scan(&stateBytes, &currentExpiresAt); err != nil {
				return err
			}
			sessState = &SessionState{}
			if err := json.Unmarshal(stateBytes, sessState); err != nil {
				return fmt.Errorf("unmarshal session state failed: %w", err)
			}
		}
		return nil
	}, fmt.Sprintf(`SELECT state, expires_at FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}
	if sessState == nil {
		return fmt.Errorf("session not found")
	}

	// Check if session is expired, log info if so.
	if currentExpiresAt != nil && currentExpiresAt.Before(now) {
		log.InfofContext(
			ctx,
			"appending event to expired session (app=%s, user=%s, "+
				"session=%s), will extend expires_at",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
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

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := now.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	// Use transaction to update session state and insert event.
	err = s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		// Update session state
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET state = $1, updated_at = $2, expires_at = $3
			 WHERE app_name = $4 AND user_id = $5 AND session_id = $6 AND deleted_at IS NULL`, s.tableSessionStates),
			updatedStateBytes, sessState.UpdatedAt, expiresAt,
			key.AppName, key.UserID, key.SessionID)
		if err != nil {
			return fmt.Errorf("update session state failed: %w", err)
		}

		// Insert event if it has response and is not partial
		if event.Response != nil && !event.IsPartial && event.IsValidContent() {
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, event, created_at, updated_at)
				 VALUES ($1, $2, $3, $4, $5, $6)`, s.tableSessionEvents),
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

func (s *Service) addTrackEvent(ctx context.Context, key session.Key, trackEvent *session.TrackEvent) error {
	now := time.Now()

	// Get current session state (always filter deleted records, but allow expired sessions).
	var sessState *SessionState
	var currentExpiresAt *time.Time
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			var stateBytes []byte
			if err := rows.Scan(&stateBytes, &currentExpiresAt); err != nil {
				return err
			}
			sessState = &SessionState{}
			if err := json.Unmarshal(stateBytes, sessState); err != nil {
				return fmt.Errorf("unmarshal session state failed: %w", err)
			}
		}
		return nil
	}, fmt.Sprintf(`SELECT state, expires_at FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		AND deleted_at IS NULL`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("get session state failed: %w", err)
	}
	if sessState == nil {
		return fmt.Errorf("session not found")
	}

	// Check if session is expired, log info if so.
	if currentExpiresAt != nil && currentExpiresAt.Before(now) {
		log.InfofContext(
			ctx,
			"appending track event to expired session (app=%s, "+
				"user=%s, session=%s), will extend expires_at",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
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

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := now.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	// Use transaction to update session state and insert track event.
	err = s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		// Update session state.
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET state = $1, updated_at = $2, expires_at = $3
			 WHERE app_name = $4 AND user_id = $5 AND session_id = $6 AND deleted_at IS NULL`, s.tableSessionStates),
			updatedStateBytes, sessState.UpdatedAt, expiresAt,
			key.AppName, key.UserID, key.SessionID)
		if err != nil {
			return fmt.Errorf("update session state failed: %w", err)
		}

		// Insert track event.
		_, err = tx.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, track, event, created_at, updated_at, expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, s.tableSessionTracks),
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
// This effectively "renews" the session, extending its lifetime by the configured TTL.
func (s *Service) refreshSessionTTL(ctx context.Context, key session.Key) error {
	now := time.Now()
	expiresAt := now.Add(s.opts.sessionTTL)

	_, err := s.pgClient.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s
		SET updated_at = $1, expires_at = $2
		WHERE app_name = $3 AND user_id = $4 AND session_id = $5
		AND deleted_at IS NULL`, s.tableSessionStates),
		now, expiresAt, key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("refresh session TTL failed: %w", err)
	}
	return nil
}

func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	err := s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		if s.opts.softDelete {
			// Soft delete: set deleted_at timestamp
			now := time.Now()

			// Soft delete session state
			_, err := tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`, s.tableSessionStates),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session summaries
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`, s.tableSessionSummaries),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session events
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`, s.tableSessionEvents),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Soft delete session track events.
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET deleted_at = $1
				 WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`, s.tableSessionTracks),
				now, key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}
		} else {
			// Hard delete: permanently remove records

			// Delete session state
			_, err := tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`, s.tableSessionStates),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session summaries
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`, s.tableSessionSummaries),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session events
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`, s.tableSessionEvents),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}

			// Delete session track events.
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s
				 WHERE app_name = $1 AND user_id = $2 AND session_id = $3`, s.tableSessionTracks),
				key.AppName, key.UserID, key.SessionID)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("postgres session service delete session state failed: %w", err)
	}
	return nil
}

func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	// init event pair chan and track pair chan.
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	s.trackEventChans = make([]chan *trackEventPair, persisterNum)
	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(chan *sessionEventPair, defaultChanBufferSize)
		s.trackEventChans[i] = make(chan *trackEventPair, defaultChanBufferSize)
	}

	s.persistWg.Add(persisterNum * 2)
	for _, eventPairChan := range s.eventPairChans {
		go func(eventPairChan chan *sessionEventPair) {
			defer s.persistWg.Done()
			for pair := range eventPairChan {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				log.DebugfContext(
					ctx,
					"Session persistence queue monitoring: channel "+
						"capacity: %d, current length: %d, "+
						"session key:(app: %s, user: %s, session: %s)",
					cap(eventPairChan),
					len(eventPairChan),
					pair.key.AppName,
					pair.key.UserID,
					pair.key.SessionID,
				)
				if err := s.addEvent(ctx, pair.key, pair.event); err != nil {
					log.ErrorfContext(
						ctx,
						"postgres session service async persist "+
							"event failed: %v",
						err,
					)
				}
				cancel()
			}
		}(eventPairChan)
	}

	for _, trackPairChan := range s.trackEventChans {
		go func(trackPairChan chan *trackEventPair) {
			defer s.persistWg.Done()
			for pair := range trackPairChan {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				log.DebugfContext(
					ctx,
					"Session track persistence queue monitoring: "+
						"channel capacity: %d, current length: "+
						"%d, session key:(app: %s, user: %s, "+
						"session: %s)",
					cap(trackPairChan),
					len(trackPairChan),
					pair.key.AppName,
					pair.key.UserID,
					pair.key.SessionID,
				)
				if err := s.addTrackEvent(ctx, pair.key, pair.event); err != nil {
					log.ErrorfContext(
						ctx,
						"postgres session service async persist track "+
							"event failed: %v",
						err,
					)
				}
				cancel()
			}
		}(trackPairChan)
	}
}

func mergeState(appState, userState session.StateMap, sess *session.Session) *session.Session {
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
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

// getEventsList batch loads events for multiple sessions.
// Note: limit here only controls how many events to return per session, not delete from database
func (s *Service) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	limit int,
	afterTime time.Time,
) ([][]event.Event, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	// Build session IDs array
	sessionIDs := make([]string, len(sessionKeys))
	for i, key := range sessionKeys {
		sessionIDs[i] = key.SessionID
	}

	// Query events for all sessions
	query := fmt.Sprintf(`
			SELECT session_id, event
			FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND session_id = ANY($3::varchar[])
			AND deleted_at IS NULL
			ORDER BY session_id, created_at ASC`, s.tableSessionEvents)
	args := []any{sessionKeys[0].AppName, sessionKeys[0].UserID, sessionIDs}

	// Execute query and group events by session
	eventsMap := make(map[string][]event.Event)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var sessionID string
			var eventBytes []byte
			if err := rows.Scan(&sessionID, &eventBytes); err != nil {
				return err
			}

			// Skip null events (from LEFT JOIN when no events exist)
			if eventBytes == nil {
				continue
			}

			var evt event.Event
			if err := json.Unmarshal(eventBytes, &evt); err != nil {
				return fmt.Errorf("unmarshal event failed: %w", err)
			}
			eventsMap[sessionID] = append(eventsMap[sessionID], evt)
		}
		return nil
	}, query, args...)

	if err != nil {
		return nil, fmt.Errorf("query events failed: %w", err)
	}

	if limit <= 0 {
		limit = s.opts.sessionEventLimit
	}
	if afterTime.IsZero() && s.opts.sessionTTL > 0 {
		afterTime = time.Now().Add(-s.opts.sessionTTL)
	}
	// Build result list in the same order as sessionKeys
	result := make([][]event.Event, len(sessionKeys))
	for i, key := range sessionKeys {
		events := eventsMap[key.SessionID]
		if events == nil {
			result[i] = []event.Event{}
			continue
		}
		sess := &session.Session{
			Events: events,
		}
		sess.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))
		result[i] = sess.Events
	}

	return result, nil
}

// getTrackEvents batch loads track events for multiple tracks.
// Note: limit here only controls how many events to return per session, not delete from database.
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
					WHERE app_name = $1 AND user_id = $2 AND session_id = $3 AND track = $4
					AND (expires_at IS NULL OR expires_at > $5)
					AND created_at > $6
					AND deleted_at IS NULL
					ORDER BY created_at DESC
					LIMIT $7`, s.tableSessionTracks)
				args = []any{key.AppName, key.UserID, key.SessionID, track, now, afterTime, limit}
			} else {
				query = fmt.Sprintf(`SELECT event FROM %s
					WHERE app_name = $1 AND user_id = $2 AND session_id = $3 AND track = $4
					AND (expires_at IS NULL OR expires_at > $5)
					AND created_at > $6
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
		err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
			for rows.Next() {
				var eventBytes []byte
				if err := rows.Scan(&eventBytes); err != nil {
					return err
				}
				var event session.TrackEvent
				if err := json.Unmarshal(eventBytes, &event); err != nil {
					return fmt.Errorf("unmarshal track event failed: %w", err)
				}
				events = append(events, event)
			}
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

// getSummariesList batch loads summaries for multiple sessions.
func (s *Service) getSummariesList(
	ctx context.Context,
	sessionKeys []session.Key,
) ([]map[string]*session.Summary, error) {
	if len(sessionKeys) == 0 {
		return []map[string]*session.Summary{}, nil
	}

	// Build session IDs array
	sessionIDs := make([]string, len(sessionKeys))
	for i, key := range sessionKeys {
		sessionIDs[i] = key.SessionID
	}

	// Query all summaries for all sessions (always filter deleted records)
	summaryQuery := fmt.Sprintf(`SELECT session_id, filter_key, summary FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = ANY($3::varchar[])
		AND (expires_at IS NULL OR expires_at > $4)
		AND deleted_at IS NULL`, s.tableSessionSummaries)

	// Query all summaries for all sessions
	summariesMap := make(map[string]map[string]*session.Summary)
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var sessionID, filterKey string
			var summaryBytes []byte
			if err := rows.Scan(&sessionID, &filterKey, &summaryBytes); err != nil {
				return err
			}

			var sum session.Summary
			if err := json.Unmarshal(summaryBytes, &sum); err != nil {
				return fmt.Errorf("unmarshal summary failed: %w", err)
			}

			if summariesMap[sessionID] == nil {
				summariesMap[sessionID] = make(map[string]*session.Summary)
			}
			summariesMap[sessionID][filterKey] = &sum
		}
		return nil
	}, summaryQuery, sessionKeys[0].AppName, sessionKeys[0].UserID, sessionIDs, time.Now())

	if err != nil {
		return nil, fmt.Errorf("query summaries failed: %w", err)
	}

	// Build result list in the same order as sessionKeys
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
