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
	if sess == nil {
		return errors.New("nil session")
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}

	// Get the authoritative session from storage directly (not via GetSession which clones).
	app, ok := s.getAppSessions(key.AppName)
	if !ok {
		// If app not found, use the input session directly.
		// This handles the case where CreateSessionSummary is called before session is stored.
		updated, err := isummary.CreateSessionSummary(ctx, s.opts.summarizer, sess, filterKey, force)
		if err != nil || !updated {
			return err
		}
		sess.SummariesMu.RLock()
		sum := sess.Summaries[filterKey]
		sess.SummariesMu.RUnlock()
		if sum == nil {
			return nil
		}
		app = s.getOrCreateAppSessions(sess.AppName)
		return s.writeSummaryUnderLock(app, key, filterKey, sum.Summary)
	}

	app.mu.RLock()
	var stored *session.Session
	if userSessions, ok := app.sessions[key.UserID]; ok {
		if sessWithTTL, ok := userSessions[key.SessionID]; ok {
			stored = getValidSession(sessWithTTL)
		}
	}
	app.mu.RUnlock()

	var workSession *session.Session
	if stored == nil {
		// If session not found in storage, use the input session directly.
		// This handles the case where CreateSessionSummary is called before session is stored.
		workSession = sess
	} else {
		// Clone the stored session to avoid modifying the original.
		// Clone() already copies summaries, so prevAt will be correct.
		workSession = stored.Clone()
	}

	updated, err := isummary.CreateSessionSummary(ctx, s.opts.summarizer, workSession, filterKey, force)
	if err != nil || !updated {
		return err
	}

	// Persist to in-memory store under lock.
	workSession.SummariesMu.RLock()
	sum := workSession.Summaries[filterKey]
	workSession.SummariesMu.RUnlock()

	if sum == nil {
		return nil
	}

	return s.writeSummaryUnderLock(app, key, filterKey, sum.Summary)
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

	if s.asyncWorker != nil {
		return s.asyncWorker.EnqueueJob(ctx, sess, filterKey, force)
	}

	// Fallback to synchronous processing.
	return isummary.CreateSessionSummaryWithCascade(ctx, sess, filterKey, force, s.CreateSessionSummary)
}
