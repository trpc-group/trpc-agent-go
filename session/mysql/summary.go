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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

// CreateSessionSummary is the internal implementation that returns the summary.
func (s *Service) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if !isummary.HasSummarizer(s.opts.summarizer) {
		return nil
	}

	if sess == nil {
		return session.ErrNilSession
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}
	if !isummary.NewSummaryDispatchPolicy(
		s.opts.summaryFilterAllowlist,
		s.opts.shouldCascadeFullSessionSummary(),
	).AllowsFilterKey(filterKey) {
		return nil
	}

	updated, err := isummary.SummarizeSession(ctx, s.opts.summarizer, sess, filterKey, force)
	if err != nil || !updated {
		return err
	}

	sess.SummariesMu.RLock()
	sum := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()

	if sum == nil {
		return nil
	}

	summaryBytes, err := json.Marshal(sum)
	if err != nil {
		return fmt.Errorf("marshal summary failed: %w", err)
	}

	return s.upsertSessionSummary(ctx, key, filterKey, summaryBytes, sum.UpdatedAt)
}

// upsertSessionSummary serializes summary persistence through the parent
// session row. This keeps writes correct for both the current four-column
// unique index and legacy schemas whose nullable deleted_at column does not
// prevent duplicate active summaries.
func (s *Service) upsertSessionSummary(
	ctx context.Context,
	key session.Key,
	filterKey string,
	summaryBytes []byte,
	updatedAt time.Time,
) error {
	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		if err := s.lockActiveSessionForSummary(ctx, tx, key); err != nil {
			return err
		}

		persistedUpdatedAt, exists, err := s.latestActiveSummaryUpdatedAtForUpdate(
			ctx, tx, key, filterKey,
		)
		if err != nil {
			return err
		}
		if exists {
			// A summary may be generated before waiting for this transaction's
			// parent-session lock. Do not let an older cutoff overwrite a newer
			// committed summary. Equal cutoffs remain last-write-wins so callers
			// can force regeneration for the same summarized history.
			if persistedUpdatedAt.After(updatedAt) {
				return nil
			}

			// Update every active copy so reads remain consistent while legacy
			// duplicate rows are being cleaned up online.
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s
					SET summary = ?, updated_at = ?, expires_at = NULL
					WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
					AND deleted_at IS NULL`, s.tableSessionSummaries),
				string(summaryBytes), updatedAt,
				key.AppName, key.UserID, key.SessionID, filterKey)
			if err != nil {
				return fmt.Errorf("update active summaries failed: %w", err)
			}
			return nil
		}

		// Keep ON DUPLICATE KEY UPDATE for the current schema: when only a
		// soft-deleted row exists, its four-column unique key must be revived.
		// Legacy indexes do not conflict with that row and insert a new active
		// summary instead.
		_, err = tx.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s
					(app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, NULL)
				ON DUPLICATE KEY UPDATE
					summary = VALUES(summary),
					updated_at = VALUES(updated_at),
					expires_at = VALUES(expires_at),
					deleted_at = NULL`, s.tableSessionSummaries),
			key.AppName, key.UserID, key.SessionID, filterKey,
			string(summaryBytes), updatedAt, nil)
		if err != nil {
			return fmt.Errorf("insert or revive summary failed: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("upsert summary failed: %w", err)
	}
	return nil
}

func (s *Service) lockActiveSessionForSummary(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
) error {
	rows, err := tx.QueryContext(ctx,
		fmt.Sprintf(`SELECT id FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL
			FOR UPDATE`, s.tableSessionStates),
		key.AppName, key.UserID, key.SessionID)
	if err != nil {
		return fmt.Errorf("lock session for summary failed: %w", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan session lock row failed: %w", err)
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate session lock rows failed: %w", err)
	}
	if !found {
		return errSessionNotFound
	}
	return nil
}

func (s *Service) latestActiveSummaryUpdatedAtForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	key session.Key,
	filterKey string,
) (time.Time, bool, error) {
	var updatedAt time.Time
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT updated_at FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
			AND deleted_at IS NULL
			ORDER BY updated_at DESC, id DESC
			LIMIT 1
			FOR UPDATE`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey).Scan(&updatedAt)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("lock active summaries failed: %w", err)
	}
	return updatedAt, true, nil
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
func (s *Service) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	if !isummary.HasSummarizer(s.opts.summarizer) {
		return nil
	}

	if sess == nil {
		return session.ErrNilSession
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}

	if s.asyncWorker != nil {
		return s.asyncWorker.EnqueueJob(ctx, sess, filterKey, force)
	}

	// Fallback to synchronous processing with the same detached context that
	// async workers use.
	return isummary.CreateSessionSummaryWithCascade(
		isummary.DetachContext(ctx),
		sess,
		filterKey,
		force,
		isummary.NewSummaryDispatchPolicy(
			s.opts.summaryFilterAllowlist,
			s.opts.shouldCascadeFullSessionSummary(),
		),
		s.CreateSessionSummary,
	)
}

// GetSessionSummaryText gets the summary text for a session.
// When no options are provided, returns the full-session summary (SummaryFilterKeyAllContents).
// Use session.WithSummaryFilterKey to specify a different filter key.
func (s *Service) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	// Check session validity.
	if sess == nil {
		return "", false
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}

	// Try in-memory summaries first.
	if text, ok := isummary.GetSummaryTextFromSession(sess, opts...); ok {
		return text, true
	}

	// Query database with specified filterKey.
	filterKey := isummary.GetFilterKeyFromOptions(opts...)

	var summaryText string
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop.
		var summaryBytes []byte
		if err := rows.Scan(&summaryBytes); err != nil {
			return err
		}
		var sum session.Summary
		if err := json.Unmarshal(summaryBytes, &sum); err != nil {
			return fmt.Errorf("unmarshal summary failed: %w", err)
		}
		summaryText = sum.Summary
		return nil
	}, fmt.Sprintf(`SELECT summary FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
		AND (expires_at IS NULL OR expires_at > ?)
		AND updated_at >= ?
		AND deleted_at IS NULL
		ORDER BY updated_at DESC, id DESC
		LIMIT 1`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, time.Now(), sess.CreatedAt)

	if err != nil {
		return "", false
	}

	if summaryText != "" {
		return summaryText, true
	}

	// If requested filterKey not found, try fallback to full-session summary.
	if filterKey != session.SummaryFilterKeyAllContents {
		err = s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
			// rows.Next() is already called by the Query loop.
			var summaryBytes []byte
			if err := rows.Scan(&summaryBytes); err != nil {
				return err
			}
			var sum session.Summary
			if err := json.Unmarshal(summaryBytes, &sum); err != nil {
				return fmt.Errorf("unmarshal summary failed: %w", err)
			}
			summaryText = sum.Summary
			return nil
		}, fmt.Sprintf(`SELECT summary FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND updated_at >= ?
			AND deleted_at IS NULL
			ORDER BY updated_at DESC, id DESC
			LIMIT 1`, s.tableSessionSummaries),
			key.AppName, key.UserID, key.SessionID, session.SummaryFilterKeyAllContents, time.Now(), sess.CreatedAt)

		if err == nil && summaryText != "" {
			return summaryText, true
		}
	}

	return "", false
}
