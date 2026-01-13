//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

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
	if s.opts.summarizer == nil {
		return nil
	}

	if sess == nil {
		return session.ErrNilSession
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}

	updated, err := isummary.SummarizeSession(ctx, s.opts.summarizer, sess, filterKey, force)
	if err != nil || !updated {
		return err
	}

	// Persist to PostgreSQL.
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

	// Note: expires_at is set to NULL - summaries are bound to session
	// lifecycle and will be deleted when session is deleted or expires.
	// Use UPSERT (INSERT ... ON CONFLICT) for atomic operation.
	// This handles both insert and update in a single, race-condition-free operation.
	// Note: Last write wins - no timestamp comparison to avoid silent failures.
	_, err = s.pgClient.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
		 ON CONFLICT (app_name, user_id, session_id, filter_key) WHERE deleted_at IS NULL
		 DO UPDATE SET
		   summary = EXCLUDED.summary,
		   updated_at = EXCLUDED.updated_at,
		   expires_at = EXCLUDED.expires_at`, s.tableSessionSummaries),
		sess.AppName, sess.UserID, sess.ID, filterKey, summaryBytes, sum.UpdatedAt, nil)

	if err != nil {
		return fmt.Errorf("upsert summary failed: %w", err)
	}

	return nil
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
func (s *Service) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	if s.opts.summarizer == nil {
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

	// Fallback to synchronous processing.
	return isummary.CreateSessionSummaryWithCascade(ctx, sess, filterKey, force, s.CreateSessionSummary)
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
	err := s.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			var summaryBytes []byte
			if err := rows.Scan(&summaryBytes); err != nil {
				return err
			}
			var sum session.Summary
			if err := json.Unmarshal(summaryBytes, &sum); err != nil {
				return fmt.Errorf("unmarshal summary failed: %w", err)
			}
			summaryText = sum.Summary
		}
		return nil
	}, fmt.Sprintf(`SELECT summary FROM %s
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3 AND filter_key = $4
		AND (expires_at IS NULL OR expires_at > $5)
		AND updated_at >= $6
		AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, time.Now(), sess.CreatedAt)

	if err == nil && summaryText != "" {
		return summaryText, true
	}

	// If requested filterKey not found, try fallback to full-session summary.
	if filterKey != session.SummaryFilterKeyAllContents {
		err = s.pgClient.Query(ctx, func(rows *sql.Rows) error {
			if rows.Next() {
				var summaryBytes []byte
				if err := rows.Scan(&summaryBytes); err != nil {
					return err
				}
				var sum session.Summary
				if err := json.Unmarshal(summaryBytes, &sum); err != nil {
					return fmt.Errorf("unmarshal summary failed: %w", err)
				}
				summaryText = sum.Summary
			}
			return nil
		}, fmt.Sprintf(`SELECT summary FROM %s
			WHERE app_name = $1 AND user_id = $2 AND session_id = $3 AND filter_key = $4
			AND (expires_at IS NULL OR expires_at > $5)
			AND updated_at >= $6
			AND deleted_at IS NULL`, s.tableSessionSummaries),
			key.AppName, key.UserID, key.SessionID, session.SummaryFilterKeyAllContents, time.Now(), sess.CreatedAt)
		if err == nil && summaryText != "" {
			return summaryText, true
		}
	}

	return "", false
}
