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

	var expiresAt *time.Time
	if s.opts.sessionTTL > 0 {
		t := summary.UpdatedAt.Add(s.opts.sessionTTL)
		expiresAt = &t
	}

	_, err = s.mysqlClient.Exec(ctx,
		fmt.Sprintf(`REPLACE INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, filterKey, summaryBytes, summary.UpdatedAt, expiresAt)

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

	// Create summary job with detached context to preserve values (e.g., trace ID)
	// but not inherit cancel/timeout from the original context.
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
	if ctx.Err() != nil {
		return false
	}

	// Select a channel using hash distribution.
	index := job.session.Hash % len(s.summaryJobChans)

	// Use a defer-recover pattern to handle potential panic from sending to closed channel.
	defer func() {
		if r := recover(); r != nil {
			log.WarnfContext(
				ctx,
				"summary job channel may be closed, falling back to "+
					"synchronous processing: %v",
				r,
			)
		}
	}()

	select {
	case s.summaryJobChans[index] <- job:
		return true // Successfully enqueued.
	case <-ctx.Done():
		log.DebugfContext(
			ctx,
			"summary job channel context cancelled, falling back to "+
				"synchronous processing, error: %v",
			ctx.Err(),
		)
		return false // Context cancelled.
	default:
		// Queue is full, fall back to synchronous processing.
		log.WarnfContext(
			ctx,
			"summary job queue is full, falling back to synchronous "+
				"processing",
		)
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
		AND deleted_at IS NULL`, s.tableSessionSummaries),
		key.AppName, key.UserID, key.SessionID, isummary.GetFilterKeyFromOptions(opts...), time.Now())

	if err != nil {
		return "", false
	}

	// If requested filterKey not found, try fallback to full-session summary.
	filterKey := isummary.GetFilterKeyFromOptions(opts...)
	if summaryText == "" && filterKey != session.SummaryFilterKeyAllContents {
		err = s.mysqlClient.Query(ctx, func(rows *sql.Rows) error {
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
			key.AppName, key.UserID, key.SessionID, session.SummaryFilterKeyAllContents, time.Now())
		if err != nil {
			return "", false
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
