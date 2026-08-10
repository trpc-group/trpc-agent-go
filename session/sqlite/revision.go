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

type revisionExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
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
			`SELECT record FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
			s.tableSessionRevisions,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return &sessionrevision.PersistedRecord{}, nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, fmt.Errorf(
				"%w: revision tables are not initialized",
				sessionrevision.ErrLatestTurnReplacementUnsupported,
			)
		}
		return nil, fmt.Errorf("get revision metadata: %w", err)
	}
	var record sessionrevision.PersistedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode revision metadata: %w", err)
	}
	return &record, nil
}

func (s *Service) writeRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	record *sessionrevision.PersistedRecord,
	expiresAt any,
) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode revision metadata: %w", err)
	}
	_, err = tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
  app_name, user_id, session_id, record, updated_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(app_name, user_id, session_id) DO UPDATE SET
  record = excluded.record,
  updated_at = excluded.updated_at,
  expires_at = excluded.expires_at`,
			s.tableSessionRevisions,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		raw,
		time.Now().UTC().UnixNano(),
		expiresAt,
	)
	if err != nil {
		return fmt.Errorf("store revision metadata: %w", err)
	}
	_, err = tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET expires_at = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
			s.tableRevisionArchives,
		),
		expiresAt,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("refresh revision archive expiration: %w", err)
	}
	return nil
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

func (s *Service) attachRevisionGeneration(
	ctx context.Context,
	key session.Key,
	sess *session.Session,
) error {
	if sess == nil {
		return nil
	}
	record, err := s.readRevision(ctx, s.db, key)
	if errors.Is(err, sessionrevision.ErrLatestTurnReplacementUnsupported) {
		return nil
	}
	if err != nil {
		return err
	}
	sessionrevision.SetGeneration(sess, record.Generation)
	return nil
}

func (s *Service) revisionGeneration(
	ctx context.Context,
	key session.Key,
) (uint64, error) {
	record, err := s.readRevision(ctx, s.db, key)
	if errors.Is(err, sessionrevision.ErrLatestTurnReplacementUnsupported) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return record.Generation, nil
}

func (s *Service) readRevisionForWrite(
	ctx context.Context,
	query revisionRowQuerier,
	key session.Key,
) (*sessionrevision.PersistedRecord, bool, error) {
	record, err := s.readRevision(ctx, query, key)
	if errors.Is(err, sessionrevision.ErrLatestTurnReplacementUnsupported) {
		return &sessionrevision.PersistedRecord{}, false, nil
	}
	return record, true, err
}

func (s *Service) deleteRevisionMetadata(
	ctx context.Context,
	exec revisionExecer,
	key session.Key,
) error {
	for _, table := range []string{
		s.tableRevisionArchives,
		s.tableSessionRevisions,
	} {
		if _, err := exec.ExecContext(
			ctx,
			fmt.Sprintf(
				`DELETE FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?`,
				table,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		); err != nil {
			if strings.Contains(err.Error(), "no such table") {
				continue
			}
			return fmt.Errorf("delete revision metadata: %w", err)
		}
	}
	return nil
}

func (s *Service) flushRevisionPersistence(
	ctx context.Context,
	key session.Key,
) error {
	if !s.opts.enableAsyncPersist {
		return nil
	}
	hash := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash
	eventBarrier := &sessionEventPair{done: make(chan error, 1)}
	select {
	case s.eventPairChans[hash%len(s.eventPairChans)] <- eventBarrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-eventBarrier.done:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.flushTrackPersistence(ctx, hash)
}

func (s *Service) flushTrackPersistence(ctx context.Context, hash int) error {
	if !s.opts.enableAsyncPersist || len(s.trackEventChans) == 0 {
		return nil
	}
	trackBarrier := &trackEventPair{done: make(chan error, 1)}
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
	restored, err := sessionrevision.DecodeSnapshot(checkpoint.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("decode latest-turn checkpoint: %w", err)
	}
	current, expiresAt, err := s.loadActiveSessionTx(ctx, tx, req.Key)
	if err != nil {
		return nil, err
	}
	archive, err := sessionrevision.Snapshot(current)
	if err != nil {
		return nil, fmt.Errorf("encode discarded revision: %w", err)
	}
	_, err = tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
  app_name, user_id, session_id, generation, snapshot, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			s.tableRevisionArchives,
		),
		req.Key.AppName,
		req.Key.UserID,
		req.Key.SessionID,
		record.Generation,
		archive,
		time.Now().UTC().UnixNano(),
		nullInt64Arg(expiresAt),
	)
	if err != nil {
		return nil, fmt.Errorf("archive discarded revision: %w", err)
	}
	if err := s.replaceActiveSessionTx(ctx, tx, req.Key, restored, expiresAt); err != nil {
		return nil, err
	}
	record.Generation++
	record.Head++
	record.Checkpoint = nil
	if record.Replays == nil {
		record.Replays = make(map[string]sessionrevision.PersistedReplay)
	}
	record.Replays[req.IdempotencyKey] = sessionrevision.PersistedReplay{
		RequestID:  req.ExpectedRequestID,
		Generation: record.Generation,
		Head:       record.Head,
	}
	if err := s.writeRevisionTx(ctx, tx, req.Key, record, nullInt64Arg(expiresAt)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit latest-turn replacement: %w", err)
	}
	sessionrevision.SetGeneration(restored, record.Generation)
	return s.replacementResultWithScopedState(ctx, req.Key, restored, true)
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
	if err := json.Unmarshal(stateRaw, &state); err != nil {
		return nil, sql.NullInt64{}, fmt.Errorf("decode active session state: %w", err)
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
	expiresAt sql.NullInt64,
) error {
	stateRaw, err := json.Marshal(&SessionState{
		ID:        key.SessionID,
		State:     restored.SnapshotState(),
		CreatedAt: restored.CreatedAt,
		UpdatedAt: restored.UpdatedAt,
	})
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
	for _, table := range []string{
		s.tableSessionEvents,
		s.tableSessionSummaries,
	} {
		if _, err := tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`DELETE FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ?`,
				table,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		); err != nil {
			return fmt.Errorf("clear active session projection: %w", err)
		}
	}
	if err := s.trimActiveTrackTailsTx(ctx, tx, key, restored); err != nil {
		return err
	}
	for i := range restored.Events {
		evt := restored.Events[i]
		raw, err := json.Marshal(&evt)
		if err != nil {
			return err
		}
		created := evt.Timestamp.UTC().UnixNano()
		if evt.Timestamp.IsZero() {
			created = restored.CreatedAt.UTC().UnixNano() + int64(i)
		}
		_, err = tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, user_id, session_id, event, created_at, updated_at, expires_at,
  deleted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`,
				s.tableSessionEvents,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
			raw,
			created,
			created,
			nullInt64Arg(expiresAt),
		)
		if err != nil {
			return fmt.Errorf("restore active event: %w", err)
		}
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
			nullInt64Arg(expiresAt),
		)
		if err != nil {
			return fmt.Errorf("restore active summary: %w", err)
		}
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
	args := make([]any, len(tail), len(tail)+3)
	for i, id := range tail {
		args[i] = id
	}
	args = append(args, key.AppName, key.UserID, key.SessionID)
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s WHERE id IN (%s)
AND app_name = ? AND user_id = ? AND session_id = ?`,
			s.tableSessionTracks,
			placeholders,
		),
		args...,
	); err != nil {
		return fmt.Errorf("remove discarded track tail: %w", err)
	}
	return nil
}
