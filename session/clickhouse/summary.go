//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
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
	if err != nil {
		return fmt.Errorf("summarize and persist failed: %w", err)
	}
	if !updated {
		return nil
	}

	// Persist only the updated filterKey summary with atomic set-if-newer to avoid late-write override.
	sess.SummariesMu.RLock()
	summary := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()
	summaryBytes, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshal summary failed: %w", err)
	}

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := summary.UpdatedAt.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	now := time.Now()
	// INSERT new version (ReplacingMergeTree will deduplicate based on updated_at)
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, created_at, updated_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, string(summaryBytes), now, summary.UpdatedAt, expiresAt)

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

	// Refresh summary TTLs if session has TTL configured and we're accessing summaries
	// This ensures summaries remain valid when session TTL is refreshed
	if s.opts.sessionTTL > 0 {
		if err := s.refreshSessionSummaryTTLs(ctx, key); err != nil {
			// Log warning but don't fail the operation
			// This is defensive to avoid breaking summary access due to TTL refresh failures
		}
	}

	// Try in-memory summaries first.
	if text, ok := isummary.GetSummaryTextFromSession(sess, opts...); ok {
		return text, true
	}

	// Query database with specified filterKey.
	filterKey := isummary.GetFilterKeyFromOptions(opts...)

	var summaryText string
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT summary FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
		AND (expires_at IS NULL OR expires_at > ?)
		AND updated_at >= ?
		AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, time.Now(), sess.CreatedAt)

	if err != nil {
		return "", false
	}
	defer rows.Close()

	if rows.Next() {
		var summaryBytes []byte
		if err := rows.Scan(&summaryBytes); err != nil {
			return "", false
		}
		var sum session.Summary
		if err := json.Unmarshal(summaryBytes, &sum); err != nil {
			return "", false
		}
		summaryText = sum.Summary
	}

	if summaryText != "" {
		return summaryText, true
	}

	// If requested filterKey not found, try fallback to full-session summary.
	if filterKey != session.SummaryFilterKeyAllContents {
		rows2, err := s.chClient.Query(ctx,
			fmt.Sprintf(`SELECT summary FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND updated_at >= ?
			AND deleted_at IS NULL`, s.tableSessionSummaries),
			key.AppName, key.UserID, key.SessionID, session.SummaryFilterKeyAllContents, time.Now(), sess.CreatedAt)

		if err == nil {
			defer rows2.Close()
			if rows2.Next() {
				var summaryBytes []byte
				if err := rows2.Scan(&summaryBytes); err == nil {
					var sum session.Summary
					if err := json.Unmarshal(summaryBytes, &sum); err == nil {
						summaryText = sum.Summary
					}
				}
			}
		}
		if summaryText != "" {
			return summaryText, true
		}
	}

	return "", false
}

// refreshSessionSummaryTTLs updates the expires_at timestamps of all summaries for a session.
// This ensures summaries remain valid when the session TTL is refreshed.
func (s *Service) refreshSessionSummaryTTLs(ctx context.Context, key session.Key) error {
	now := time.Now()
	expiresAt := now.Add(s.opts.sessionTTL)

	// Update all summaries for this session
	err := s.chClient.Exec(ctx,
		fmt.Sprintf(`UPDATE %s SET expires_at = ? WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			s.tableSessionSummaries),
		expiresAt, key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("refresh session summary TTLs failed: %w", err)
	}

	return nil
}
