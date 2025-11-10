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
	"errors"
	"fmt"
	"time"

	"github.com/spaolacci/murmur3"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isession "trpc.group/trpc-go/trpc-agent-go/session/internal/session"
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
		return errors.New("nil session")
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}

	updated, err := isession.SummarizeSession(ctx, s.opts.summarizer, sess, filterKey, force)
	if err != nil {
		return fmt.Errorf("summarize and persist failed: %w", err)
	}
	if !updated {
		return nil
	}

	// Persist only the updated filterKey summary with atomic set-if-newer to avoid late-write override.
	sess.SummariesMu.RLock()
	sum := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()
	summaryBytes, err := json.Marshal(sum)
	if err != nil {
		return fmt.Errorf("marshal summary failed: %w", err)
	}

	var expiresAt *time.Time
	if s.sessionTTL > 0 {
		t := sum.UpdatedAt.Add(s.sessionTTL)
		expiresAt = &t
	}

	// Use UPSERT (INSERT ... ON CONFLICT) for atomic operation
	// This handles both insert and update in a single, race-condition-free operation
	// Note: Last write wins - no timestamp comparison to avoid silent failures
	_, err = s.pgClient.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
		 ON CONFLICT (app_name, user_id, session_id, filter_key) WHERE deleted_at IS NULL
		 DO UPDATE SET
		   summary = EXCLUDED.summary,
		   updated_at = EXCLUDED.updated_at,
		   expires_at = EXCLUDED.expires_at`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, summaryBytes, sum.UpdatedAt, expiresAt)

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
		return errors.New("nil session")
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}

	// If async workers are not initialized, fall back to synchronous processing.
	if len(s.summaryJobChans) == 0 {
		return s.CreateSessionSummary(ctx, sess, filterKey, force)
	}

	// Create summary job.
	job := &summaryJob{
		sessionKey: key,
		filterKey:  filterKey,
		force:      force,
		session:    sess,
	}

	// Try to enqueue the job asynchronously.
	if s.tryEnqueueJob(ctx, job) {
		return nil // Successfully enqueued.
	}

	// If async enqueue failed, fall back to synchronous processing.
	return s.CreateSessionSummary(ctx, sess, filterKey, force)
}

// tryEnqueueJob attempts to enqueue a summary job to the appropriate channel.
// Returns true if successful, false if the job should be processed synchronously.
func (s *Service) tryEnqueueJob(ctx context.Context, job *summaryJob) bool {
	// Select a channel using hash distribution.
	keyStr := fmt.Sprintf("%s:%s:%s", job.sessionKey.AppName, job.sessionKey.UserID, job.sessionKey.SessionID)
	index := int(murmur3.Sum32([]byte(keyStr))) % len(s.summaryJobChans)

	// Use a defer-recover pattern to handle potential panic from sending to closed channel.
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("summary job channel may be closed, falling back to synchronous processing: %v", r)
		}
	}()

	select {
	case s.summaryJobChans[index] <- job:
		return true // Successfully enqueued.
	case <-ctx.Done():
		log.Debugf("summary job channel context cancelled, falling back to synchronous processing, error: %v", ctx.Err())
		return false // Context cancelled.
	default:
		// Queue is full, fall back to synchronous processing.
		log.Warnf("summary job queue is full, falling back to synchronous processing")
		return false
	}
}

func (s *Service) startAsyncSummaryWorker() {
	summaryNum := s.opts.asyncSummaryNum
	// Init summary job chan.
	s.summaryJobChans = make([]chan *summaryJob, summaryNum)
	for i := 0; i < summaryNum; i++ {
		s.summaryJobChans[i] = make(chan *summaryJob, s.opts.summaryQueueSize)
	}

	for _, summaryJobChan := range s.summaryJobChans {
		go func(summaryJobChan chan *summaryJob) {
			for job := range summaryJobChan {
				s.processSummaryJob(job)
				// After branch summary, cascade a full-session summary by
				// reusing the same processing path to keep logic unified.
				if job.filterKey != session.SummaryFilterKeyAllContents {
					job.filterKey = session.SummaryFilterKeyAllContents
					s.processSummaryJob(job)
				}
			}
		}(summaryJobChan)
	}
}

func (s *Service) processSummaryJob(job *summaryJob) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("panic in summary worker: %v", r)
		}
	}()

	// Create a fresh context with timeout for this job.
	ctx := context.Background()
	if s.opts.summaryJobTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.opts.summaryJobTimeout)
		defer cancel()
	}

	if err := s.CreateSessionSummary(ctx, job.session, job.filterKey, job.force); err != nil {
		log.Warnf("summary worker failed to create session summary: %v", err)
	}
}

// GetSessionSummaryText gets the summary text for a session.
func (s *Service) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
) (string, bool) {
	if sess == nil {
		return "", false
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}
	// Prefer local in-memory session summaries when available.
	if len(sess.Summaries) > 0 {
		if text, ok := pickSummaryText(sess.Summaries); ok {
			return text, true
		}
	}

	// Use empty filterKey to get the default summary
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
		AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, session.SummaryFilterKeyAllContents, time.Now())

	if err != nil {
		return "", false
	}

	if summaryText == "" {
		return "", false
	}

	return summaryText, true
}

// pickSummaryText picks a non-empty summary string with preference for the
// all-contents key "" (empty filterKey). No special handling for "root".
func pickSummaryText(summaries map[string]*session.Summary) (string, bool) {
	if summaries == nil {
		return "", false
	}
	// Prefer full-summary stored under empty filterKey.
	if sum, ok := summaries[session.SummaryFilterKeyAllContents]; ok && sum != nil && sum.Summary != "" {
		return sum.Summary, true
	}
	for _, s := range summaries {
		if s != nil && s.Summary != "" {
			return s.Summary, true
		}
	}
	return "", false
}
