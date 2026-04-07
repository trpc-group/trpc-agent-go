//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

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

// getSession retrieves a single session with its events
// and summaries.
func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	// Query session state.
	// Use NOW() AT TIME ZONE 'localtime' to get the server's local
	// time without timezone, matching the TIMESTAMP column type.
	var sessState *SessionState
	stateQuery := fmt.Sprintf(
		`SELECT state, created_at, updated_at
		FROM %s
		WHERE app_name = $1 AND user_id = $2
		AND session_id = $3
		AND (expires_at IS NULL OR expires_at > NOW() AT TIME ZONE 'localtime')
		AND deleted_at IS NULL`,
		s.tableSessionStates,
	)
	stateArgs := []any{
		key.AppName, key.UserID,
		key.SessionID,
	}

	err := s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			if rows.Next() {
				var stateBytes []byte
				var createdAt, updatedAt time.Time
				if err := rows.Scan(
					&stateBytes, &createdAt, &updatedAt,
				); err != nil {
					return err
				}
				sessState = &SessionState{}
				if err := json.Unmarshal(
					stateBytes, sessState,
				); err != nil {
					return fmt.Errorf(
						"unmarshal session state failed: %w",
						err,
					)
				}
				sessState.CreatedAt = createdAt
				sessState.UpdatedAt = updatedAt
			}
			return nil
		}, stateQuery, stateArgs...)
	if err != nil {
		return nil, fmt.Errorf(
			"get session state failed: %w", err,
		)
	}
	if sessState == nil {
		return nil, nil
	}

	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx,
		session.UserKey{
			AppName: key.AppName,
			UserID:  key.UserID,
		},
	)
	if err != nil {
		return nil, err
	}

	eventsList, err := s.getEventsList(
		ctx, []session.Key{key}, limit, afterTime,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get events failed: %w", err,
		)
	}
	events := eventsList[0]

	summaries := make(map[string]*session.Summary)
	summariesList, err := s.getSummariesList(
		ctx, []session.Key{key},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get summaries failed: %w", err,
		)
	}
	summaries = summariesList[0]

	sess := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(sessState.State),
		session.WithSessionEvents(events),
		session.WithSessionSummaries(summaries),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)

	trackEventsList, err := s.getTrackEvents(
		ctx, []session.Key{key},
		[]*SessionState{sessState}, limit, afterTime,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get track events failed: %w", err,
		)
	}
	if len(trackEventsList) > 0 &&
		len(trackEventsList[0]) > 0 {
		sess.Tracks = make(
			map[session.Track]*session.TrackEvents,
			len(trackEventsList[0]),
		)
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
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx, key)
	if err != nil {
		return nil, err
	}

	var sessStates []*SessionState
	listQuery := fmt.Sprintf(
		`SELECT session_id, state,
		created_at, updated_at
		FROM %s
		WHERE app_name = $1 AND user_id = $2
		AND (expires_at IS NULL OR expires_at > NOW() AT TIME ZONE 'localtime')
		AND deleted_at IS NULL
		ORDER BY updated_at DESC`,
		s.tableSessionStates,
	)
	listArgs := []any{
		key.AppName, key.UserID,
	}

	err = s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var sessionID string
				var stateBytes []byte
				var createdAt, updatedAt time.Time
				if err := rows.Scan(
					&sessionID, &stateBytes,
					&createdAt, &updatedAt,
				); err != nil {
					return err
				}
				var state SessionState
				if err := json.Unmarshal(
					stateBytes, &state,
				); err != nil {
					return fmt.Errorf(
						"unmarshal session state "+
							"failed: %w", err,
					)
				}
				state.ID = sessionID
				state.CreatedAt = createdAt
				state.UpdatedAt = updatedAt
				sessStates = append(sessStates, &state)
			}
			return nil
		}, listQuery, listArgs...)
	if err != nil {
		return nil, fmt.Errorf(
			"list session states failed: %w", err,
		)
	}

	sessionKeys := make(
		[]session.Key, 0, len(sessStates),
	)
	for _, st := range sessStates {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: st.ID,
		})
	}

	eventsList, err := s.getEventsList(
		ctx, sessionKeys, limit, afterTime,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get events list failed: %w", err,
		)
	}

	summariesList, err := s.getSummariesList(
		ctx, sessionKeys,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get summaries list failed: %w", err,
		)
	}

	trackEvents, err := s.getTrackEvents(
		ctx, sessionKeys, sessStates,
		limit, afterTime,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get track events: %w", err,
		)
	}
	if len(trackEvents) != len(sessStates) {
		return nil, fmt.Errorf(
			"track events count mismatch: %d != %d",
			len(trackEvents), len(sessStates),
		)
	}

	sessions := make(
		[]*session.Session, 0, len(sessStates),
	)
	for i, st := range sessStates {
		summaries := summariesList[i]
		sess := session.NewSession(
			key.AppName, key.UserID, st.ID,
			session.WithSessionState(st.State),
			session.WithSessionEvents(eventsList[i]),
			session.WithSessionSummaries(summaries),
			session.WithSessionCreatedAt(st.CreatedAt),
			session.WithSessionUpdatedAt(st.UpdatedAt),
		)
		if len(trackEvents[i]) > 0 {
			sess.Tracks = make(
				map[session.Track]*session.TrackEvents,
				len(trackEvents[i]),
			)
			for tn, h := range trackEvents[i] {
				sess.Tracks[tn] = &session.TrackEvents{
					Track:  tn,
					Events: h,
				}
			}
		}
		sessions = append(sessions,
			mergeState(appState, userState, sess),
		)
	}
	return sessions, nil
}

