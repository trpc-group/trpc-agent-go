//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

// CreateSessionSummary generates and persists a summary for the session.
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

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key: %w", err)
	}

	updated, err := isummary.SummarizeSession(
		ctx,
		s.opts.summarizer,
		sess,
		filterKey,
		force,
	)
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
		return fmt.Errorf("marshal summary: %w", err)
	}

	const insertSQL = `INSERT INTO %s (
  app_name, user_id, session_id, filter_key, summary, updated_at, expires_at,
  deleted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)
ON CONFLICT(app_name, user_id, session_id, filter_key) DO UPDATE SET
  summary = excluded.summary,
  updated_at = excluded.updated_at,
  expires_at = excluded.expires_at,
  deleted_at = NULL`

	_, err = s.db.ExecContext(
		ctx,
		fmt.Sprintf(insertSQL, s.tableSessionSummaries),
		key.AppName,
		key.UserID,
		key.SessionID,
		filterKey,
		summaryBytes,
		sum.UpdatedAt.UTC().UnixNano(),
		nil,
	)
	if err != nil {
		return fmt.Errorf("upsert summary: %w", err)
	}
	return nil
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
func (s *Service) EnqueueSummaryJob(
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

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key: %w", err)
	}

	if s.asyncWorker != nil {
		return s.asyncWorker.EnqueueJob(ctx, sess, filterKey, force)
	}

	return isummary.CreateSessionSummaryWithCascade(
		ctx,
		sess,
		filterKey,
		force,
		s.CreateSessionSummary,
	)
}

// GetSessionSummaryText returns the latest summary text for the session.
func (s *Service) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	if sess == nil {
		return "", false
	}

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}

	if text, ok := isummary.GetSummaryTextFromSession(sess, opts...); ok {
		return text, true
	}

	filterKey := isummary.GetFilterKeyFromOptions(opts...)
	text, ok := s.getSummaryTextFromDB(ctx, key, sess.CreatedAt, filterKey)
	if ok {
		return text, true
	}

	if filterKey != session.SummaryFilterKeyAllContents {
		text, ok = s.getSummaryTextFromDB(
			ctx,
			key,
			sess.CreatedAt,
			session.SummaryFilterKeyAllContents,
		)
		if ok {
			return text, true
		}
	}

	return "", false
}

func (s *Service) getSummaryTextFromDB(
	ctx context.Context,
	key session.Key,
	createdAt time.Time,
	filterKey string,
) (string, bool) {
	const selectSQL = `SELECT summary FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
AND (expires_at IS NULL OR expires_at > ?)
AND updated_at >= ?
AND deleted_at IS NULL
LIMIT 1`

	var summaryBytes []byte
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(selectSQL, s.tableSessionSummaries),
		key.AppName,
		key.UserID,
		key.SessionID,
		filterKey,
		time.Now().UTC().UnixNano(),
		createdAt.UTC().UnixNano(),
	).Scan(&summaryBytes)
	if err != nil {
		return "", false
	}

	var sum session.Summary
	if err := json.Unmarshal(summaryBytes, &sum); err != nil {
		return "", false
	}
	if sum.Summary == "" {
		return "", false
	}
	return sum.Summary, true
}
