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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionwindow "trpc.group/trpc-go/trpc-agent-go/session/internal/window"
)

var _ session.WindowService = (*Service)(nil)

// GetEventWindow loads a small ordered event window around one anchor event.
func (s *Service) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	if err := req.Key.CheckSessionKey(); err != nil {
		return nil, err
	}

	sessionCreatedAt, ok, err := s.loadActiveSessionCreatedAt(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return sessionwindow.EventWindowFromOrderedEntries(req.Key, nil, req)
	}

	entries, err := s.loadWindowEntries(ctx, req.Key, sessionCreatedAt)
	if err != nil {
		return nil, err
	}
	return sessionwindow.EventWindowFromOrderedEntries(req.Key, entries, req)
}

func (s *Service) loadActiveSessionCreatedAt(
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
		return time.Time{}, false, fmt.Errorf("load active session: %w", err)
	}
	return unixNanoToTime(createdNs), true, nil
}

func (s *Service) loadWindowEntries(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
) ([]session.EventWindowEntry, error) {
	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT event, created_at FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND created_at >= ?
AND deleted_at IS NULL
ORDER BY created_at ASC, rowid ASC`,
			s.tableSessionEvents,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		sessionCreatedAt.UTC().UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("load event window entries: %w", err)
	}
	defer rows.Close()

	var entries []session.EventWindowEntry
	for rows.Next() {
		var (
			eventBytes []byte
			createdNs  int64
		)
		if err := rows.Scan(&eventBytes, &createdNs); err != nil {
			return nil, fmt.Errorf("scan event window entry: %w", err)
		}
		var evt event.Event
		if err := json.Unmarshal(eventBytes, &evt); err != nil {
			return nil, fmt.Errorf("unmarshal event window entry: %w", err)
		}
		entries = append(entries, session.EventWindowEntry{
			Event:     evt,
			CreatedAt: unixNanoToTime(createdNs),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event window entries: %w", err)
	}
	return entries, nil
}
