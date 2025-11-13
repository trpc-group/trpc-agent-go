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

	"github.com/spaolacci/murmur3"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// CreateSessionSummary creates a summary for a session.
func (s *Service) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	_, err := s.createSessionSummary(ctx, sess, filterKey, force)
	return err
}

// createSessionSummary is the internal implementation that returns the summary.
func (s *Service) createSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) (*session.Summary, error) {
	if s.opts.summarizer == nil {
		return nil, fmt.Errorf("summarizer not configured")
	}

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}

	// Check if summary already exists and is recent
	if !force {
		var existingSummary *session.Summary
		err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
			// rows.Next() is already called by the Query loop
			var summaryBytes []byte
			var updatedAt time.Time
			if err := rows.Scan(&summaryBytes, &updatedAt); err != nil {
				return err
			}
			var sum session.Summary
			if err := json.Unmarshal(summaryBytes, &sum); err != nil {
				return fmt.Errorf("unmarshal summary failed: %w", err)
			}
			sum.UpdatedAt = updatedAt
			existingSummary = &sum
			return nil
		}, fmt.Sprintf(`SELECT summary, updated_at FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
			AND (expires_at IS NULL OR expires_at > ?)
			AND deleted_at IS NULL`, s.tableSessionSummaries),
			key.AppName, key.UserID, key.SessionID, filterKey, time.Now())

		if err != nil {
			return nil, fmt.Errorf("check existing summary failed: %w", err)
		}

		if existingSummary != nil {
			// Check if summary is recent enough (within 1 minute of last event)
			if sess.UpdatedAt.Sub(existingSummary.UpdatedAt) < time.Minute {
				return existingSummary, nil
			}
		}
	}

	// Generate new summary
	summaryText, err := s.opts.summarizer.Summarize(ctx, sess)
	if err != nil {
		return nil, fmt.Errorf("generate summary failed: %w", err)
	}

	// Create summary object
	now := time.Now()
	summary := &session.Summary{
		Summary:   summaryText,
		Topics:    []string{},
		UpdatedAt: now,
	}

	// Store summary
	summaryBytes, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("marshal summary failed: %w", err)
	}

	expiresAt := calculateExpiresAt(s.sessionTTL)

	// Use UPSERT (MySQL syntax: ON DUPLICATE KEY UPDATE)
	_, err = s.mysqlClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULL)
		 ON DUPLICATE KEY UPDATE
		   summary = VALUES(summary),
		   updated_at = VALUES(updated_at),
		   expires_at = VALUES(expires_at),
		   deleted_at = NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, summaryBytes, summary.UpdatedAt, expiresAt)

	if err != nil {
		return nil, fmt.Errorf("upsert summary failed: %w", err)
	}

	return summary, nil
}

// EnqueueSummaryJob enqueues a summary job for async processing.
func (s *Service) EnqueueSummaryJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if s.opts.summarizer == nil {
		return fmt.Errorf("summarizer not configured")
	}

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	job := &summaryJob{
		sessionKey: key,
		filterKey:  filterKey,
		force:      force,
		session:    sess,
	}

	// Try to enqueue job
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok && err.Error() == "send on closed channel" {
				log.Errorf("mysql session service enqueue summary job failed: %v", r)
				return
			}
			panic(r)
		}
	}()

	n := len(s.summaryJobChans)
	if n == 0 {
		log.Warnf("summary workers not started, fallback to sync processing")
		return s.CreateSessionSummary(ctx, sess, filterKey, force)
	}
	index := int(murmur3.Sum32([]byte(fmt.Sprintf("%s:%s:%s", key.AppName, key.UserID, key.SessionID)))) % n

	select {
	case s.summaryJobChans[index] <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Queue is full, fallback to sync processing
		log.Warnf("summary job queue is full, fallback to sync processing")
		return s.CreateSessionSummary(ctx, sess, filterKey, force)
	}
}

// GetSessionSummaryText gets the summary text for a session.
func (s *Service) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
) (string, bool) {
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}

	// Use empty filterKey to get the default summary
	filterKey := ""
	var summaryText string
	err := s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
		// rows.Next() is already called by the Query loop
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
		AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, time.Now())

	if err != nil {
		return "", false
	}

	if summaryText == "" {
		return "", false
	}

	return summaryText, true
}
