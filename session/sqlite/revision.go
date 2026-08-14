//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type revisionRowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) readRevision(
	ctx context.Context,
	query revisionRowQuerier,
	key session.Key,
) (*sessionrevision.PersistedRecord, error) {
	var raw []byte
	err := query.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT state FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return &sessionrevision.PersistedRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get revision metadata: %w", err)
	}
	var state SessionState
	record, err := sessionrevision.DecodeState(raw, &state)
	if err != nil {
		return nil, err
	}
	return record, nil
}

func checkRevisionGeneration(
	record *sessionrevision.PersistedRecord,
	write sessionrevision.Write,
) error {
	if write.HasExpectedGeneration &&
		record.Generation != write.ExpectedGeneration {
		return sessionrevision.ErrStaleGeneration
	}
	return nil
}

func (s *Service) revisionGeneration(
	ctx context.Context,
	key session.Key,
) (uint64, error) {
	record, err := s.readRevision(ctx, s.db, key)
	if err != nil {
		return 0, err
	}
	return record.Generation, nil
}

func (s *Service) flushRevisionPersistence(
	ctx context.Context,
	key session.Key,
) error {
	if !s.opts.enableAsyncPersist {
		return nil
	}
	return errors.Join(
		s.flushEventPersistence(ctx, key),
		s.flushTrackPersistence(ctx, key),
	)
}

