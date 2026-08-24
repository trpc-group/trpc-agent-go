//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"strconv"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

const trackEventPageCursorKindSQLite = "sqlite"

type trackEventPageRow struct {
	id        int64
	event     session.TrackEvent
	createdAt int64
}

// GetTrackEventPage returns a cursor page of persisted track events.
func (s *Service) GetTrackEventPage(
	ctx context.Context,
	req session.TrackEventPageRequest,
) (*session.TrackEventPage, error) {
	if err := trackpage.ValidateRequest(req); err != nil {
		return nil, err
	}
	sessionCreatedAt, ok, err := s.getTrackEventPageSessionCreatedAt(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("sqlite session service get track event page failed: %w", err)
	}
	if !ok {
		return &session.TrackEventPage{Track: req.Track}, nil
	}
	rows, hasMore, err := s.queryTrackEventPageRows(ctx, req, sessionCreatedAt)
	if err != nil {
		return nil, fmt.Errorf("sqlite session service get track event page failed: %w", err)
	}
	entries, err := sqliteTrackEventPageEntries(req, sessionCreatedAt, rows)
	if err != nil {
		return nil, fmt.Errorf("sqlite session service get track event page failed: %w", err)
	}
	return &session.TrackEventPage{
		Track:   req.Track,
		Entries: entries,
		HasMore: hasMore,
	}, nil
}

func (s *Service) getTrackEventPageSessionCreatedAt(
	ctx context.Context,
	key session.Key,
) (time.Time, bool, error) {
	var createdNs int64
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT created_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND (expires_at IS NULL OR expires_at > ?)
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		time.Now().UTC().UnixNano(),
	).Scan(&createdNs)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("query session created_at: %w", err)
	}
	return unixNanoToTime(createdNs), true, nil
}

func (s *Service) queryTrackEventPageRows(
	ctx context.Context,
	req session.TrackEventPageRequest,
	sessionCreatedAt time.Time,
) ([]trackEventPageRow, bool, error) {
	query := fmt.Sprintf(
		`SELECT id, event, created_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ?
AND (expires_at IS NULL OR expires_at > ?)
AND deleted_at IS NULL`,
		s.tableSessionTracks,
	)
	args := []any{req.Key.AppName, req.Key.UserID, req.Key.SessionID, req.Track, time.Now().UTC().UnixNano()}
	if req.Cursor != "" {
		cursor, err := trackpage.Decode(req.Cursor)
		if err != nil {
			return nil, false, err
		}
		if err := trackpage.ValidateBinding(cursor, trackEventPageCursorKindSQLite, req.Key, req.Track, sessionCreatedAt); err != nil {
			return nil, false, err
		}
		cursorID, err := trackpage.ParseIntID(cursor.ID)
		if err != nil {
			return nil, false, err
		}
		query += `
AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursorID)
	}
	query += `
ORDER BY created_at DESC, id DESC
LIMIT ?`
	args = append(args, req.EventLimit+1)
	sqlRows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query track event page: %w", err)
	}
	defer sqlRows.Close()
	rows := make([]trackEventPageRow, 0, req.EventLimit+1)
	for sqlRows.Next() {
		var row trackEventPageRow
		var eventBytes []byte
		if err := sqlRows.Scan(&row.id, &eventBytes, &row.createdAt); err != nil {
			return nil, false, fmt.Errorf("scan track event page: %w", err)
		}
		if err := json.Unmarshal(eventBytes, &row.event); err != nil {
			return nil, false, fmt.Errorf("unmarshal track event: %w", err)
		}
		rows = append(rows, row)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate track event page: %w", err)
	}
	hasMore := len(rows) > req.EventLimit
	if hasMore {
		rows = rows[:req.EventLimit]
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, hasMore, nil
}

func sqliteTrackEventPageEntries(
	req session.TrackEventPageRequest,
	sessionCreatedAt time.Time,
	rows []trackEventPageRow,
) ([]session.TrackEventPageEntry, error) {
	entries := make([]session.TrackEventPageEntry, 0, len(rows))
	for _, row := range rows {
		cursor, err := trackpage.CursorForUnixNano(
			trackEventPageCursorKindSQLite,
			req.Key,
			req.Track,
			sessionCreatedAt,
			row.createdAt,
			strconv.FormatInt(row.id, 10),
		)
		if err != nil {
			return nil, err
		}
		entries = append(entries, session.TrackEventPageEntry{
			Event:  row.event,
			Cursor: cursor,
		})
	}
	return entries, nil
}
