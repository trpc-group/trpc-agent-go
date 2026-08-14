//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sqlrevision implements the common private revision protocol used by
// transactional SQL session backends.
package sqlrevision

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

// Dialect identifies the SQL placeholder and upsert syntax used by a store.
type Dialect int

const (
	// PostgreSQL selects PostgreSQL-compatible SQL.
	PostgreSQL Dialect = iota
	// MySQL selects MySQL-compatible SQL.
	MySQL
)

// Tables identifies the active session projection tables.
type Tables struct {
	States    string
	Events    string
	Tracks    string
	Summaries string
}

// Store implements revision persistence for one SQL session backend.
type Store struct {
	Dialect Dialect
	Tables  Tables
	// SoftDelete preserves tombstoned projection rows during restoration.
	SoftDelete bool
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type rowsQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type projectionDB interface {
	rowQuerier
	rowsQuerier
	execer
}

type stateEnvelope struct {
	ID        string           `json:"id"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

func (s Store) bind(position int) string {
	if s.Dialect == MySQL {
		return "?"
	}
	return fmt.Sprintf("$%d", position)
}

func (s Store) jsonArg(raw []byte) any {
	if s.Dialect == MySQL {
		return string(raw)
	}
	return raw
}

// Read loads the revision sidecar from the owning session state row and
// optionally locks that row for update.
func (s Store) Read(
	ctx context.Context,
	query rowQuerier,
	key session.Key,
	forUpdate bool,
) (*sessionrevision.PersistedRecord, error) {
	statement := fmt.Sprintf(
		`SELECT state FROM %s WHERE app_name = %s AND user_id = %s AND session_id = %s AND deleted_at IS NULL`,
		s.Tables.States,
		s.bind(1),
		s.bind(2),
		s.bind(3),
	)
	if forUpdate {
		statement += " FOR UPDATE"
	}
	var raw []byte
	err := query.QueryRowContext(
		ctx,
		statement,
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return &sessionrevision.PersistedRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session revision: %w", err)
	}
	var envelope stateEnvelope
	record, err := sessionrevision.DecodeState(raw, &envelope)
	if err != nil {
		return nil, err
	}
	return record, nil
}

// Generation loads the active revision generation.
func (s Store) Generation(
	ctx context.Context,
	query rowQuerier,
	key session.Key,
) (uint64, error) {
	record, err := s.Read(ctx, query, key, false)
	if err != nil {
		return 0, err
	}
	return record.Generation, nil
}

// Generations loads revision generations for keys in one query. Missing rows
// are reported as generation zero.
func (s Store) Generations(
	ctx context.Context,
	query rowsQuerier,
	keys []session.Key,
) (map[session.Key]uint64, error) {
	generations := make(map[session.Key]uint64, len(keys))
	for _, key := range keys {
		generations[key] = 0
	}
	if len(keys) == 0 {
		return generations, nil
	}
	clauses := make([]string, len(keys))
	args := make([]any, 0, len(keys)*3)
	for i, key := range keys {
		position := i*3 + 1
		clauses[i] = fmt.Sprintf(
			"(app_name = %s AND user_id = %s AND session_id = %s)",
			s.bind(position), s.bind(position+1), s.bind(position+2),
		)
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}
	rows, err := query.QueryContext(
		ctx,
		fmt.Sprintf(
			"SELECT app_name, user_id, session_id, state FROM %s WHERE deleted_at IS NULL AND (%s)",
			s.Tables.States,
			strings.Join(clauses, " OR "),
		),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("read session revisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key session.Key
		var raw []byte
		if err := rows.Scan(&key.AppName, &key.UserID, &key.SessionID, &raw); err != nil {
			return nil, fmt.Errorf("scan session revision: %w", err)
		}
		var envelope stateEnvelope
		record, err := sessionrevision.DecodeState(raw, &envelope)
		if err != nil {
			return nil, err
		}
		generations[key] = record.Generation
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session revisions: %w", err)
	}
	return generations, nil
}

// ApplyEventWrite validates and advances revision state in the same
// transaction as an event mutation. The session state row must already be
// locked by the caller.
func (s Store) ApplyEventWrite(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	record *sessionrevision.PersistedRecord,
	write sessionrevision.Write,
	evt *event.Event,
	persisted bool,
) error {
	if err := checkGeneration(record, write); err != nil {
		return err
	}
	if write.Start != nil {
		current, _, err := s.LoadActive(ctx, tx, key, false)
		if err != nil {
			return fmt.Errorf("load authoritative pre-turn session: %w", err)
		}
		write.Boundary, err = sessionrevision.NewBoundary(current)
		if err != nil {
			return fmt.Errorf("capture session boundary before latest turn: %w", err)
		}
	}
	sessionrevision.ApplyEventWrite(record, write, evt, persisted)
	return nil
}

// ApplyTrackWrite validates and advances revision state in the same
// transaction as a track mutation.
func (s Store) ApplyTrackWrite(
	record *sessionrevision.PersistedRecord,
	write sessionrevision.Write,
	trackEvent *session.TrackEvent,
) error {
	if err := checkGeneration(record, write); err != nil {
		return err
	}
	sessionrevision.ApplyTrackWrite(record, write, trackEvent)
	return nil
}

// ApplyMutation validates and advances revision state for a non-event session
// mutation. Callers may use ContextWithHazard to make the open checkpoint
// ineligible for replacement.
func (s Store) ApplyMutation(
	record *sessionrevision.PersistedRecord,
	write sessionrevision.Write,
) error {
	if err := checkGeneration(record, write); err != nil {
		return err
	}
	sessionrevision.ApplyWrite(record, write)
	return nil
}

func checkGeneration(
	record *sessionrevision.PersistedRecord,
	write sessionrevision.Write,
) error {
	if write.HasExpectedGeneration && record.Generation != write.ExpectedGeneration {
		return sessionrevision.ErrStaleGeneration
	}
	return nil
}

// LoadActive loads a complete active projection. It does not merge app- or
// user-scoped state.
func (s Store) LoadActive(
	ctx context.Context,
	query projectionDB,
	key session.Key,
	forUpdate bool,
) (*session.Session, *time.Time, error) {
	stateStatement := fmt.Sprintf(
		`SELECT state, created_at, updated_at, expires_at FROM %s
WHERE app_name = %s AND user_id = %s AND session_id = %s AND deleted_at IS NULL`,
		s.Tables.States,
		s.bind(1),
		s.bind(2),
		s.bind(3),
	)
	if forUpdate {
		stateStatement += " FOR UPDATE"
	}
	var (
		stateRaw []byte
		created  time.Time
		updated  time.Time
		expires  sql.NullTime
	)
	if err := query.QueryRowContext(
		ctx,
		stateStatement,
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&stateRaw, &created, &updated, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, sessionrevision.ErrLatestTurnReplacementUnavailable
		}
		return nil, nil, fmt.Errorf("load active session state: %w", err)
	}
	var envelope stateEnvelope
	if _, err := sessionrevision.DecodeState(stateRaw, &envelope); err != nil {
		return nil, nil, err
	}
	active := session.NewSession(
		key.AppName,
		key.UserID,
		key.SessionID,
		session.WithSessionState(envelope.State),
		session.WithSessionCreatedAt(created),
		session.WithSessionUpdatedAt(updated),
	)
	if err := s.loadEvents(ctx, query, key, active); err != nil {
		return nil, nil, err
	}
	if s.Tables.Tracks != "" {
		if err := s.loadTracks(ctx, query, key, active); err != nil {
			return nil, nil, err
		}
	}
	if err := s.loadSummaries(ctx, query, key, active); err != nil {
		return nil, nil, err
	}
	if expires.Valid {
		return active, &expires.Time, nil
	}
	return active, nil, nil
}

func (s Store) loadEvents(
	ctx context.Context,
	query rowsQuerier,
	key session.Key,
	active *session.Session,
) error {
	rows, err := query.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT event FROM %s WHERE app_name = %s AND user_id = %s AND session_id = %s AND deleted_at IS NULL ORDER BY created_at, id`,
			s.Tables.Events,
			s.bind(1),
			s.bind(2),
			s.bind(3),
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("load active events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var evt event.Event
		if err := json.Unmarshal(raw, &evt); err != nil {
			return err
		}
		active.Events = append(active.Events, evt)
	}
	return rows.Err()
}

func (s Store) loadTracks(
	ctx context.Context,
	query rowsQuerier,
	key session.Key,
	active *session.Session,
) error {
	rows, err := query.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT track, event FROM %s WHERE app_name = %s AND user_id = %s AND session_id = %s AND deleted_at IS NULL ORDER BY created_at, id`,
			s.Tables.Tracks,
			s.bind(1),
			s.bind(2),
			s.bind(3),
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("load active tracks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			track session.Track
			raw   []byte
		)
		if err := rows.Scan(&track, &raw); err != nil {
			return err
		}
		var trackEvent session.TrackEvent
		if err := json.Unmarshal(raw, &trackEvent); err != nil {
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
	return rows.Err()
}

func (s Store) loadSummaries(
	ctx context.Context,
	query rowsQuerier,
	key session.Key,
	active *session.Session,
) error {
	rows, err := query.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT filter_key, summary FROM %s WHERE app_name = %s AND user_id = %s AND session_id = %s AND deleted_at IS NULL`,
			s.Tables.Summaries,
			s.bind(1),
			s.bind(2),
			s.bind(3),
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("load active summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			filterKey string
			raw       []byte
		)
		if err := rows.Scan(&filterKey, &raw); err != nil {
			return err
		}
		var summary session.Summary
		if err := json.Unmarshal(raw, &summary); err != nil {
			return err
		}
		if active.Summaries == nil {
			active.Summaries = make(map[string]*session.Summary)
		}
		active.Summaries[filterKey] = &summary
	}
	return rows.Err()
}