func (s *Service) flushEventPersistence(
	ctx context.Context,
	key session.Key,
) error {
	if !s.opts.enableAsyncPersist || len(s.eventPairChans) == 0 {
		return nil
	}
	hash := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash
	eventBarrier := &sessionEventPair{
		key: key, done: make(chan error), barrierCtx: ctx,
	}
	select {
	case s.eventPairChans[hash%len(s.eventPairChans)] <- eventBarrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-eventBarrier.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) flushTrackPersistence(ctx context.Context, key session.Key) error {
	if !s.opts.enableAsyncPersist || len(s.trackEventChans) == 0 {
		return nil
	}
	hash := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash
	trackBarrier := &trackEventPair{
		key: key, done: make(chan error), barrierCtx: ctx,
	}
	select {
	case s.trackEventChans[hash%len(s.trackEventChans)] <- trackBarrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-trackBarrier.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReplaceLatestTurn restores the active session projection to the checkpoint
// immediately before its latest persisted turn for Runner.
func (s *Service) ReplaceLatestTurn(
	ctx context.Context,
	req sessionrevision.LatestTurnReplacementRequest,
) (*sessionrevision.LatestTurnReplacementResult, error) {
	if err := sessionrevision.ValidateLatestTurnReplacementRequest(req); err != nil {
		return nil, err
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, err
	}
	s.stateWriteMu.Lock()
	defer s.stateWriteMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin latest-turn replacement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	record, err := s.readRevision(ctx, tx, req.Key)
	if err != nil {
		return nil, err
	}
	if _, replayed, err := sessionrevision.LatestTurnReplacementReplay(
		record,
		req.ExpectedRequestID,
		req.IdempotencyKey,
	); err != nil {
		return nil, err
	} else if replayed {
		active, _, err := s.loadActiveSessionTx(ctx, tx, req.Key)
		if err != nil {
			return nil, fmt.Errorf("load idempotent replacement result: %w", err)
		}
		sessionrevision.SetGeneration(active, record.Generation)
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("finish idempotent latest-turn replacement: %w", err)
		}
		return s.replacementResultWithScopedState(ctx, req.Key, active, false)
	}
	checkpoint, err := sessionrevision.LatestTurnReplacementCheckpoint(
		record,
		req.ExpectedRequestID,
	)
	if err != nil {
		return nil, err
	}
	if record.Generation >= math.MaxInt64 {
		return nil, sessionrevision.ErrLatestTurnReplacementUnavailable
	}
	current, expiresAt, err := s.loadActiveSessionTx(ctx, tx, req.Key)
	if err != nil {
		return nil, err
	}
	restored, err := sessionrevision.RestoreBoundary(
		current,
		checkpoint.Boundary,
	)
	if err != nil {
		return nil, fmt.Errorf("restore latest-turn boundary: %w", err)
	}
	if err := sessionrevision.ResetProjectionFromBoundary(
		record, checkpoint.Boundary,
	); err != nil {
		return nil, err
	}
	record.Generation++
	record.Head++
	record.Checkpoint = nil
	sessionrevision.RecordLatestTurnReplacementReplay(
		record,
		req.IdempotencyKey,
		sessionrevision.PersistedReplay{
			RequestID:  req.ExpectedRequestID,
			Generation: record.Generation,
			Head:       record.Head,
		},
	)
	if err := s.replaceActiveSessionTx(
		ctx,
		tx,
		req.Key,
		restored,
		record,
		expiresAt,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit latest-turn replacement: %w", err)
	}
	sessionrevision.SetGeneration(restored, record.Generation)
	return s.replacementResultWithScopedState(ctx, req.Key, restored, true)
}

func (s *Service) loadTurnBoundaryTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	record *sessionrevision.PersistedRecord,
	state *SessionState,
) (*session.Session, error) {
	if !sessionrevision.ProjectionInitialized(record) {
		active, _, err := s.loadActiveSessionTx(ctx, tx, key)
		if err != nil {
			return nil, err
		}
		if err := sessionrevision.InitializeProjection(record, active); err != nil {
			return nil, err
		}
		return active, nil
	}
	if state == nil {
		return nil, session.ErrNilSession
	}
	active := session.NewSession(
		key.AppName,
		key.UserID,
		key.SessionID,
		session.WithSessionState(state.State),
		session.WithSessionCreatedAt(state.CreatedAt),
		session.WithSessionUpdatedAt(state.UpdatedAt),
	)
	if err := s.loadActiveSummariesTx(ctx, tx, key, active); err != nil {
		return nil, err
	}
	return active, nil
}

func nullInt64Arg(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func (s *Service) replacementResultWithScopedState(
	ctx context.Context,
	key session.Key,
	active *session.Session,
	applied bool,
) (*sessionrevision.LatestTurnReplacementResult, error) {
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
	return &sessionrevision.LatestTurnReplacementResult{
		ActiveSession: mergeState(appState, userState, active),
		Applied:       applied,
	}, nil
}

func (s *Service) loadActiveSessionTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
) (*session.Session, sql.NullInt64, error) {
	var (
		stateRaw []byte
		created  int64
		updated  int64
		expires  sql.NullInt64
	)
	err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT state, created_at, updated_at, expires_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&stateRaw, &created, &updated, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.NullInt64{}, sessionrevision.ErrLatestTurnReplacementUnavailable
	}
	if err != nil {
		return nil, sql.NullInt64{}, fmt.Errorf("load active session state: %w", err)
	}
	var state SessionState
	if _, err := sessionrevision.DecodeState(stateRaw, &state); err != nil {
		return nil, sql.NullInt64{}, err
	}
	active := session.NewSession(
		key.AppName,
		key.UserID,
		key.SessionID,
		session.WithSessionState(state.State),
		session.WithSessionCreatedAt(unixNanoToTime(created)),
		session.WithSessionUpdatedAt(unixNanoToTime(updated)),
	)
	if err := s.loadActiveEventsTx(ctx, tx, key, active); err != nil {
		return nil, sql.NullInt64{}, err
	}
	if err := s.loadActiveTracksTx(ctx, tx, key, active); err != nil {
		return nil, sql.NullInt64{}, err
	}
	if err := s.loadActiveSummariesTx(ctx, tx, key, active); err != nil {
		return nil, sql.NullInt64{}, err
	}
	return active, expires, nil
}

