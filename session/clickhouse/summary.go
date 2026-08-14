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

	"trpc.group/trpc-go/trpc-agent-go/event"
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
	stale, err := s.summaryWriteIsStale(
		ctx,
		key,
		filterKey,
		sess.CreatedAt,
		summary,
		sess.Events,
	)
	if err != nil {
		return fmt.Errorf("check existing summary failed: %w", err)
	}
	if stale {
		return nil
	}

	// Note: expires_at is set to NULL - summaries are bound to session
	// lifecycle and will be deleted when session is deleted or expires.
	now := time.Now().UTC()
	updatedAt := summaryUpdatedAt(summary, now)
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, created_at, updated_at, version_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, string(summaryBytes), now, updatedAt, now, nil)

	if err != nil {
		return fmt.Errorf("upsert summary failed: %w", err)
	}

	return nil
}

func summaryUpdatedAt(summary *session.Summary, fallback time.Time) time.Time {
	if summary == nil {
		return fallback.UTC()
	}
	if cutoff := summary.CutoffTime(); !cutoff.IsZero() {
		return cutoff.UTC()
	}
	return fallback.UTC()
}

func (s *Service) summaryWriteIsStale(
	ctx context.Context,
	key session.Key,
	filterKey string,
	sessionCreatedAt time.Time,
	next *session.Summary,
	events []event.Event,
) (bool, error) {
	current, err := s.getPersistedSummary(ctx, key, filterKey, sessionCreatedAt)
	if err != nil || current == nil {
		return false, err
	}
	return summaryBoundaryBefore(next, current, events), nil
}

func (s *Service) getPersistedSummary(
	ctx context.Context,
	key session.Key,
	filterKey string,
	sessionCreatedAt time.Time,
) (*session.Summary, error) {
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT summary FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
			AND updated_at >= ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, sessionCreatedAt, time.Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, nil
	}
	var summaryBytes []byte
	if err := rows.Scan(&summaryBytes); err != nil {
		return nil, err
	}
	var sum session.Summary
	if err := json.Unmarshal(summaryBytes, &sum); err != nil {
		return nil, err
	}
	return &sum, nil
}

func summaryBoundaryBefore(
	next *session.Summary,
	current *session.Summary,
	events []event.Event,
) bool {
	if next == nil || current == nil {
		return false
	}
	nextBoundary := next.CutoffBoundary()
	currentBoundary := current.CutoffBoundary()
	if nextBoundary == nil || currentBoundary == nil {
		return false
	}
	nextCutoff := nextBoundary.CutoffTime()
	currentCutoff := currentBoundary.CutoffTime()
	if nextCutoff.IsZero() || currentCutoff.IsZero() {
		return false
	}
	if nextCutoff.Before(currentCutoff) {
		return true
	}
	if nextCutoff.After(currentCutoff) {
		return false
	}
	nextIndex, nextOK := summaryBoundaryEventIndex(events, nextBoundary)
	currentIndex, currentOK := summaryBoundaryEventIndex(events, currentBoundary)
	return nextOK && currentOK && nextIndex < currentIndex
}

func summaryBoundaryEventIndex(
	events []event.Event,
	boundary *session.SummaryBoundary,
) (int, bool) {
	if boundary == nil || boundary.LastEventID == "" {
		return 0, false
	}
	for i := range events {
		if events[i].ID == boundary.LastEventID {
			return i, true
		}
	}
	return 0, false
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
	rows, err := s.chClient.Query(ctx,
		fmt.Sprintf(`SELECT summary FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
		AND updated_at >= ?
		AND (expires_at IS NULL OR expires_at > ?)
		AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, sess.CreatedAt, time.Now())

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

	// If requested filterKey not found, try fallback to full-session summary.
	if summaryText == "" && filterKey != session.SummaryFilterKeyAllContents {
		rows2, err := s.chClient.Query(ctx,
			fmt.Sprintf(`SELECT summary FROM %s FINAL
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
			AND updated_at >= ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND deleted_at IS NULL`, s.tableSessionSummaries),
			key.AppName, key.UserID, key.SessionID, session.SummaryFilterKeyAllContents, sess.CreatedAt, time.Now())

		if err != nil {
			return "", false
		}
		defer rows2.Close()

		if rows2.Next() {
			var summaryBytes []byte
			if err := rows2.Scan(&summaryBytes); err != nil {
				return "", false
			}
			var sum session.Summary
			if err := json.Unmarshal(summaryBytes, &sum); err != nil {
				return "", false
			}
			summaryText = sum.Summary
		}
	}

	if summaryText == "" {
		return "", false
	}
	return summaryText, true
}
