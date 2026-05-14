//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

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
)

var _ session.WindowService = (*Service)(nil)

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
		return nil, fmt.Errorf(
			"event window requires before >= 0 and after >= 0",
		)
	}

	roleFilter := makeRoleFilter(req.Roles)
	roleNames := compactRoles(req.Roles)

	anchor, err := s.loadWindowAnchor(
		ctx,
		req.Key,
		anchorEventID,
		roleNames,
		roleFilter,
	)
	if err != nil {
		return nil, fmt.Errorf("load event window: %w", err)
	}
	if anchor == nil {
		return nil, fmt.Errorf(
			"anchor event not found: %s",
			anchorEventID,
		)
	}

	beforeEntries, err := s.loadWindowNeighbors(
		ctx,
		req.Key,
		anchor.entry.CreatedAt,
		anchor.rowID,
		req.Before,
		roleNames,
		roleFilter,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("load event window: %w", err)
	}
	afterEntries, err := s.loadWindowNeighbors(
		ctx,
		req.Key,
		anchor.entry.CreatedAt,
		anchor.rowID,
		req.After,
		roleNames,
		roleFilter,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("load event window: %w", err)
	}
	reverseWindowEntries(beforeEntries)

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

func (s *Service) loadWindowAnchor(
	ctx context.Context,
	key session.Key,
	anchorEventID string,
	roles []string,
	roleFilter map[model.Role]struct{},
) (*persistedWindowEntry, error) {
	query := fmt.Sprintf(
		`SELECT se.id, se.event, se.created_at
		FROM %s se
		WHERE se.app_name = $1 AND se.user_id = $2
		AND se.session_id = $3
		AND se.deleted_at IS NULL
		AND (se.expires_at IS NULL OR se.expires_at > NOW() AT TIME ZONE 'localtime')
		AND se.content_text <> ''
		AND se.event->>'id' = $4`,
		s.tableSessionEvents,
	)

	args := []any{key.AppName, key.UserID, key.SessionID, anchorEventID}
	if len(roles) > 0 {
		query += fmt.Sprintf(
			` AND se.role = ANY($%d::varchar[])`,
			len(args)+1,
		)
		args = append(args, roles)
	}
	query += ` ORDER BY se.created_at ASC, se.id ASC LIMIT 1`

	var anchor *persistedWindowEntry
	err := s.pgClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			if !rows.Next() {
				return nil
			}

			var (
				rowID      int64
				eventBytes []byte
				createdAt  time.Time
			)
			if err := rows.Scan(&rowID, &eventBytes, &createdAt); err != nil {
				return fmt.Errorf("scan event window anchor row: %w", err)
			}

			entry, ok, err := decodeWindowEntry(
				eventBytes,
				createdAt,
				roleFilter,
			)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}

			anchor = &persistedWindowEntry{
				rowID: rowID,
				entry: entry,
			}
			return nil
		},
		query,
		args...,
	)
	if err != nil {
		return nil, err
	}
	return anchor, nil
}

func (s *Service) loadWindowNeighbors(
	ctx context.Context,
	key session.Key,
	anchorCreatedAt time.Time,
	anchorRowID int64,
	limit int,
	roles []string,
	roleFilter map[model.Role]struct{},
	before bool,
) ([]session.EventWindowEntry, error) {
	if limit <= 0 {
		return nil, nil
	}

	comparator := `((se.created_at < $4) OR (se.created_at = $4 AND se.id < $5))`
	orderBy := `ORDER BY se.created_at DESC, se.id DESC`
	if !before {
		comparator = `((se.created_at > $4) OR (se.created_at = $4 AND se.id > $5))`
		orderBy = `ORDER BY se.created_at ASC, se.id ASC`
	}

	query := fmt.Sprintf(
		`SELECT se.event, se.created_at
		FROM %s se
		WHERE se.app_name = $1 AND se.user_id = $2
		AND se.session_id = $3
		AND se.deleted_at IS NULL
		AND (se.expires_at IS NULL OR se.expires_at > NOW() AT TIME ZONE 'localtime')
		AND se.content_text <> ''
		AND %s`,
		s.tableSessionEvents,
		comparator,
	)

	args := []any{key.AppName, key.UserID, key.SessionID, anchorCreatedAt, anchorRowID}
	if len(roles) > 0 {
		query += fmt.Sprintf(
			` AND se.role = ANY($%d::varchar[])`,
			len(args)+1,
		)
		args = append(args, roles)
	}
	query += fmt.Sprintf(" %s LIMIT %d", orderBy, limit)

	entries := make([]session.EventWindowEntry, 0, limit)
	err := s.pgClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var (
					eventBytes []byte
					createdAt  time.Time
				)
				if err := rows.Scan(&eventBytes, &createdAt); err != nil {
					return fmt.Errorf("scan event window row: %w", err)
				}

				entry, ok, err := decodeWindowEntry(
					eventBytes,
					createdAt,
					roleFilter,
				)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}

				entries = append(entries, entry)
			}
			return nil
		},
		query,
		args...,
	)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func decodeWindowEntry(
	eventBytes []byte,
	createdAt time.Time,
	roleFilter map[model.Role]struct{},
) (session.EventWindowEntry, bool, error) {
	var evt event.Event
	if err := json.Unmarshal(eventBytes, &evt); err != nil {
		return session.EventWindowEntry{}, false, fmt.Errorf(
			"unmarshal event window row: %w",
			err,
		)
	}
	if !eventAllowedInWindow(&evt, roleFilter) {
		return session.EventWindowEntry{}, false, nil
	}
	return session.EventWindowEntry{
		Event:     evt,
		CreatedAt: createdAt,
	}, true, nil
}

func reverseWindowEntries(entries []session.EventWindowEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}

func makeRoleFilter(
	roles []model.Role,
) map[model.Role]struct{} {
	if len(roles) == 0 {
		return nil
	}
	filter := make(map[model.Role]struct{}, len(roles))
	for _, role := range roles {
		role = model.Role(strings.TrimSpace(string(role)))
		if role == "" {
			continue
		}
		filter[role] = struct{}{}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

func eventAllowedInWindow(
	evt *event.Event,
	roleFilter map[model.Role]struct{},
) bool {
	if len(roleFilter) == 0 {
		return true
	}
	_, role, ok := extractWindowEventText(evt)
	if !ok {
		return false
	}
	_, ok = roleFilter[role]
	return ok
}

func extractWindowEventText(
	evt *event.Event,
) (string, model.Role, bool) {
	if evt == nil || evt.Response == nil || evt.Response.IsPartial ||
		len(evt.Response.Choices) == 0 {
		return "", "", false
	}

	msg := evt.Response.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		return "", "", false
	}

	role := msg.Role
	if role == "" {
		role = model.RoleAssistant
	}
	if msg.ToolID != "" || role == model.RoleTool {
		role = model.RoleTool
	}
	if role != model.RoleUser && role != model.RoleAssistant && role != model.RoleTool {
		return "", "", false
	}

	text := strings.TrimSpace(msg.Content)
	if text == "" && len(msg.ContentParts) > 0 {
		var parts []string
		for _, part := range msg.ContentParts {
			if part.Text == nil {
				continue
			}
			partText := strings.TrimSpace(*part.Text)
			if partText == "" {
				continue
			}
			parts = append(parts, partText)
		}
		text = strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if text == "" {
		return "", "", false
	}
	if role == model.RoleTool {
		toolName := strings.TrimSpace(msg.ToolName)
		if toolName != "" {
			text = toolName + ": " + text
		}
	}
	return text, role, true
}