func (s *Service) loadActiveEventsTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	active *session.Session,
) error {
	eventRows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC`,
			s.tableSessionEvents,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("load active events: %w", err)
	}
	for eventRows.Next() {
		var raw []byte
		if err := eventRows.Scan(&raw); err != nil {
			_ = eventRows.Close()
			return err
		}
		var evt event.Event
		if err := json.Unmarshal(raw, &evt); err != nil {
			_ = eventRows.Close()
			return err
		}
		active.Events = append(active.Events, evt)
	}
	if err := eventRows.Err(); err != nil {
		_ = eventRows.Close()
		return fmt.Errorf("load active events: %w", err)
	}
	if err := eventRows.Close(); err != nil {
		return err
	}
	return nil
}

func (s *Service) loadActiveTracksTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	active *session.Session,
) error {
	trackRows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT track, event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC`,
			s.tableSessionTracks,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("load active tracks: %w", err)
	}
	for trackRows.Next() {
		var (
			track session.Track
			raw   []byte
		)
		if err := trackRows.Scan(&track, &raw); err != nil {
			_ = trackRows.Close()
			return err
		}
		var trackEvent session.TrackEvent
		if err := json.Unmarshal(raw, &trackEvent); err != nil {
			_ = trackRows.Close()
			return err
		}
		if active.Tracks == nil {
			active.Tracks = make(map[session.Track]*session.TrackEvents)
		}
		if active.Tracks[track] == nil {
			active.Tracks[track] = &session.TrackEvents{Track: track}
		}
		active.Tracks[track].Events = append(active.Tracks[track].Events, trackEvent)
	}
	if err := trackRows.Err(); err != nil {
		_ = trackRows.Close()
		return fmt.Errorf("load active tracks: %w", err)
	}
	if err := trackRows.Close(); err != nil {
		return err
	}
	return nil
}

func (s *Service) loadActiveSummariesTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	active *session.Session,
) error {
	summaryRows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT filter_key, summary FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionSummaries,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("load active summaries: %w", err)
	}
	for summaryRows.Next() {
		var (
			filterKey string
			raw       []byte
		)
		if err := summaryRows.Scan(&filterKey, &raw); err != nil {
			_ = summaryRows.Close()
			return err
		}
		var summary session.Summary
		if err := json.Unmarshal(raw, &summary); err != nil {
			_ = summaryRows.Close()
			return err
		}
		if active.Summaries == nil {
			active.Summaries = make(map[string]*session.Summary)
		}
		active.Summaries[filterKey] = &summary
	}
	if err := summaryRows.Err(); err != nil {
		_ = summaryRows.Close()
		return fmt.Errorf("load active summaries: %w", err)
	}
	if err := summaryRows.Close(); err != nil {
		return err
	}
	return nil
}