// addEvent adds an event to a session.
func (s *Service) addEvent(
	ctx context.Context,
	key session.Key,
	evt *event.Event,
) error {
	now := time.Now()
	eventBytes, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf(
			"marshal event failed: %w", err,
		)
	}

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := now.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	err = s.pgClient.Transaction(ctx,
		func(tx *sql.Tx) error {
			var (
				stateBytes       []byte
				currentExpiresAt *time.Time
				sessState        SessionState
			)
			row := tx.QueryRowContext(
				ctx,
				fmt.Sprintf(
					`SELECT state, expires_at FROM %s
					WHERE app_name = $1 AND user_id = $2
					AND session_id = $3
					AND deleted_at IS NULL
					FOR UPDATE`,
					s.tableSessionStates,
				),
				key.AppName, key.UserID, key.SessionID,
			)
			if err := row.Scan(
				&stateBytes, &currentExpiresAt,
			); err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("session not found")
				}
				return fmt.Errorf(
					"get session state failed: %w", err,
				)
			}
			if err := json.Unmarshal(
				stateBytes, &sessState,
			); err != nil {
				return fmt.Errorf(
					"unmarshal session state failed: %w",
					err,
				)
			}
			if currentExpiresAt != nil &&
				currentExpiresAt.Before(now) {
				log.InfofContext(ctx,
					"appending event to expired session "+
						"(app=%s, user=%s, session=%s), "+
						"will extend expires_at",
					key.AppName, key.UserID, key.SessionID,
				)
			}
			sessState.UpdatedAt = now
			if sessState.State == nil {
				sessState.State = make(session.StateMap)
			}
			session.ApplyEventStateDeltaMap(
				sessState.State, evt,
			)
			updatedStateBytes, err := json.Marshal(
				&sessState,
			)
			if err != nil {
				return fmt.Errorf(
					"marshal session state failed: %w",
					err,
				)
			}
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(
					`UPDATE %s SET state = $1,
					updated_at = $2, expires_at = $3
					WHERE app_name = $4
					AND user_id = $5
					AND session_id = $6
					AND deleted_at IS NULL`,
					s.tableSessionStates,
				),
				updatedStateBytes,
				sessState.UpdatedAt, expiresAt,
				key.AppName, key.UserID,
				key.SessionID,
			)
			if err != nil {
				return fmt.Errorf(
					"update session state failed: %w",
					err,
				)
			}

			if evt.Response != nil && !evt.IsPartial &&
				evt.IsValidContent() {
				_, err = tx.ExecContext(ctx,
					fmt.Sprintf(
						`INSERT INTO %s
						(app_name, user_id, session_id,
						 event, created_at, updated_at,
						 expires_at)
						VALUES
						($1, $2, $3, $4, $5, $6, $7)`,
						s.tableSessionEvents,
					),
					key.AppName, key.UserID,
					key.SessionID, eventBytes,
					now, now, expiresAt,
				)
				if err != nil {
					return fmt.Errorf(
						"insert event failed: %w", err,
					)
				}
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf(
			"store event failed: %w", err,
		)
	}
	return nil
}

