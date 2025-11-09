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

	"github.com/spaolacci/murmur3"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isession "trpc.group/trpc-go/trpc-agent-go/session/internal/session"
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
	updated, err := isession.SummarizeSession(ctx, s.opts.summarizer, sess, filterKey, force)
	if err != nil {
		return fmt.Errorf("summarize and persist failed: %w", err)
	}
	if !updated {
		return nil
	}
	// Persist to in-memory store under lock.
	app := s.getOrCreateAppSessions(key.AppName)
	if err := s.writeSummaryUnderLock(app, key, filterKey, sess.Summaries[filterKey].Summary); err != nil {
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
func (s *SessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session) (string, bool) {
	if sess == nil {
		return "", false
	}
	// Prefer structured summaries on session.
	if sess.Summaries != nil {
		// Prefer full-summary under the all-contents filter key.
		if sum, ok := sess.Summaries[session.SummaryFilterKeyAllContents]; ok && sum != nil && sum.Summary != "" {
			return sum.Summary, true
		}
		for _, s := range sess.Summaries {
			if s != nil && s.Summary != "" {
				return s.Summary, true
			}
		}
	}
	return "", false
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
		return s.CreateSessionSummary(ctx, sess, filterKey, force)
	}

	// Do not check storage existence before enqueueing. The worker and
	// write path perform authoritative validation under lock.

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
func (s *SessionService) tryEnqueueJob(ctx context.Context, job *summaryJob) bool {
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

func (s *SessionService) startAsyncSummaryWorker() {
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

func (s *SessionService) processSummaryJob(job *summaryJob) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("panic in summary worker: %v", r)
		}
	}()

	// Do not pre-validate against storage here. We summarize based on the
	// provided job.session and rely on the write path to validate under lock.

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

// stopAsyncSummaryWorker stops all async summary workers and closes their channels.
func (s *SessionService) stopAsyncSummaryWorker() {
	for _, ch := range s.summaryJobChans {
		close(ch)
	}
	s.summaryJobChans = nil
}
