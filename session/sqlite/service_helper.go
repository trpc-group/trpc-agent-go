//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	const stateSQL = `SELECT state, created_at, updated_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND (expires_at IS NULL OR expires_at > ?)
AND deleted_at IS NULL`
	query := fmt.Sprintf(stateSQL, s.tableSessionStates)

	var (
		stateBytes []byte
		createdNs  int64
		updatedNs  int64
	)
	err := s.db.QueryRowContext(
		ctx,
		query,
		key.AppName,
		key.UserID,
		key.SessionID,
		time.Now().UTC().UnixNano(),
	).Scan(&stateBytes, &createdNs, &updatedNs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session state: %w", err)
	}

	var sessState SessionState
	if err := json.Unmarshal(stateBytes, &sessState); err != nil {
		return nil, fmt.Errorf("unmarshal session state: %w", err)
	}
	sessState.CreatedAt = unixNanoToTime(createdNs)
	sessState.UpdatedAt = unixNanoToTime(updatedNs)

	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	if err != nil {
		return nil, err
	}

	eventsList, err := s.getEventsList(
		ctx,
		[]session.Key{key},
		[]time.Time{sessState.CreatedAt},
		limit,
		afterTime,
	)
	if err != nil {
		return nil, err
	}
	events := eventsList[0]

	summaries := make(map[string]*session.Summary)
	if len(events) > 0 {
		sums, err := s.getSummariesList(
			ctx,
			[]session.Key{key},
			[]time.Time{sessState.CreatedAt},
		)
		if err != nil {
			return nil, err
		}
		summaries = sums[0]
	}

	sess := session.NewSession(
		key.AppName,
		key.UserID,
		sessState.ID,
		session.WithSessionState(sessState.State),
		session.WithSessionEvents(events),
		session.WithSessionSummaries(summaries),
		session.WithSessionCreatedAt(sessState.CreatedAt),
		session.WithSessionUpdatedAt(sessState.UpdatedAt),
	)

	trackEventsList, err := s.getTrackEvents(
		ctx,
		[]session.Key{key},
		[]*SessionState{&sessState},
		limit,
		afterTime,
	)
	if err != nil {
		return nil, err
	}
	if len(trackEventsList) > 0 && len(trackEventsList[0]) > 0 {
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

	const listSQL = `SELECT session_id, state, created_at, updated_at FROM %s