// addTrackEvent adds a track event to a session.
func (s *Service) addTrackEvent(
	ctx context.Context,
	key session.Key,
	trackEvent *session.TrackEvent,
) error {
	now := time.Now()
	eventBytes, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf(
			"marshal track event failed: %w", err,
		)
	}

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := now.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	err = s.pgClient.Transaction(ctx,
		func(tx *sql.Tx) error {
			var (
				stateBytes       []byte
				currentExpiresAt *time.Time
				sessState        SessionState
			)
			row := tx.QueryRowContext(
				ctx,
				fmt.Sprintf(
					`SELECT state, expires_at FROM %s
					WHERE app_name = $1 AND user_id = $2
					AND session_id = $3
					AND deleted_at IS NULL
					FOR UPDATE`,
					s.tableSessionStates,
				),
				key.AppName, key.UserID, key.SessionID,
			)
			if err := row.Scan(
				&stateBytes, &currentExpiresAt,
			); err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("session not found")
				}
				return fmt.Errorf(
					"get session state failed: %w", err,
				)
			}
			if err := json.Unmarshal(
				stateBytes, &sessState,
			); err != nil {
				return fmt.Errorf(
					"unmarshal session state failed: %w",
					err,
				)
			}
			if currentExpiresAt != nil &&
				currentExpiresAt.Before(now) {
				log.InfofContext(ctx,
					"appending track event to expired "+
						"session (app=%s, user=%s, "+
						"session=%s), will extend expires_at",
					key.AppName, key.UserID, key.SessionID,
				)
			}
			sess := &session.Session{
				ID:      key.SessionID,
				AppName: key.AppName,
				UserID:  key.UserID,
				State:   sessState.State,
			}
			if err := sess.AppendTrackEvent(
				trackEvent,
			); err != nil {
				return err
			}
			sessState.State = sess.SnapshotState()
			sessState.UpdatedAt = sess.UpdatedAt
			updatedStateBytes, err := json.Marshal(
				&sessState,
			)
			if err != nil {
				return fmt.Errorf(
					"marshal session state failed: %w",
					err,
				)
			}
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(
					`UPDATE %s SET state = $1,
					updated_at = $2, expires_at = $3
					WHERE app_name = $4
					AND user_id = $5
					AND session_id = $6
					AND deleted_at IS NULL`,
					s.tableSessionStates,
				),
				updatedStateBytes,
				sessState.UpdatedAt, expiresAt,
				key.AppName, key.UserID,
				key.SessionID,
			)
			if err != nil {
				return fmt.Errorf(
					"update session state failed: %w",
					err,
				)
			}
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(
					`INSERT INTO %s
					(app_name, user_id, session_id,
					 track, event, created_at,
					 updated_at, expires_at)
					VALUES
					($1, $2, $3, $4, $5, $6, $7, $8)`,
					s.tableSessionTracks,
				),
				key.AppName, key.UserID,
				key.SessionID, trackEvent.Track,
				eventBytes, trackEvent.Timestamp,
				trackEvent.Timestamp, expiresAt,
			)
			if err != nil {
				return fmt.Errorf(
					"insert track event failed: %w",
					err,
				)
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf(
			"store track event failed: %w", err,
		)
	}
	return nil
}

