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

var errAsyncPersistenceClosed = errors.New("async persistence is closed")

func recoverClosedChannelPanic(result *error) {
	if recovered := recover(); recovered != nil {
		if panicErr, ok := recovered.(error); ok &&
			panicErr.Error() == "send on closed channel" {
			*result = errors.Join(*result, errAsyncPersistenceClosed)
			return
		}
		panic(recovered)
	}
}

type revisionRowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type revisionExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

const (
	revisionGenerationBatchSize = 250
	revisionTailDeleteBatchSize = 500
)

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
	return sessionrevision.CheckWrite(record, write)
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

func (s *Service) revisionGenerations(
	ctx context.Context,
	keys []session.Key,
) (map[session.Key]uint64, error) {
	generations := make(map[session.Key]uint64, len(keys))
	for _, key := range keys {
		generations[key] = 0
	}
	for start := 0; start < len(keys); start += revisionGenerationBatchSize {
		end := min(start+revisionGenerationBatchSize, len(keys))
		batch := keys[start:end]
		clauses := make([]string, len(batch))
		args := make([]any, 0, len(batch)*3)
		for i, key := range batch {
			clauses[i] = "(app_name = ? AND user_id = ? AND session_id = ?)"
			args = append(args, key.AppName, key.UserID, key.SessionID)
		}
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
			`SELECT app_name, user_id, session_id, state FROM %s
WHERE deleted_at IS NULL AND (%s)`,
			s.tableSessionStates,
			strings.Join(clauses, " OR "),
		), args...)
		if err != nil {
			return nil, fmt.Errorf("read session revisions: %w", err)
		}
		for rows.Next() {
			var key session.Key
			var raw []byte
			if err := rows.Scan(
				&key.AppName, &key.UserID, &key.SessionID, &raw,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan session revision: %w", err)
			}
			var state SessionState
			record, err := sessionrevision.DecodeState(raw, &state)
			if err != nil {
				rows.Close()
				return nil, err
			}
			generations[key] = record.Generation
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate session revisions: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close session revisions: %w", err)
		}
	}
	return generations, nil
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
) (retErr error) {
	defer recoverClosedChannelPanic(&retErr)
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

func (s *Service) flushTrackPersistence(
	ctx context.Context,
	key session.Key,
) (retErr error) {
	defer recoverClosedChannelPanic(&retErr)
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

// Rewind atomically restores a retained pre-request session boundary.
func (s *Service) Rewind(
	ctx context.Context,
	req session.RewindRequest,
) (*session.RewindResult, error) {
	if err := sessionrevision.ValidateRewindRequest(req); err != nil {
		return nil, err
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, err
	}
	s.stateWriteMu.Lock()
	defer s.stateWriteMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin session rewind: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	activeAt := time.Now().UTC().UnixNano()
	record, err := s.readRevision(ctx, tx, req.Key)
	if err != nil {
		return nil, err
	}
	current, expiresAt, err := s.loadActiveSessionTx(
		ctx, tx, req.Key, activeAt,
	)
	if err != nil {
		return nil, err
	}
	if _, replayed, err := sessionrevision.RewindReplay(
		record,
		req.TargetRequestID,
		req.ExpectedHeadRequestID,
		req.IdempotencyKey,
	); err != nil {
		return nil, err
	} else if replayed {
		sessionrevision.AttachRewindFence(current, record)
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("finish idempotent session rewind: %w", err)
		}
		return s.rewindResultWithScopedState(ctx, req.Key, current)
	}
	checkpoint, err := sessionrevision.RewindCheckpoint(
		record,
		req.TargetRequestID,
		req.ExpectedHeadRequestID,
	)
	if err != nil {
		return nil, err
	}
	if record.Generation >= math.MaxInt64 {
		return nil, sessionrevision.ErrRewindUnavailable
	}
	restored, err := sessionrevision.RestoreBoundary(
		current,
		checkpoint.Boundary,
	)
	if err != nil {
		return nil, fmt.Errorf("restore rewind boundary: %w", err)
	}
	if err := sessionrevision.ResetProjectionFromBoundary(
		record, checkpoint.Boundary,
	); err != nil {
		return nil, err
	}
	record.Generation++
	record.Head++
	record.HeadRequestID = checkpoint.PriorHeadRequestID
	record.Checkpoint = nil
	sessionrevision.RecordRewindReplay(
		record,
		req.IdempotencyKey,
		sessionrevision.PersistedReplay{
			TargetRequestID:       req.TargetRequestID,
			ExpectedHeadRequestID: req.ExpectedHeadRequestID,
			Generation:            record.Generation,
			Head:                  record.Head,
		},
	)
	if err := s.replaceActiveSessionTx(
		ctx,
		tx,
		req.Key,
		restored,
		record,
		expiresAt,
		activeAt,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit session rewind: %w", err)
	}
	sessionrevision.AttachRewindFence(restored, record)
	return s.rewindResultWithScopedState(ctx, req.Key, restored)
}

func (s *Service) loadTurnBoundaryTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	record *sessionrevision.PersistedRecord,
	state *SessionState,
) (*session.Session, error) {
	activeAt := time.Now().UTC().UnixNano()
	intact := false
	var err error
	if sessionrevision.ProjectionInitialized(record) {
		intact, err = s.projectionStorageIntactTx(
			ctx, tx, key, record.Projection, activeAt,
		)
	}
	if err != nil {
		return nil, err
	}
	if !intact {
		active, _, err := s.loadActiveSessionTx(ctx, tx, key, activeAt)
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
	if err := s.loadActiveSummariesTx(
		ctx, tx, key, active, activeAt,
	); err != nil {
		return nil, err
	}
	return active, nil
}

func (s *Service) projectionStorageIntactTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	projection *sessionrevision.PersistedProjection,
	activeAt int64,
) (bool, error) {
	if projection == nil {
		return false, nil
	}
	var eventCount uint64
	if err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT COUNT(*) FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
			s.tableSessionEvents,
		),
		key.AppName, key.UserID, key.SessionID, activeAt,
	).Scan(&eventCount); err != nil {
		return false, fmt.Errorf("count active events: %w", err)
	}
	if eventCount != projection.Events.Count {
		return false, nil
	}
	rows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT track, COUNT(*) FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?) GROUP BY track`,
			s.tableSessionTracks,
		),
		key.AppName, key.UserID, key.SessionID, activeAt,
	)
	if err != nil {
		return false, fmt.Errorf("count active tracks: %w", err)
	}
	defer rows.Close()
	seen := make(map[session.Track]struct{})
	for rows.Next() {
		var track session.Track
		var count uint64
		if err := rows.Scan(&track, &count); err != nil {
			return false, err
		}
		prefix, ok := projection.Tracks[track]
		if !ok || prefix.Count != count {
			return false, nil
		}
		seen[track] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for track, prefix := range projection.Tracks {
		if prefix.Count == 0 {
			continue
		}
		if _, ok := seen[track]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func nullInt64Arg(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func (s *Service) rewindResultWithScopedState(
	ctx context.Context,
	key session.Key,
	active *session.Session,
) (*session.RewindResult, error) {
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
	return &session.RewindResult{
		Session: mergeState(appState, userState, active),
	}, nil
}

func (s *Service) loadActiveSessionTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	activeAtValues ...int64,
) (*session.Session, sql.NullInt64, error) {
	activeAt := time.Now().UTC().UnixNano()
	if len(activeAtValues) > 0 {
		activeAt = activeAtValues[0]
	}
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
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
AND (expires_at IS NULL OR expires_at > ?)`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		activeAt,
	).Scan(&stateRaw, &created, &updated, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.NullInt64{}, sessionrevision.ErrRewindUnavailable
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
	if err := s.loadActiveEventsTx(ctx, tx, key, active, activeAt); err != nil {
		return nil, sql.NullInt64{}, err
	}
	if err := s.loadActiveTracksTx(ctx, tx, key, active, activeAt); err != nil {
		return nil, sql.NullInt64{}, err
	}
	if err := s.loadActiveSummariesTx(ctx, tx, key, active, activeAt); err != nil {
		return nil, sql.NullInt64{}, err
	}
	return active, expires, nil
}

