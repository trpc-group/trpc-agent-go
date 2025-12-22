//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

// CreateSessionSummary generates a summary for the session and stores it on the session object.
// This implementation preserves original events and updates session.Summaries only.
func (s *SessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
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

	// Run summarization based on the provided session. Persistence path will
	// validate app/session existence under lock.
	updated, err := isummary.SummarizeSession(ctx, s.opts.summarizer, sess, filterKey, force)
	if err != nil {
		return fmt.Errorf("summarize and persist failed: %w", err)
	}
	if !updated {
		return nil
	}

	// Persist to in-memory store under lock.
	sess.SummariesMu.RLock()
	sum := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()

	app := s.getOrCreateAppSessions(key.AppName)
	if err := s.writeSummaryUnderLock(
		app, key, filterKey, sum.Summary,
	); err != nil {
		return fmt.Errorf("write summary under lock failed: %w", err)
	}
	return nil
}

// writeSummaryUnderLock writes a summary for a filterKey under app lock and refreshes TTL.
// When filterKey is "", it represents the full-session summary.
func (s *SessionService) writeSummaryUnderLock(app *appSessions, key session.Key, filterKey string, text string) error {
	app.mu.Lock()
	defer app.mu.Unlock()
	swt, ok := app.sessions[key.UserID][key.SessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", key.SessionID)
	}
	cur := getValidSession(swt)
	if cur == nil {
		return fmt.Errorf("session expired: %s", key.SessionID)
	}
	// Acquire write lock to protect Summaries access.
	cur.SummariesMu.Lock()
	defer cur.SummariesMu.Unlock()

	if cur.Summaries == nil {
		cur.Summaries = make(map[string]*session.Summary)
	}
	cur.Summaries[filterKey] = &session.Summary{Summary: text, UpdatedAt: time.Now().UTC()}
	cur.UpdatedAt = time.Now()
	swt.session = cur
	swt.expiredAt = calculateExpiredAt(s.opts.sessionTTL)
	return nil
}

// GetSessionSummaryText returns previously stored summary from session summaries if present.
// When no options are provided, returns the full-session summary (SummaryFilterKeyAllContents).
// Use session.WithSummaryFilterKey to specify a different filter key.
func (s *SessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	return isummary.GetSummaryTextFromSession(sess, opts...)
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
func (s *SessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
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

	// Do not check storage existence before enqueueing. The worker and
	// write path perform authoritative validation under lock.

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
func (s *SessionService) tryEnqueueJob(ctx context.Context, job *summaryJob) bool {
	// Select a channel using hash distribution.
	index := job.session.Hash % len(s.summaryJobChans)

	// If context already cancelled, do not enqueue.
	if err := ctx.Err(); err != nil {
		log.DebugfContext(
			ctx,
			"summary job context cancelled before enqueue: %v",
			err,
		)
		return false
	}

	// Use a defer-recover pattern to handle potential panic from
	// sending to a closed channel.
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

	// Non-blocking enqueue to avoid waiting when the queue is full.
	select {
	case s.summaryJobChans[index] <- job:
		return true // Successfully enqueued.
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

func (s *SessionService) startAsyncSummaryWorker() {
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

func (s *SessionService) processSummaryJob(job *summaryJob) {
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

// stopAsyncSummaryWorker stops all async summary workers and closes their channels.
func (s *SessionService) stopAsyncSummaryWorker() {
	if len(s.summaryJobChans) == 0 {
		return
	}
	for _, ch := range s.summaryJobChans {
		close(ch)
	}
	s.summaryWg.Wait()
	s.summaryJobChans = nil
}
