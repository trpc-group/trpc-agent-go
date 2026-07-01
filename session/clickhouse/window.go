//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
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
	eventID string
	entry   session.EventWindowEntry
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
		return nil, fmt.Errorf("%w: %s", session.ErrEventWindowAnchorNotFound, anchorEventID)
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
		return nil, fmt.Errorf("%w: %s", session.ErrEventWindowAnchorNotFound, anchorEventID)
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
	rows, err := s.chClient.Query(
		ctx,
		fmt.Sprintf(
			`SELECT created_at FROM %s FINAL
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
	defer rows.Close()

	if !rows.Next() {
		return time.Time{}, false, nil
	}
	var createdAt time.Time
	if err := rows.Scan(&createdAt); err != nil {
		return time.Time{}, false, fmt.Errorf("scan active session: %w", err)
	}
	return createdAt, true, nil
}

func (s *Service) loadWindowAnchor(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
	anchorEventID string,
	roleFilter map[model.Role]struct{},
) (*persistedWindowEntry, error) {
	rows, err := s.chClient.Query(
		ctx,
		fmt.Sprintf(
			`SELECT event_id, event, created_at FROM %s FINAL
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND created_at >= ?
AND event_id = ?
AND deleted_at IS NULL
ORDER BY created_at ASC, event_id ASC
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
	defer rows.Close()

	if !rows.Next() {
		return nil, nil
	}
	anchor, err := scanWindowRow(rows)
	if err != nil {
		return nil, err
	}
	if !sessionwindow.EventAllowed(&anchor.entry.Event, roleFilter) {
		return nil, nil
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event window anchor: %w", err)
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
	cursorEventID := anchor.eventID
	out := make([]session.EventWindowEntry, 0, limit)
	for len(out) < limit {
		rows, err := s.queryWindowNeighborBatch(
			ctx,
			key,
			sessionCreatedAt,
			cursorCreatedAt,
			cursorEventID,
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
			cursorEventID = row.eventID
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
	cursorEventID string,
	before bool,
) ([]*persistedWindowEntry, error) {
	comparator := `((created_at > ?) OR (created_at = ? AND event_id > ?))`
	orderBy := `ORDER BY created_at ASC, event_id ASC`
	if before {
		comparator = `((created_at < ?) OR (created_at = ? AND event_id < ?))`
		orderBy = `ORDER BY created_at DESC, event_id DESC`
	}

	rows, err := s.chClient.Query(
		ctx,
		fmt.Sprintf(
			`SELECT event_id, event, created_at FROM %s FINAL
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
		cursorEventID,
		eventWindowBatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("load event window neighbors: %w", err)
	}
	defer rows.Close()

	out := make([]*persistedWindowEntry, 0, eventWindowBatchSize)
	for rows.Next() {
		row, err := scanWindowRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event window neighbors: %w", err)
	}
	return out, nil
}

func scanWindowRow(rows interface {
	Scan(dest ...any) error
}) (*persistedWindowEntry, error) {
	var (
		eventID     string
		eventString string
		createdAt   time.Time
	)
	if err := rows.Scan(&eventID, &eventString, &createdAt); err != nil {
		return nil, fmt.Errorf("scan event window entry: %w", err)
	}
	var evt event.Event
	if err := json.Unmarshal([]byte(eventString), &evt); err != nil {
		return nil, fmt.Errorf("unmarshal event window entry: %w", err)
	}
	return &persistedWindowEntry{
		eventID: eventID,
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
