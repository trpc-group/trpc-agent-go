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

	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

// CreateSessionSummary generates a summary for the session and stores it on the session object.
// This implementation preserves original events and updates session.Summaries only.
func (s *SessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	updated, err := isummary.CreateSessionSummary(ctx, s.opts.summarizer, sess, filterKey, force)
	if err != nil || !updated {
		return err
	}

	// Persist to in-memory store under lock.
	sess.SummariesMu.RLock()
	sum := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()

	app := s.getOrCreateAppSessions(sess.AppName)
	if err := s.writeSummaryUnderLock(
		app, session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}, filterKey, sum.Summary,
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
	// inmemory only needs in-memory summaries.
	return isummary.GetSessionSummaryText(ctx, sess, opts...)
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

	if s.asyncWorker == nil {
		if s.opts.asyncSummaryNum > 0 {
			s.asyncWorker = isummary.NewAsyncSummaryWorker(isummary.AsyncSummaryConfig{
				Summarizer:        s.opts.summarizer,
				AsyncSummaryNum:   s.opts.asyncSummaryNum,
				SummaryQueueSize:  s.opts.summaryQueueSize,
				SummaryJobTimeout: s.opts.summaryJobTimeout,
				CreateSummaryFunc: s.CreateSessionSummary,
			})
			s.asyncWorker.Start()
		}
	}

	if s.asyncWorker != nil {
		return s.asyncWorker.EnqueueJob(ctx, sess, filterKey, force)
	}

	// Fallback to synchronous processing.
	return isummary.CreateSessionSummaryWithCascade(ctx, sess, filterKey, force, s.CreateSessionSummary)
}