WHERE app_name = ? AND user_id = ?
AND (expires_at IS NULL OR expires_at > ?)
AND deleted_at IS NULL
ORDER BY updated_at DESC`
	query := fmt.Sprintf(listSQL, s.tableSessionStates)

	rows, err := s.db.QueryContext(
		ctx,
		query,
		key.AppName,
		key.UserID,
		time.Now().UTC().UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("list session states: %w", err)
	}
	defer rows.Close()

	var sessStates []*SessionState
	for rows.Next() {
		var (
			sessionID  string
			stateBytes []byte
			createdNs  int64
			updatedNs  int64
		)
		if err := rows.Scan(
			&sessionID,
			&stateBytes,
			&createdNs,
			&updatedNs,
		); err != nil {
			return nil, fmt.Errorf("scan session state: %w", err)
		}
		var state SessionState
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			return nil, fmt.Errorf("unmarshal session state: %w", err)
		}
		state.ID = sessionID
		state.CreatedAt = unixNanoToTime(createdNs)
		state.UpdatedAt = unixNanoToTime(updatedNs)
		sessStates = append(sessStates, &state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session states: %w", err)
	}

	sessionKeys := make([]session.Key, 0, len(sessStates))
	sessionCreatedAts := make([]time.Time, 0, len(sessStates))
	for _, st := range sessStates {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: st.ID,
		})
		sessionCreatedAts = append(sessionCreatedAts, st.CreatedAt)
	}

	eventsList, err := s.getEventsList(
		ctx,
		sessionKeys,
		sessionCreatedAts,
		limit,
		afterTime,
	)
	if err != nil {
		return nil, err
	}

	summariesList, err := s.getSummariesList(
		ctx,
		sessionKeys,
		sessionCreatedAts,
	)
	if err != nil {
		return nil, err
	}

	trackEvents, err := s.getTrackEvents(
		ctx,
		sessionKeys,
		sessStates,
		limit,
		afterTime,
	)
	if err != nil {
		return nil, err
	}
	if len(trackEvents) != len(sessStates) {
		return nil, fmt.Errorf(
			"track events count mismatch: %d != %d",
			len(trackEvents),
			len(sessStates),
		)
	}

	out := make([]*session.Session, 0, len(sessStates))
	for i, st := range sessStates {
		var sums map[string]*session.Summary
		if len(eventsList[i]) > 0 {
			sums = summariesList[i]
		}
		sess := session.NewSession(
			key.AppName,
			key.UserID,
			st.ID,
			session.WithSessionState(st.State),
			session.WithSessionEvents(eventsList[i]),
			session.WithSessionSummaries(sums),
			session.WithSessionCreatedAt(st.CreatedAt),
			session.WithSessionUpdatedAt(st.UpdatedAt),
		)
		if len(trackEvents[i]) > 0 {
			sess.Tracks = make(
				map[session.Track]*session.TrackEvents,
				len(trackEvents[i]),
			)
			for trackName, history := range trackEvents[i] {
				sess.Tracks[trackName] = &session.TrackEvents{
					Track:  trackName,
					Events: history,
				}
			}
		}
		out = append(out, mergeState(appState, userState, sess))
	}

	return out, nil
}

func (s *Service) addEvent(
	ctx context.Context,
	key session.Key,
	evt *event.Event,
) error {
	s.stateWriteMu.Lock()
	defer s.stateWriteMu.Unlock()

	now := time.Now().UTC()

	var (
		stateBytes []byte
		expiresAt  sql.NullInt64
	)
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT state, expires_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&stateBytes, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("session not found")
	}
	if err != nil {
		return fmt.Errorf("get session state: %w", err)
	}

	var sessState SessionState
	if err := json.Unmarshal(stateBytes, &sessState); err != nil {
		return fmt.Errorf("unmarshal session state: %w", err)
	}

	if expiresAt.Valid && unixNanoToTime(expiresAt.Int64).Before(now) {
		log.InfofContext(
			ctx,
			"appending event to expired session (app=%s, user=%s, "+
				"session=%s), extending expires_at",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
	}

	sessState.UpdatedAt = now
	if sessState.State == nil {
		sessState.State = make(session.StateMap)
	}
	session.ApplyEventStateDeltaMap(sessState.State, evt)

	updatedState, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}

	eventBytes, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	newExpires := calculateExpiresAt(now, s.opts.sessionTTL)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s
SET state = ?, updated_at = ?, expires_at = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		updatedState,
		now.UTC().UnixNano(),
		newExpires,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}

	if evt.Response != nil && !evt.IsPartial && evt.IsValidContent() {
		_, err = tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, user_id, session_id, event, created_at, updated_at, deleted_at
) VALUES (?, ?, ?, ?, ?, ?, NULL)`,
				s.tableSessionEvents,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
			eventBytes,
			now.UTC().UnixNano(),
			now.UTC().UnixNano(),
		)
		if err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *Service) addTrackEvent(
	ctx context.Context,
	key session.Key,
	trackEvent *session.TrackEvent,
) error {
	s.stateWriteMu.Lock()
	defer s.stateWriteMu.Unlock()

	now := time.Now().UTC()

	var (
		stateBytes []byte
		expiresAt  sql.NullInt64
	)
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT state, expires_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&stateBytes, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("session not found")
	}
	if err != nil {
		return fmt.Errorf("get session state: %w", err)
	}

	var sessState SessionState
	if err := json.Unmarshal(stateBytes, &sessState); err != nil {
		return fmt.Errorf("unmarshal session state: %w", err)
	}

	if expiresAt.Valid && unixNanoToTime(expiresAt.Int64).Before(now) {
		log.InfofContext(
			ctx,
			"appending track event to expired session (app=%s, "+
				"user=%s, session=%s), extending expires_at",
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

	updatedState, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}

	eventBytes, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf("marshal track event: %w", err)
	}

	newExpires := calculateExpiresAt(now, s.opts.sessionTTL)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s
SET state = ?, updated_at = ?, expires_at = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		updatedState,
		sessState.UpdatedAt.UTC().UnixNano(),
		newExpires,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}

	tsNs := trackEvent.Timestamp.UTC().UnixNano()
	_, err = tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
  app_name, user_id, session_id, track, event, created_at, updated_at,
  expires_at, deleted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			s.tableSessionTracks,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		trackEvent.Track,
		eventBytes,
		tsNs,
		tsNs,
		newExpires,
	)
	if err != nil {
		return fmt.Errorf("insert track event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *Service) deleteSessionState(
	ctx context.Context,
	key session.Key,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if s.opts.softDelete {
		nowNs := time.Now().UTC().UnixNano()

		if err := s.softDeleteSessionTx(ctx, tx, key, nowNs); err != nil {
			return err
		}
	} else {
		if err := s.hardDeleteSessionTx(ctx, tx, key); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *Service) softDeleteSessionTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	nowNs int64,
) error {
	tables := []string{
		s.tableSessionStates,
		s.tableSessionSummaries,
		s.tableSessionEvents,
		s.tableSessionTracks,
	}
	for _, table := range tables {
		_, err := tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET deleted_at = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
				table,
			),
			nowNs,
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		if err != nil {
			return fmt.Errorf("soft delete from %s: %w", table, err)
		}
	}
	return nil
}

func (s *Service) hardDeleteSessionTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
) error {
	tables := []string{
		s.tableSessionEvents,
		s.tableSessionTracks,
		s.tableSessionSummaries,
		s.tableSessionStates,
	}
	for _, table := range tables {
		_, err := tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`DELETE FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
				table,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		if err != nil {
			return fmt.Errorf("hard delete from %s: %w", table, err)
		}
	}
	return nil
}
