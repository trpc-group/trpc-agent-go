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
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackcursor"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

// readTrackEvents reads committed track rows by persistent ID in one snapshot.
// AppendTrackEvent acquires the session row lock before allocating a track ID,
// so a later append cannot commit ahead of an earlier ID for that session.
func (s *Service) readTrackEvents(ctx context.Context, key session.Key, track session.Track,
	cursor string) (*session.TrackEvents, string, error) {
	return trackcursor.Read(ctx, key, track, cursor, s.opts.getSessionHooks,
		func(c *session.GetSessionContext) (*session.Session, string, error) {
			if err := c.Key.CheckSessionKey(); err != nil {
				return nil, "", err
			}
			if err := session.ValidateGetSessionOptions(c.Options, false); err != nil {
				return nil, "", err
			}
			position, err := trackcursor.Decode(cursor, "mysql/"+s.tableSessionTracks, c.Key, track)
			if err != nil {
				return nil, "", err
			}
			var snapshot *session.Session
			err = s.mysqlClient.Transaction(c.Context, func(tx *sql.Tx) error {
				client := cursorTransaction{tx: tx}
				now := time.Now()
				var generation string
				rows := 0
				err := client.Query(c.Context, func(row *sql.Rows) error {
					var id uint64
					var data []byte
					var created time.Time
					if err := row.Scan(&id, &data, &created); err != nil {
						return err
					}
					var state SessionState
					if err := json.Unmarshal(data, &state); err != nil {
						return err
					}
					if !state.CreatedAt.IsZero() {
						created = state.CreatedAt
					}
					generation = fmt.Sprintf("%d/%s", id, created.UTC().Format(time.RFC3339Nano))
					rows++
					return nil
				}, fmt.Sprintf(`SELECT id, state, created_at FROM %s
					WHERE app_name = ? AND user_id = ? AND session_id = ?
					AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, s.tableSessionStates),
					c.Key.AppName, c.Key.UserID, c.Key.SessionID, now)
				if err != nil {
					return err
				}
				if rows > 1 || (position.Generation != "" && position.Generation != generation) {
					return session.ErrTrackCursorInvalid
				}
				if rows == 0 {
					return nil
				}
				position.Generation = generation
				if position.Position > 0 {
					var count int
					err := client.QueryRow(c.Context, []any{&count}, fmt.Sprintf(`SELECT COUNT(*) FROM %s
						WHERE id = ? AND app_name = ? AND user_id = ? AND session_id = ? AND track = ?
						AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, s.tableSessionTracks),
						position.Position, c.Key.AppName, c.Key.UserID, c.Key.SessionID, track, now)
					if err != nil {
						return err
					}
					if count != 1 {
						return session.ErrTrackCursorInvalid
					}
				}
				// Hooks may inspect the complete Session projection. Reuse its
				// loader on this transaction without running the hook chain twice.
				snapshot = session.NewSession(c.Key.AppName, c.Key.UserID, c.Key.SessionID)
				if len(s.opts.getSessionHooks) > 0 {
					view := &Service{opts: s.opts, mysqlClient: client,
						tableSessionStates: s.tableSessionStates, tableSessionEvents: s.tableSessionEvents,
						tableSessionTracks: s.tableSessionTracks, tableSessionSummaries: s.tableSessionSummaries,
						tableAppStates: s.tableAppStates, tableUserStates: s.tableUserStates}
					snapshot, err = view.getSession(c.Context, c.Key, c.Options.EventNum, c.Options.EventTime, nil)
					if err != nil {
						return err
					}
					if snapshot == nil {
						return session.ErrTrackCursorInvalid
					}
				}
				events := &session.TrackEvents{Track: track}
				entries, lastID, err := s.readCursorTrackRows(c.Context, client, c.Key, track, position.Position, now)
				if err != nil {
					return err
				}
				position.Position = lastID
				for _, event := range entries {
					if c.Options.EventTime.IsZero() || event.Timestamp.After(c.Options.EventTime) {
						events.Events = append(events.Events, event)
					}
				}
				if n := c.Options.EventNum; n > 0 && len(events.Events) > n {
					events.Events = events.Events[len(events.Events)-n:]
				}
				if snapshot.Tracks == nil {
					snapshot.Tracks = make(map[session.Track]*session.TrackEvents)
				}
				snapshot.Tracks[track] = events
				return nil
			}, func(opts *sql.TxOptions) { opts.Isolation = sql.LevelRepeatableRead; opts.ReadOnly = true })
			if err != nil {
				return nil, "", err
			}
			return snapshot, trackcursor.Encode(position), nil
		})
}

func (s *Service) readCursorTrackRows(ctx context.Context, client cursorTransaction, key session.Key,
	track session.Track, after uint64, now time.Time) ([]session.TrackEvent, uint64, error) {
	var ids []uint64
	// Sort only row IDs; sorting full event JSON can exhaust MySQL's sort buffer.
	err := client.Query(ctx, func(row *sql.Rows) error {
		var id uint64
		if err := row.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	}, fmt.Sprintf(`SELECT id FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ? AND id > ?
		AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?) ORDER BY id ASC`, s.tableSessionTracks),
		key.AppName, key.UserID, key.SessionID, track, after, now)
	if err != nil {
		return nil, after, err
	}
	var result []session.TrackEvent
	const payloadBatchSize = 128
	for begin := 0; begin < len(ids); begin += payloadBatchSize {
		end := min(begin+payloadBatchSize, len(ids))
		args := []any{key.AppName, key.UserID, key.SessionID, track}
		for _, id := range ids[begin:end] {
			args = append(args, id)
		}
		loaded := make(map[uint64]session.TrackEvent, end-begin)
		err := client.Query(ctx, func(row *sql.Rows) error {
			var id uint64
			var data []byte
			if err := row.Scan(&id, &data); err != nil {
				return err
			}
			var event session.TrackEvent
			if err := json.Unmarshal(data, &event); err != nil {
				return err
			}
			loaded[id] = event
			return nil
		}, fmt.Sprintf(`SELECT id, event FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ? AND id IN (%s)`,
			s.tableSessionTracks, strings.TrimSuffix(strings.Repeat("?,", end-begin), ",")), args...)
		if err != nil {
			return nil, after, err
		}
		for _, id := range ids[begin:end] {
			event, ok := loaded[id]
			if !ok {
				return nil, after, session.ErrTrackCursorInvalid
			}
			result = append(result, event)
			after = id
		}
	}
	return result, after, nil
}

// cursorTransaction lets the existing Session projection loader share the read
// snapshot. It never opens another connection or commits the caller's transaction.
type cursorTransaction struct{ tx *sql.Tx }

func (c cursorTransaction) Query(ctx context.Context, next storage.NextFunc, query string, args ...any) error {
	rows, err := c.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := next(rows); err != nil {
			if errors.Is(err, storage.ErrBreak) {
				break
			}
			return err
		}
	}
	return rows.Err()
}
func (c cursorTransaction) QueryRow(ctx context.Context, dest []any, query string, args ...any) error {
	return c.tx.QueryRowContext(ctx, query, args...).Scan(dest...)
}
func (c cursorTransaction) Exec(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("track cursor transaction is read-only")
}
func (c cursorTransaction) Transaction(context.Context, storage.TxFunc, ...storage.TxOption) error {
	return errors.New("nested track cursor transaction")
}
func (c cursorTransaction) Close() error { return nil }
