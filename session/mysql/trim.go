//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const trimDeleteBatchSize = 500

type trimEventOptions struct {
	count int
}

// TrimConversationOption customizes TrimConversations.
type TrimConversationOption func(*trimEventOptions)

// WithCount sets the number of recent conversations to trim. A conversation
// consists of all events with the same non-empty RequestID. Non-positive counts
// are treated as one. When supplied more than once, the last count takes effect.
func WithCount(n int) TrimConversationOption {
	return func(o *trimEventOptions) {
		o.count = n
	}
}

// TrimConversations deletes the most recent conversations and returns their
// persisted events in ascending Timestamp order, breaking ties by event ID and
// then database row ID. It selects conversations by their latest event in that
// order and deletes every event with a selected RequestID, including interleaved
// events. Events without a RequestID are preserved. The default count is one.
//
// Deletion follows WithSoftDelete. Only events are changed: session state,
// summaries, tracks, session timestamps, TTLs, and previously loaded Session
// objects are not updated. Missing, deleted, expired, or empty sessions return
// nil, nil. Invalid keys and storage failures return an error.
//
// The operation reads all active events in the current session lifecycle,
// independently of event limits and hooks, and deletes the selected events in
// one transaction.
// It does not wait for asynchronous persistence or cancel running requests.
// Callers needing a complete trim must ensure the target requests have finished
// and their events have been persisted. Repeated calls trim further history;
// there is no automatic retry, including after an ambiguous commit failure.
func (s *Service) TrimConversations(
	ctx context.Context,
	key session.Key,
	options ...TrimConversationOption,
) ([]event.Event, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opt := trimEventOptions{count: 1}
	for _, option := range options {
		option(&opt)
	}
	if opt.count <= 0 {
		opt.count = 1
	}

	var deleted []event.Event
	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		// AppendEvent takes the same parent-row lock before inserting events.
		// Read DB creation time rather than the serialized state's timestamp so
		// an older incarnation of the same session cannot contribute events.
		var createdAt time.Time
		var expiresAt sql.NullTime
		err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT created_at, expires_at FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL FOR UPDATE`, s.tableSessionStates),
			key.AppName, key.UserID, key.SessionID).Scan(&createdAt, &expiresAt)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock session: %w", err)
		}
		// Match GetSession's expires_at > now filter and cleanup's
		// expires_at <= now boundary; trimming does not revive expired sessions.
		if expiresAt.Valid && !expiresAt.Time.After(time.Now()) {
			return nil
		}

		events, err := s.loadTrimEvents(ctx, tx, key, createdAt)
		if err != nil {
			return err
		}
		selected := selectTrimEvents(events, opt.count)
		if err := s.deleteTrimEvents(ctx, tx, key, selected); err != nil {
			return err
		}
		if len(selected) > 0 {
			deleted = make([]event.Event, len(selected))
			for i, row := range selected {
				deleted[i] = row.event
			}
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("trim conversations: %w", err)
	}
	return deleted, nil
}

type trimEvent struct {
	id    int64
	event event.Event
}

func (s *Service) loadTrimEvents(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	createdAt time.Time,
) ([]trimEvent, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT id, event FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND created_at >= ? AND deleted_at IS NULL FOR UPDATE`, s.tableSessionEvents),
		key.AppName, key.UserID, key.SessionID, createdAt)
	if err != nil {
		return nil, fmt.Errorf("load trim events: %w", err)
	}
	defer rows.Close()

	var events []trimEvent
	for rows.Next() {
		var row trimEvent
		var payload []byte
		if err := rows.Scan(&row.id, &payload); err != nil {
			return nil, fmt.Errorf("scan trim event: %w", err)
		}
		if err := json.Unmarshal(payload, &row.event); err != nil {
			return nil, fmt.Errorf("unmarshal trim event: %w", err)
		}
		events = append(events, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trim events: %w", err)
	}
	return events, nil
}

// selectTrimEvents uses a total order so selection and returned events are
// deterministic regardless of database row order.
func selectTrimEvents(events []trimEvent, count int) []trimEvent {
	slices.SortFunc(events, func(a, b trimEvent) int {
		if c := a.event.Timestamp.Compare(b.event.Timestamp); c != 0 {
			return c
		}
		if c := cmp.Compare(a.event.ID, b.event.ID); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	// Do not size this map from count, which may far exceed the actual history.
	targets := make(map[string]struct{})
	for i := len(events) - 1; i >= 0 && len(targets) < count; i-- {
		if requestID := events[i].event.RequestID; requestID != "" {
			targets[requestID] = struct{}{}
		}
	}
	// Selection is separate from collection: request events need not be
	// contiguous, and a selected request can span any number of SQL batches.
	var selected []trimEvent
	for _, row := range events {
		if row.event.RequestID == "" {
			continue
		}
		if _, ok := targets[row.event.RequestID]; ok {
			selected = append(selected, row)
		}
	}
	return selected
}

func (s *Service) deleteTrimEvents(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	events []trimEvent,
) error {
	// #nosec G201 -- NewService builds the table name from a validated prefix; values are bound below.
	queryPrefix := fmt.Sprintf("DELETE FROM %s", s.tableSessionEvents)
	if s.opts.softDelete {
		queryPrefix = fmt.Sprintf("UPDATE %s SET deleted_at = ?", s.tableSessionEvents)
	}
	// Keep user_id explicit for TDSQL shard routing; row IDs alone are
	// insufficient because the distributed table's PK is (id, user_id).
	queryPrefix += " WHERE app_name = ? AND user_id = ? AND session_id = ?" +
		" AND deleted_at IS NULL AND id IN ("
	now := time.Now()
	for start := 0; start < len(events); start += trimDeleteBatchSize {
		batch := events[start:min(start+trimDeleteBatchSize, len(events))]
		query := queryPrefix + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") + ")"
		args := make([]any, 0, len(batch)+4)
		if s.opts.softDelete {
			args = append(args, now)
		}
		args = append(args, key.AppName, key.UserID, key.SessionID)
		for _, row := range batch {
			args = append(args, row.id)
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("delete trim events: %w", err)
		}
	}
	return nil
}
