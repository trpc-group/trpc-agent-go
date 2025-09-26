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
	sisession "trpc.group/trpc-go/trpc-agent-go/session/internal/session"
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

	app := s.getOrCreateAppSessions(key.AppName)
	app.mu.Lock()
	userSessions, ok := app.sessions[key.UserID]
	if !ok {
		app.mu.Unlock()
		return fmt.Errorf("user not found: %s", key.UserID)
	}
	swt, ok := userSessions[key.SessionID]
	if !ok {
		app.mu.Unlock()
		return fmt.Errorf("session not found: %s", key.SessionID)
	}
	storedSession := getValidSession(swt)
	if storedSession == nil {
		app.mu.Unlock()
		return fmt.Errorf("session expired: %s", key.SessionID)
	}
	app.mu.Unlock()

	// Run summarization which updates storedSession.Summaries in place.
	updated, err := sisession.SummarizeSession(ctx, s.opts.summarizer, storedSession, filterKey, force)
	if err != nil {
		return fmt.Errorf("summarize and persist failed: %w", err)
	}
	if !updated {
		return nil
	}
	// Persist to in-memory store under lock.
	if err := s.writeSummaryUnderLock(app, key, filterKey, storedSession.Summaries[filterKey].Summary); err != nil {
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
		// Prefer full-summary under empty filterKey.
		if sum, ok := sess.Summaries[""]; ok && sum != nil && sum.Summary != "" {
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

	// Verify session exists in storage before enqueueing.
	app := s.getOrCreateAppSessions(key.AppName)
	app.mu.RLock()
	userSessions, ok := app.sessions[key.UserID]
	if !ok {
		app.mu.RUnlock()
		return fmt.Errorf("user not found: %s", key.UserID)
	}
	swt, ok := userSessions[key.SessionID]
	if !ok {
		app.mu.RUnlock()
		return fmt.Errorf("session not found: %s", key.SessionID)
	}
	storedSession := getValidSession(swt)
	if storedSession == nil {
		app.mu.RUnlock()
		return fmt.Errorf("session expired: %s", key.SessionID)
	}
	app.mu.RUnlock()

	// Create summary job.
	job := &summaryJob{
		sessionKey: key,
		filterKey:  filterKey,
		force:      force,
		session:    sess,
		context:    ctx,
	}

	// Select a channel using hash distribution.
	keyStr := fmt.Sprintf("%s:%s:%s", key.AppName, key.UserID, key.SessionID)
	index := int(murmur3.Sum32([]byte(keyStr))) % len(s.summaryJobChans)

	select {
	case s.summaryJobChans[index] <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Queue is full, fall back to synchronous processing.
		log.Warnf("summary job queue is full, falling back to synchronous processing")
		return s.CreateSessionSummary(ctx, sess, filterKey, force)
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

	// Get the app and session under lock.
	app := s.getOrCreateAppSessions(job.sessionKey.AppName)
	app.mu.Lock()
	userSessions, ok := app.sessions[job.sessionKey.UserID]
	if !ok {
		app.mu.Unlock()
		log.Errorf("user not found: %s", job.sessionKey.UserID)
		return
	}
	swt, ok := userSessions[job.sessionKey.SessionID]
	if !ok {
		app.mu.Unlock()
		log.Errorf("session not found: %s", job.sessionKey.SessionID)
		return
	}
	storedSession := getValidSession(swt)
	if storedSession == nil {
		app.mu.Unlock()
		log.Errorf("session expired: %s", job.sessionKey.SessionID)
		return
	}
	app.mu.Unlock()

	// Perform the actual summary generation.
	updated, err := sisession.SummarizeSession(job.context, s.opts.summarizer, storedSession, job.filterKey, job.force)
	if err != nil {
		log.Errorf("summary worker failed to generate summary: %v", err)
		return
	}
	if !updated {
		return
	}

	// Persist to in-memory store under lock.
	if err := s.writeSummaryUnderLock(app, job.sessionKey, job.filterKey, storedSession.Summaries[job.filterKey].Summary); err != nil {
		log.Errorf("summary worker failed to write summary: %v", err)
	}
}