func (s *Service) replaceActiveSessionTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	restored *session.Session,
	record *sessionrevision.PersistedRecord,
	expiresAt sql.NullInt64,
) error {
	stateRaw, err := sessionrevision.EncodeState(&SessionState{
		ID:        key.SessionID,
		State:     restored.SnapshotState(),
		CreatedAt: restored.CreatedAt,
		UpdatedAt: restored.UpdatedAt,
	}, record)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET state = ?, created_at = ?, updated_at = ?, expires_at = ?
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		stateRaw,
		restored.CreatedAt.UTC().UnixNano(),
		restored.UpdatedAt.UTC().UnixNano(),
		nullInt64Arg(expiresAt),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("restore session state: %w", err)
	}
	if err := s.trimActiveEventTailTx(ctx, tx, key, len(restored.Events)); err != nil {
		return err
	}
	// Summary rows have a key-wide unique index, so the active map is replaced
	// in full. Rows deleted by an earlier Session instance remain untouched.
	summaryDelete := fmt.Sprintf(
		`DELETE FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ?`,
		s.tableSessionSummaries,
	)
	if s.opts.softDelete {
		summaryDelete += " AND deleted_at IS NULL"
	}
	if _, err := tx.ExecContext(
		ctx, summaryDelete, key.AppName, key.UserID, key.SessionID,
	); err != nil {
		return fmt.Errorf("clear active session summaries: %w", err)
	}
	if err := s.trimActiveTrackTailsTx(ctx, tx, key, restored); err != nil {
		return err
	}
	for filterKey, summary := range restored.Summaries {
		if summary == nil {
			continue
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, user_id, session_id, filter_key, summary, updated_at,
  expires_at, deleted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`,
				s.tableSessionSummaries,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
			filterKey,
			raw,
			summary.UpdatedAt.UTC().UnixNano(),
			nil,
		)
		if err != nil {
			return fmt.Errorf("restore active summary: %w", err)
		}
	}
	return nil
}

func (s *Service) trimActiveEventTailTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	prefixLength int,
) error {
	rows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC`,
			s.tableSessionEvents,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("load active event rows: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate active event rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(ids) <= prefixLength {
		return sessionrevision.ErrLatestTurnReplacementUnavailable
	}
	tail := ids[prefixLength:]
	placeholders := make([]string, len(tail))
	args := make([]any, 0, len(tail)+4)
	for i, id := range tail {
		placeholders[i] = "?"
		args = append(args, id)
	}
	var statement string
	if s.opts.softDelete {
		statement = fmt.Sprintf(
			`UPDATE %s SET deleted_at = ? WHERE id IN (%s)
AND app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionEvents,
			strings.Join(placeholders, ", "),
		)
		args = append([]any{time.Now().UTC().UnixNano()}, args...)
	} else {
		statement = fmt.Sprintf(
			`DELETE FROM %s WHERE id IN (%s)
AND app_name = ? AND user_id = ? AND session_id = ?`,
			s.tableSessionEvents,
			strings.Join(placeholders, ", "),
		)
	}
	args = append(args, key.AppName, key.UserID, key.SessionID)
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("remove discarded event tail: %w", err)
	}
	return nil
}

func (s *Service) trimActiveTrackTailsTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	restored *session.Session,
) error {
	rows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, track, event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL ORDER BY created_at, id`,
			s.tableSessionTracks,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("lock active tracks: %w", err)
	}
	type trackRow struct {
		id    int64
		event session.TrackEvent
	}
	active := make(map[session.Track][]trackRow)
	for rows.Next() {
		var (
			id    int64
			track session.Track
			raw   []byte
		)
		if err := rows.Scan(&id, &track, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		var trackEvent session.TrackEvent
		if err := json.Unmarshal(raw, &trackEvent); err != nil {
			_ = rows.Close()
			return err
		}
		active[track] = append(active[track], trackRow{id: id, event: trackEvent})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate active tracks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var tail []int64
	for track, activeRows := range active {
		history := restored.Tracks[track]
		prefixLength := 0
		if history != nil {
			prefixLength = len(history.Events)
		}
		if len(activeRows) < prefixLength {
			return fmt.Errorf(
				"track %q checkpoint prefix has %d events, active projection has %d: %w",
				track,
				prefixLength,
				len(activeRows),
				sessionrevision.ErrLatestTurnReplacementUnavailable,
			)
		}
		for i := 0; i < prefixLength; i++ {
			if !sessionrevision.TrackEventsEqual(
				activeRows[i].event,
				history.Events[i],
			) {
				return fmt.Errorf(
					"track %q checkpoint prefix differs at event %d: %w",
					track,
					i,
					sessionrevision.ErrLatestTurnReplacementUnavailable,
				)
			}
		}
		for _, row := range activeRows[prefixLength:] {
			tail = append(tail, row.id)
		}
	}
	for track, history := range restored.Tracks {
		if history != nil && len(history.Events) > 0 && len(active[track]) == 0 {
			return fmt.Errorf(
				"track %q checkpoint prefix is missing: %w",
				track,
				sessionrevision.ErrLatestTurnReplacementUnavailable,
			)
		}
	}
	if len(tail) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(tail)), ",")
	args := make([]any, 0, len(tail)+4)
	for _, id := range tail {
		args = append(args, id)
	}
	var statement string
	if s.opts.softDelete {
		statement = fmt.Sprintf(
			`UPDATE %s SET deleted_at = ? WHERE id IN (%s)
AND app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionTracks,
			placeholders,
		)
		args = append([]any{time.Now().UTC().UnixNano()}, args...)
	} else {
		statement = fmt.Sprintf(
			`DELETE FROM %s WHERE id IN (%s)
AND app_name = ? AND user_id = ? AND session_id = ?`,
			s.tableSessionTracks,
			placeholders,
		)
	}
	args = append(args, key.AppName, key.UserID, key.SessionID)
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("remove discarded track tail: %w", err)
	}
	return nil
}