func (s *Service) loadActiveEventsTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	active *session.Session,
	activeAt int64,
) error {
	eventRows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
AND (expires_at IS NULL OR expires_at > ?)
ORDER BY created_at ASC, id ASC`,
			s.tableSessionEvents,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		activeAt,
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
	activeAt int64,
) error {
	trackRows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT track, event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
AND (expires_at IS NULL OR expires_at > ?)
ORDER BY created_at ASC, id ASC`,
			s.tableSessionTracks,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		activeAt,
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
	activeAt int64,
) error {
	summaryRows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT filter_key, summary FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
AND (expires_at IS NULL OR expires_at > ?)`,
			s.tableSessionSummaries,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		activeAt,
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
	activeAt int64,
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
	if err := s.trimActiveEventTailTx(
		ctx, tx, key, len(restored.Events), activeAt,
	); err != nil {
		return err
	}
	activeSummaryRows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT filter_key FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
			s.tableSessionSummaries,
		),
		key.AppName, key.UserID, key.SessionID, activeAt,
	)
	if err != nil {
		return fmt.Errorf("load active summary identities: %w", err)
	}
	activeSummaryKeys := make(map[string]struct{})
	for activeSummaryRows.Next() {
		var filterKey string
		if err := activeSummaryRows.Scan(&filterKey); err != nil {
			_ = activeSummaryRows.Close()
			return err
		}
		activeSummaryKeys[filterKey] = struct{}{}
	}
	if err := activeSummaryRows.Err(); err != nil {
		_ = activeSummaryRows.Close()
		return err
	}
	if err := activeSummaryRows.Close(); err != nil {
		return err
	}
	for filterKey, summary := range restored.Summaries {
		if summary == nil {
			continue
		}
		if _, ok := activeSummaryKeys[filterKey]; !ok {
			return fmt.Errorf(
				"summary %q checkpoint source is no longer active: %w",
				filterKey,
				sessionrevision.ErrRewindUnavailable,
			)
		}
	}
	if err := s.trimActiveTrackTailsTx(ctx, tx, key, restored, activeAt); err != nil {
		return err
	}
	for filterKey := range activeSummaryKeys {
		summary := restored.Summaries[filterKey]
		if summary == nil {
			statement := fmt.Sprintf(
				`DELETE FROM %s WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ? AND deleted_at IS NULL`,
				s.tableSessionSummaries,
			)
			args := []any{key.AppName, key.UserID, key.SessionID, filterKey}
			if s.opts.softDelete {
				statement = fmt.Sprintf(
					`UPDATE %s SET deleted_at = ? WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ? AND deleted_at IS NULL`,
					s.tableSessionSummaries,
				)
				args = append([]any{activeAt}, args...)
			}
			if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
				return fmt.Errorf("discard active summary: %w", err)
			}
			continue
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET summary = ?, updated_at = ?, expires_at = NULL
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
AND deleted_at IS NULL`,
				s.tableSessionSummaries,
			),
			raw,
			summary.UpdatedAt.UTC().UnixNano(),
			key.AppName,
			key.UserID,
			key.SessionID,
			filterKey,
		); err != nil {
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
	activeAt int64,
) error {
	rows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
AND (expires_at IS NULL OR expires_at > ?)
ORDER BY created_at ASC, id ASC`,
			s.tableSessionEvents,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		activeAt,
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
		return sessionrevision.ErrRewindUnavailable
	}
	if err := s.removeActiveTailRowsTx(
		ctx, tx, s.tableSessionEvents, key, ids[prefixLength:],
	); err != nil {
		return fmt.Errorf("remove discarded event tail: %w", err)
	}
	return nil
}

func (s *Service) trimActiveTrackTailsTx(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	restored *session.Session,
	activeAt int64,
) error {
	rows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, track, event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)
ORDER BY created_at, id`,
			s.tableSessionTracks,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		activeAt,
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
				sessionrevision.ErrRewindUnavailable,
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
					sessionrevision.ErrRewindUnavailable,
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
				sessionrevision.ErrRewindUnavailable,
			)
		}
	}
	if len(tail) == 0 {
		return nil
	}
	if err := s.removeActiveTailRowsTx(
		ctx, tx, s.tableSessionTracks, key, tail,
	); err != nil {
		return fmt.Errorf("remove discarded track tail: %w", err)
	}
	return nil
}

func (s *Service) removeActiveTailRowsTx(
	ctx context.Context,
	exec revisionExecer,
	table string,
	key session.Key,
	ids []int64,
) error {
	for start := 0; start < len(ids); start += revisionTailDeleteBatchSize {
		end := min(start+revisionTailDeleteBatchSize, len(ids))
		batch := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+4)
		for _, id := range batch {
			args = append(args, id)
		}
		var statement string
		if s.opts.softDelete {
			statement = fmt.Sprintf(
				`UPDATE %s SET deleted_at = ? WHERE id IN (%s)
AND app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
				table,
				placeholders,
			)
			args = append([]any{time.Now().UTC().UnixNano()}, args...)
		} else {
			statement = fmt.Sprintf(
				`DELETE FROM %s WHERE id IN (%s)
AND app_name = ? AND user_id = ? AND session_id = ?`,
				table,
				placeholders,
			)
		}
		args = append(args, key.AppName, key.UserID, key.SessionID)
		if _, err := exec.ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	return nil
}
