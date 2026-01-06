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
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/log"
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
		return errors.New("nil session")
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

	// Note: expires_at is set to NULL - summaries are bound to session lifecycle,
	// they will be deleted when session expires or is deleted.
	now := time.Now()
	// INSERT new version (ReplacingMergeTree will deduplicate based on updated_at)
	err = s.chClient.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, created_at, updated_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, string(summaryBytes), now, summary.UpdatedAt, nil)

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
		return isummary.CreateSessionSummaryWithCascade(ctx, sess, filterKey, force, s.CreateSessionSummary)
	}

	// Create summary job.
	job := &summaryJob{
		ctx:       context.WithoutCancel(ctx),
		filterKey: filterKey,
		force:     force,
		session:   sess,
	}

	// Try to enqueue the job asynchronously.
	if s.tryEnqueueJob(ctx, job) {
		return nil // Successfully enqueued.
	}

	// If async enqueue failed, fall back to synchronous processing.
	return isummary.CreateSessionSummaryWithCascade(ctx, sess, filterKey, force, s.CreateSessionSummary)
}

// tryEnqueueJob attempts to enqueue a summary job to the appropriate channel.
// Returns true if successful, false if the job should be processed synchronously.
// Note: This method assumes channels are already initialized. Callers should check
// len(s.summaryJobChans) > 0 before calling this method.
func (s *Service) tryEnqueueJob(ctx context.Context, job *summaryJob) bool {
	// Select a channel using hash distribution.
	index := job.session.Hash % len(s.summaryJobChans)

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

// GetSessionSummaryText gets the summary text for a session.
// When no options are provided, returns the full-session summary (SummaryFilterKeyAllContents).
// Use session.WithSummaryFilterKey to specify a different filter key.
func (s *Service) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	if sess == nil {
		return "", false
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}

	// Try in-memory session summaries first.
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
		var summaryStr string
		if err := rows.Scan(&summaryStr); err != nil {
			return "", false
		}
		var sum session.Summary
		if err := json.Unmarshal([]byte(summaryStr), &sum); err != nil {
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
			var summaryStr string
			if err := rows2.Scan(&summaryStr); err != nil {
				return "", false
			}
			var sum session.Summary
			if err := json.Unmarshal([]byte(summaryStr), &sum); err != nil {
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

// startAsyncSummaryWorker starts worker goroutines for async summary generation.
func (s *Service) startAsyncSummaryWorker() {
	summaryNum := s.opts.asyncSummaryNum
	// Init summary job chan.
	s.summaryJobChans = make([]chan *summaryJob, summaryNum)
	for i := 0; i < summaryNum; i++ {
		s.summaryJobChans[i] = make(chan *summaryJob, s.opts.summaryQueueSize)
	}

	s.summaryWg.Add(summaryNum)
	for _, summaryJobChan := range s.summaryJobChans {
		go func(summaryJobChan chan *summaryJob) {
			defer s.summaryWg.Done()
			for job := range summaryJobChan {
				s.processSummaryJob(job)
			}
		}(summaryJobChan)
	}
}

func (s *Service) processSummaryJob(job *summaryJob) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(
				context.Background(),
				"panic in summary worker: %v",
				r,
			)
		}
	}()

	// Use the detached context from job which preserves values (e.g., trace ID).
	// Fallback to background context if job.ctx is nil for defensive programming.
	ctx := util.If(job.ctx == nil, context.Background(), job.ctx)
	// Apply timeout if configured.
	if s.opts.summaryJobTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.opts.summaryJobTimeout)
		defer cancel()
	}

	if err := isummary.CreateSessionSummaryWithCascade(ctx, job.session, job.filterKey,
		job.force, s.CreateSessionSummary); err != nil {
		log.WarnfContext(
			ctx,
			"summary worker failed to create session summary: %v",
			err,
		)
	}
}