// refreshSessionTTL updates the session's updated_at
// and expires_at timestamps.
func (s *Service) refreshSessionTTL(
	ctx context.Context,
	key session.Key,
) error {
	now := time.Now()
	expiresAt := now.Add(s.opts.sessionTTL)

	_, err := s.pgClient.ExecContext(ctx,
		fmt.Sprintf(
			`UPDATE %s
			SET updated_at = $1, expires_at = $2
			WHERE app_name = $3 AND user_id = $4
			AND session_id = $5
			AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		now, expiresAt,
		key.AppName, key.UserID, key.SessionID,
	)
	if err != nil {
		return fmt.Errorf(
			"refresh session TTL failed: %w", err,
		)
	}
	return nil
}

// deleteSessionState deletes session state plus related
// events, summaries and tracks in a single transaction.
func (s *Service) deleteSessionState(
	ctx context.Context,
	key session.Key,
) error {
	err := s.pgClient.Transaction(ctx,
		func(tx *sql.Tx) error {
			tables := []string{
				s.tableSessionStates,
				s.tableSessionSummaries,
				s.tableSessionEvents,
				s.tableSessionTracks,
			}
			if s.opts.softDelete {
				now := time.Now()
				for _, tbl := range tables {
					_, err := tx.ExecContext(ctx,
						fmt.Sprintf(
							`UPDATE %s
							SET deleted_at = $1
							WHERE app_name = $2
							AND user_id = $3
							AND session_id = $4
							AND deleted_at IS NULL`,
							tbl,
						),
						now, key.AppName,
						key.UserID, key.SessionID,
					)
					if err != nil {
						return err
					}
				}
			} else {
				for _, tbl := range tables {
					_, err := tx.ExecContext(ctx,
						fmt.Sprintf(
							`DELETE FROM %s
							WHERE app_name = $1
							AND user_id = $2
							AND session_id = $3`,
							tbl,
						),
						key.AppName,
						key.UserID, key.SessionID,
					)
					if err != nil {
						return err
					}
				}
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf(
			"pgvector session service delete session "+
				"state failed: %w", err,
		)
	}
	return nil
}

// startAsyncPersistWorker starts goroutine workers for
// asynchronous event and track event persistence.
func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	s.eventPairChans = make(
		[]chan *sessionEventPair, persisterNum,
	)
	s.trackEventChans = make(
		[]chan *trackEventPair, persisterNum,
	)
	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(
			chan *sessionEventPair,
			defaultChanBufferSize,
		)
		s.trackEventChans[i] = make(
			chan *trackEventPair,
			defaultChanBufferSize,
		)
	}

	s.persistWg.Add(persisterNum * 2)
	for _, ch := range s.eventPairChans {
		go func(eventCh chan *sessionEventPair) {
			defer s.persistWg.Done()
			for pair := range eventCh {
				ctx, cancel := context.WithTimeout(
					context.Background(),
					s.opts.embedTimeout,
				)
				log.DebugfContext(ctx,
					"Session persistence queue "+
						"monitoring: channel capacity: "+
						"%d, current length: %d, "+
						"session key:(app: %s, user: "+
						"%s, session: %s)",
					cap(eventCh), len(eventCh),
					pair.key.AppName,
					pair.key.UserID,
					pair.key.SessionID,
				)
				if err := s.addEvent(
					ctx, pair.key, pair.event,
				); err != nil {
					log.ErrorfContext(ctx,
						"pgvector session service "+
							"async persist event "+
							"failed: %v", err,
					)
				} else if shouldPersistEvent(pair.event) {
					sess := &session.Session{
						ID:      pair.key.SessionID,
						AppName: pair.key.AppName,
						UserID:  pair.key.UserID,
					}
					s.indexEventAfterPersist(
						sess, pair.event,
					)
				}
				cancel()
			}
		}(ch)
	}

	for _, ch := range s.trackEventChans {
		go func(trackCh chan *trackEventPair) {
			defer s.persistWg.Done()
			for pair := range trackCh {
				ctx, cancel := context.WithTimeout(
					context.Background(),
					s.opts.embedTimeout,
				)
				log.DebugfContext(ctx,
					"Session track persistence "+
						"queue monitoring: channel "+
						"capacity: %d, current "+
						"length: %d, session "+
						"key:(app: %s, user: %s, "+
						"session: %s)",
					cap(trackCh), len(trackCh),
					pair.key.AppName,
					pair.key.UserID,
					pair.key.SessionID,
				)
				if err := s.addTrackEvent(
					ctx, pair.key, pair.event,
				); err != nil {
					log.ErrorfContext(ctx,
						"pgvector session service "+
							"async persist track "+
							"event failed: %v", err,
					)
				}
				cancel()
			}
		}(ch)
	}
}

// getEventsList batch loads events for multiple sessions.
func (s *Service) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	limit int,
	afterTime time.Time,
) ([][]event.Event, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	sessionIDs := make([]string, len(sessionKeys))
	for i, key := range sessionKeys {
		sessionIDs[i] = key.SessionID
	}

	query := fmt.Sprintf(
		`SELECT session_id, event
		FROM %s
		WHERE app_name = $1 AND user_id = $2
		AND session_id = ANY($3::varchar[])
		AND deleted_at IS NULL
		ORDER BY session_id, created_at ASC`,
		s.tableSessionEvents,
	)
	args := []any{
		sessionKeys[0].AppName,
		sessionKeys[0].UserID,
		sessionIDs,
	}

	eventsMap := make(map[string][]event.Event)
	err := s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var sessionID string
				var eventBytes []byte
				if err := rows.Scan(
					&sessionID, &eventBytes,
				); err != nil {
					return err
				}
				if eventBytes == nil {
					continue
				}
				var evt event.Event
				if err := json.Unmarshal(
					eventBytes, &evt,
				); err != nil {
					return fmt.Errorf(
						"unmarshal event failed: %w",
						err,
					)
				}
				eventsMap[sessionID] = append(
					eventsMap[sessionID], evt,
				)
			}
			return nil
		}, query, args...)
	if err != nil {
		return nil, fmt.Errorf(
			"query events failed: %w", err,
		)
	}

	if limit <= 0 {
		limit = s.opts.sessionEventLimit
	}
	if afterTime.IsZero() && s.opts.sessionTTL > 0 {
		afterTime = time.Now().Add(-s.opts.sessionTTL)
	}

	result := make([][]event.Event, len(sessionKeys))
	for i, key := range sessionKeys {
		events := eventsMap[key.SessionID]
		if events == nil {
			result[i] = []event.Event{}
			continue
		}
		sess := &session.Session{Events: events}
		sess.ApplyEventFiltering(
			session.WithEventNum(limit),
			session.WithEventTime(afterTime),
		)
		result[i] = sess.Events
	}
	return result, nil
}

// getTrackEvents batch loads track events for multiple
// sessions.
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
		return nil, fmt.Errorf(
			"session states count mismatch: %d != %d",
			len(sessionStates), len(sessionKeys),
		)
	}

	type trackQuery struct {
		sessionIdx int
		track      session.Track
		query      string
		args       []any
	}
	queries := make([]*trackQuery, 0)
	for i, key := range sessionKeys {
		tracks, err := session.TracksFromState(
			sessionStates[i].State,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"get track list failed: %w", err,
			)
		}
		for _, track := range tracks {
			var q string
			var args []any
			if limit > 0 {
				q = fmt.Sprintf(
					`SELECT event FROM %s
					WHERE app_name = $1
					AND user_id = $2
					AND session_id = $3
					AND track = $4
					AND (expires_at IS NULL
						OR expires_at > NOW() AT TIME ZONE 'localtime')
					AND created_at > $5
					AND deleted_at IS NULL
					ORDER BY created_at DESC
					LIMIT $6`,
					s.tableSessionTracks,
				)
				args = []any{
					key.AppName, key.UserID,
					key.SessionID, track,
					afterTime, limit,
				}
			} else {
				q = fmt.Sprintf(
					`SELECT event FROM %s
					WHERE app_name = $1
					AND user_id = $2
					AND session_id = $3
					AND track = $4
					AND (expires_at IS NULL
						OR expires_at > NOW() AT TIME ZONE 'localtime')
					AND created_at > $5
					AND deleted_at IS NULL
					ORDER BY created_at DESC`,
					s.tableSessionTracks,
				)
				args = []any{
					key.AppName, key.UserID,
					key.SessionID, track,
					afterTime,
				}
			}
			queries = append(queries, &trackQuery{
				sessionIdx: i,
				track:      track,
				query:      q,
				args:       args,
			})
		}
	}

	results := make(
		[]map[session.Track][]session.TrackEvent,
		len(sessionKeys),
	)
	for _, q := range queries {
		events := make([]session.TrackEvent, 0)
		err := s.pgClient.Query(ctx,
			func(rows *sql.Rows) error {
				for rows.Next() {
					var eventBytes []byte
					if err := rows.Scan(
						&eventBytes,
					); err != nil {
						return err
					}
					var te session.TrackEvent
					if err := json.Unmarshal(
						eventBytes, &te,
					); err != nil {
						return fmt.Errorf(
							"unmarshal track event "+
								"failed: %w", err,
						)
					}
					events = append(events, te)
				}
				return nil
			}, q.query, q.args...)
		if err != nil {
			return nil, fmt.Errorf(
				"query track events failed: %w", err,
			)
		}
		// Reverse to ascending order.
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
		if results[q.sessionIdx] == nil {
			results[q.sessionIdx] = make(
				map[session.Track][]session.TrackEvent,
			)
		}
		results[q.sessionIdx][q.track] = events
	}
	for i := range results {
		if results[i] == nil {
			results[i] = make(
				map[session.Track][]session.TrackEvent,
			)
		}
	}
	return results, nil
}

// getSummariesList batch loads summaries for multiple
// sessions.
func (s *Service) getSummariesList(
	ctx context.Context,
	sessionKeys []session.Key,
) ([]map[string]*session.Summary, error) {
	if len(sessionKeys) == 0 {
		return []map[string]*session.Summary{}, nil
	}

	sessionIDs := make([]string, len(sessionKeys))
	for i, key := range sessionKeys {
		sessionIDs[i] = key.SessionID
	}

	summaryQuery := fmt.Sprintf(
		`SELECT session_id, filter_key, summary
		FROM %s
		WHERE app_name = $1 AND user_id = $2
		AND session_id = ANY($3::varchar[])
		AND (expires_at IS NULL OR expires_at > NOW() AT TIME ZONE 'localtime')
		AND deleted_at IS NULL`,
		s.tableSessionSummaries,
	)

	summariesMap := make(
		map[string]map[string]*session.Summary,
	)
	err := s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var sessionID, filterKey string
				var summaryBytes []byte
				if err := rows.Scan(
					&sessionID, &filterKey,
					&summaryBytes,
				); err != nil {
					return err
				}
				var sum session.Summary
				if err := json.Unmarshal(
					summaryBytes, &sum,
				); err != nil {
					return fmt.Errorf(
						"unmarshal summary failed: %w",
						err,
					)
				}
				if summariesMap[sessionID] == nil {
					summariesMap[sessionID] = make(
						map[string]*session.Summary,
					)
				}
				summariesMap[sessionID][filterKey] = &sum
			}
			return nil
		},
		summaryQuery,
		sessionKeys[0].AppName,
		sessionKeys[0].UserID,
		sessionIDs,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"query summaries failed: %w", err,
		)
	}

	result := make(
		[]map[string]*session.Summary, len(sessionKeys),
	)
	for i, key := range sessionKeys {
		summaries := summariesMap[key.SessionID]
		if summaries == nil {
			summaries = make(
				map[string]*session.Summary,
			)
		}
		result[i] = summaries
	}
	return result, nil
}