// ReplaceLatestTurn performs the atomic revision transition inside tx. The
// caller must merge scoped state after the transaction commits.
func (s Store) ReplaceLatestTurn(
	ctx context.Context,
	tx *sql.Tx,
	req sessionrevision.LatestTurnReplacementRequest,
) (*sessionrevision.LatestTurnReplacementResult, error) {
	current, expiresAt, err := s.LoadActive(ctx, tx, req.Key, true)
	if err != nil {
		return nil, err
	}
	record, err := s.Read(ctx, tx, req.Key, true)
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
		sessionrevision.SetGeneration(current, record.Generation)
		return &sessionrevision.LatestTurnReplacementResult{
			ActiveSession: current,
			Applied:       false,
		}, nil
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
	restored, err := sessionrevision.RestoreBoundary(current, checkpoint.Boundary)
	if err != nil {
		return nil, fmt.Errorf("restore latest-turn boundary: %w", err)
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
	if err := s.restoreProjection(
		ctx, tx, req.Key, restored, record, expiresAt,
	); err != nil {
		return nil, err
	}
	sessionrevision.SetGeneration(restored, record.Generation)
	return &sessionrevision.LatestTurnReplacementResult{
		ActiveSession: restored,
		Applied:       true,
	}, nil
}

func (s Store) restoreProjection(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	restored *session.Session,
	record *sessionrevision.PersistedRecord,
	expiresAt *time.Time,
) error {
	if err := s.trimEventTail(ctx, tx, key, len(restored.Events)); err != nil {
		return err
	}
	stateRaw, err := sessionrevision.EncodeState(stateEnvelope{
		ID:        key.SessionID,
		State:     restored.SnapshotState(),
		CreatedAt: restored.CreatedAt,
		UpdatedAt: restored.UpdatedAt,
	}, record)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET state = %s, created_at = %s, updated_at = %s, expires_at = %s
WHERE app_name = %s AND user_id = %s AND session_id = %s AND deleted_at IS NULL`,
			s.Tables.States,
			s.bind(1),
			s.bind(2),
			s.bind(3),
			s.bind(4),
			s.bind(5),
			s.bind(6),
			s.bind(7),
		),
		s.jsonArg(stateRaw),
		restored.CreatedAt,
		restored.UpdatedAt,
		expiresAt,
		key.AppName,
		key.UserID,
		key.SessionID,
	); err != nil {
		return fmt.Errorf("restore session state: %w", err)
	}
	if s.Tables.Tracks != "" {
		if err := s.trimTrackTails(ctx, tx, key, restored); err != nil {
			return err
		}
	}
	return s.replaceSummaries(ctx, tx, key, restored, expiresAt)
}

func (s Store) trimEventTail(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	prefixLength int,
) error {
	rows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id FROM %s WHERE app_name = %s AND user_id = %s AND session_id = %s AND deleted_at IS NULL ORDER BY created_at, id FOR UPDATE`,
			s.Tables.Events,
			s.bind(1),
			s.bind(2),
			s.bind(3),
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("lock active events: %w", err)
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
		return fmt.Errorf("iterate active events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(ids) <= prefixLength {
		return sessionrevision.ErrLatestTurnReplacementUnavailable
	}
	tail := ids[prefixLength:]
	placeholders := make([]string, len(tail))
	args := make([]any, len(tail))
	for i, id := range tail {
		placeholders[i] = s.bind(i + 1)
		args[i] = id
	}
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			"DELETE FROM %s WHERE id IN (%s) "+
				"AND app_name = %s AND user_id = %s AND session_id = %s",
			s.Tables.Events,
			strings.Join(placeholders, ", "),
			s.bind(len(tail)+1),
			s.bind(len(tail)+2),
			s.bind(len(tail)+3),
		),
		append(args, key.AppName, key.UserID, key.SessionID)...,
	); err != nil {
		return fmt.Errorf("remove discarded event tail: %w", err)
	}
	return nil
}

