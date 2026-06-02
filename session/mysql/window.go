//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionwindow "trpc.group/trpc-go/trpc-agent-go/session/internal/window"
)

var _ session.WindowService = (*Service)(nil)

const eventWindowBatchSize = 64

type persistedWindowEntry struct {
	rowID int64
	entry session.EventWindowEntry
}

// GetEventWindow loads a small ordered event window around one anchor event.
func (s *Service) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	if err := req.Key.CheckSessionKey(); err != nil {
		return nil, err
	}
	anchorEventID := strings.TrimSpace(req.AnchorEventID)
	if anchorEventID == "" {
		return nil, fmt.Errorf("anchor event id is required")
	}
	if req.Before < 0 || req.After < 0 {
		return nil, fmt.Errorf("event window requires before >= 0 and after >= 0")
	}

	sessionCreatedAt, ok, err := s.loadActiveSessionCreatedAt(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("anchor event not found: %s", anchorEventID)
	}

	roleFilter := sessionwindow.MakeRoleFilter(req.Roles)
	anchor, err := s.loadWindowAnchor(
		ctx,
		req.Key,
		sessionCreatedAt,
		anchorEventID,
		roleFilter,
	)
	if err != nil {
		return nil, err
	}
	if anchor == nil {
		return nil, fmt.Errorf("anchor event not found: %s", anchorEventID)
	}

	beforeEntries, err := s.loadWindowNeighbors(
		ctx,
		req.Key,
		sessionCreatedAt,
		anchor,
		req.Before,
		roleFilter,
		true,
	)
	if err != nil {
		return nil, err
	}
	afterEntries, err := s.loadWindowNeighbors(
		ctx,
		req.Key,
		sessionCreatedAt,
		anchor,
		req.After,
		roleFilter,
		false,
	)
	if err != nil {
		return nil, err
	}

	entries := make([]session.EventWindowEntry, 0, len(beforeEntries)+1+len(afterEntries))
	entries = append(entries, beforeEntries...)
	entries = append(entries, anchor.entry)
	entries = append(entries, afterEntries...)
	return &session.EventWindow{
		SessionKey:    req.Key,
		AnchorEventID: anchorEventID,
		Entries:       entries,
	}, nil
}

func (s *Service) loadActiveSessionCreatedAt(
	ctx context.Context,
	key session.Key,
) (time.Time, bool, error) {
	var (
		createdAt time.Time
		found     bool
	)
	err := s.mysqlClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			found = true
			return rows.Scan(&createdAt)
		},
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
		time.Now(),
	)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("load active session: %w", err)
	}
	return createdAt, found, nil
}

func (s *Service) loadWindowAnchor(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
	anchorEventID string,
	roleFilter map[model.Role]struct{},
) (*persistedWindowEntry, error) {
	var anchor *persistedWindowEntry
	err := s.mysqlClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			row, err := scanWindowRow(rows)
			if err != nil {
				return err
			}
			if !sessionwindow.EventAllowed(&row.entry.Event, roleFilter) {
				return nil
			}
			anchor = row
			return nil
		},
		fmt.Sprintf(
			`SELECT id, event, created_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND created_at >= ?
AND JSON_UNQUOTE(JSON_EXTRACT(event, '$.id')) = ?
AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC
LIMIT 1`,
			s.tableSessionEvents,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		sessionCreatedAt,
		anchorEventID,
	)
	if err != nil {
		return nil, fmt.Errorf("load event window anchor: %w", err)
	}
	return anchor, nil
}

func (s *Service) loadWindowNeighbors(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
	anchor *persistedWindowEntry,
	limit int,
	roleFilter map[model.Role]struct{},
	before bool,
) ([]session.EventWindowEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	cursorCreatedAt := anchor.entry.CreatedAt
	cursorRowID := anchor.rowID
	out := make([]session.EventWindowEntry, 0, limit)
	for len(out) < limit {
		rows, err := s.queryWindowNeighborBatch(
			ctx,
			key,
			sessionCreatedAt,
			cursorCreatedAt,
			cursorRowID,
			before,
		)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			cursorCreatedAt = row.entry.CreatedAt
			cursorRowID = row.rowID
			if !sessionwindow.EventAllowed(&row.entry.Event, roleFilter) {
				continue
			}
			out = append(out, row.entry)
			if len(out) >= limit {
				break
			}
		}
		if len(rows) < eventWindowBatchSize {
			break
		}
	}
	if before {
		reverseWindowEntries(out)
	}
	return out, nil
}

func (s *Service) queryWindowNeighborBatch(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
	cursorCreatedAt time.Time,
	cursorRowID int64,
	before bool,
) ([]*persistedWindowEntry, error) {
	comparator := `((created_at > ?) OR (created_at = ? AND id > ?))`
	orderBy := `ORDER BY created_at ASC, id ASC`
	if before {
		comparator = `((created_at < ?) OR (created_at = ? AND id < ?))`
		orderBy = `ORDER BY created_at DESC, id DESC`
	}

	rows := make([]*persistedWindowEntry, 0, eventWindowBatchSize)
	err := s.mysqlClient.Query(
		ctx,
		func(sqlRows *sql.Rows) error {
			row, err := scanWindowRow(sqlRows)
			if err != nil {
				return err
			}
			rows = append(rows, row)
			return nil
		},
		fmt.Sprintf(
			`SELECT id, event, created_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND created_at >= ?
AND deleted_at IS NULL
AND %s
%s
LIMIT ?`,
			s.tableSessionEvents,
			comparator,
			orderBy,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		sessionCreatedAt,
		cursorCreatedAt,
		cursorCreatedAt,
		cursorRowID,
		eventWindowBatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("load event window neighbors: %w", err)
	}
	return rows, nil
}

func scanWindowRow(rows *sql.Rows) (*persistedWindowEntry, error) {
	var (
		rowID      int64
		eventBytes []byte
		createdAt  time.Time
	)
	if err := rows.Scan(&rowID, &eventBytes, &createdAt); err != nil {
		return nil, fmt.Errorf("scan event window entry: %w", err)
	}
	var evt event.Event
	if err := json.Unmarshal(eventBytes, &evt); err != nil {
		return nil, fmt.Errorf("unmarshal event window entry: %w", err)
	}
	return &persistedWindowEntry{
		rowID: rowID,
		entry: session.EventWindowEntry{
			Event:     evt,
			CreatedAt: createdAt,
		},
	}, nil
}

func reverseWindowEntries(entries []session.EventWindowEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}
