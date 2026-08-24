//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

const trackEventPageCursorKindPGVector = "pgvector"

type trackEventPageRow struct {
	id        int64
	event     session.TrackEvent
	createdAt time.Time
}

// GetTrackEventPage returns a cursor page of persisted track events.
func (s *Service) GetTrackEventPage(
	ctx context.Context,
	req session.TrackEventPageRequest,
) (*session.TrackEventPage, error) {
	if err := trackpage.ValidateRequest(req); err != nil {
		return nil, err
	}
	rows, hasMore, err := s.queryTrackEventPageRows(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("pgvector session service get track event page failed: %w", err)
	}
	entries, err := pgvectorTrackEventPageEntries(req, rows)
	if err != nil {
		return nil, fmt.Errorf("pgvector session service get track event page failed: %w", err)
	}
	return &session.TrackEventPage{
		Track:   req.Track,
		Entries: entries,
		HasMore: hasMore,
	}, nil
}

func (s *Service) queryTrackEventPageRows(
	ctx context.Context,
	req session.TrackEventPageRequest,
) ([]trackEventPageRow, bool, error) {
	query := fmt.Sprintf(`SELECT id, event, created_at FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3 AND track = $4
		AND (expires_at IS NULL OR expires_at > $5)
		AND deleted_at IS NULL`, s.tableSessionTracks)
	args := []any{req.Key.AppName, req.Key.UserID, req.Key.SessionID, req.Track, time.Now()}
	if req.Cursor != "" {
		cursor, err := trackpage.Decode(req.Cursor)
		if err != nil {
			return nil, false, err
		}
		if err := trackpage.ValidateBinding(cursor, trackEventPageCursorKindPGVector, req.Key, req.Track); err != nil {
			return nil, false, err
		}
		cursorID, err := trackpage.ParseIntID(cursor.ID)
		if err != nil {
			return nil, false, err
		}
		cursorCreatedAt := time.Unix(0, cursor.CreatedAt).UTC()
		query += `
		AND (created_at < $6 OR (created_at = $7 AND id < $8))`
		args = append(args, cursorCreatedAt, cursorCreatedAt, cursorID)
		query += `
		ORDER BY created_at DESC, id DESC
		LIMIT $9`
	} else {
		query += `
		ORDER BY created_at DESC, id DESC
		LIMIT $6`
	}
	args = append(args, req.EventLimit+1)
	rows := make([]trackEventPageRow, 0, req.EventLimit+1)
	err := s.pgClient.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			var row trackEventPageRow
			var eventBytes []byte
			if err := sqlRows.Scan(&row.id, &eventBytes, &row.createdAt); err != nil {
				return err
			}
			if err := json.Unmarshal(eventBytes, &row.event); err != nil {
				return fmt.Errorf("unmarshal track event: %w", err)
			}
			rows = append(rows, row)
		}
		return sqlRows.Err()
	}, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query track event page: %w", err)
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

func pgvectorTrackEventPageEntries(
	req session.TrackEventPageRequest,
	rows []trackEventPageRow,
) ([]session.TrackEventPageEntry, error) {
	entries := make([]session.TrackEventPageEntry, 0, len(rows))
	for _, row := range rows {
		cursor, err := trackpage.CursorFor(
			trackEventPageCursorKindPGVector,
			req.Key,
			req.Track,
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