func (s Store) trimTrackTails(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	restored *session.Session,
) error {
	rows, err := tx.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, track, event FROM %s
WHERE app_name = %s AND user_id = %s AND session_id = %s
AND deleted_at IS NULL ORDER BY created_at, id FOR UPDATE`,
			s.Tables.Tracks,
			s.bind(1),
			s.bind(2),
			s.bind(3),
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
	placeholders := make([]string, len(tail))
	args := make([]any, len(tail), len(tail)+3)
	for i, id := range tail {
		placeholders[i] = s.bind(i + 1)
		args[i] = id
	}
	args = append(args, key.AppName, key.UserID, key.SessionID)
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			"DELETE FROM %s WHERE id IN (%s) "+
				"AND app_name = %s AND user_id = %s AND session_id = %s",
			s.Tables.Tracks,
			strings.Join(placeholders, ", "),
			s.bind(len(tail)+1),
			s.bind(len(tail)+2),
			s.bind(len(tail)+3),
		),
		args...,
	); err != nil {
		return fmt.Errorf("remove discarded track tail: %w", err)
	}
	return nil
}

func (s Store) replaceSummaries(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	restored *session.Session,
	expiresAt *time.Time,
) error {
	// #nosec G201 -- table names are assembled from validated service prefixes.
	statement := fmt.Sprintf(
		`DELETE FROM %s WHERE app_name = %s AND user_id = %s AND session_id = %s`,
		s.Tables.Summaries,
		s.bind(1),
		s.bind(2),
		s.bind(3),
	)
	if s.SoftDelete {
		statement += " AND deleted_at IS NULL"
	}
	if _, err := tx.ExecContext(
		ctx,
		statement,
		key.AppName,
		key.UserID,
		key.SessionID,
	); err != nil {
		return fmt.Errorf("clear active summaries: %w", err)
	}
	for filterKey, summary := range restored.Summaries {
		if summary == nil {
			continue
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		// #nosec G201 -- table names are assembled from validated service prefixes.
		statement := fmt.Sprintf(
			`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
VALUES (%s, %s, %s, %s, %s, %s, %s, NULL)`,
			s.Tables.Summaries,
			s.bind(1), s.bind(2), s.bind(3), s.bind(4),
			s.bind(5), s.bind(6), s.bind(7),
		)
		if _, err := tx.ExecContext(
			ctx,
			statement,
			key.AppName,
			key.UserID,
			key.SessionID,
			filterKey,
			s.jsonArg(raw),
			summary.UpdatedAt,
			expiresAt,
		); err != nil {
			return fmt.Errorf("restore active summary: %w", err)
		}
	}
	return nil
}
