//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spaolacci/murmur3"
	"gorm.io/gorm"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isession "trpc.group/trpc-go/trpc-agent-go/session/internal/session"
)

// CreateSessionSummary generates a summary for the session (async-ready).
// It performs per-filterKey delta summarization; when filterKey=="", it means full-session summary.
func (s *Service) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
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

	payload, err := json.Marshal(sum)
	if err != nil {
		return fmt.Errorf("marshal summary failed: %w", err)
	}

	// Store summary with atomic set-if-newer logic
	now := time.Now()
	var expiresAt time.Time
	if s.opts.sessionTTL > 0 {
		expiresAt = now.Add(s.opts.sessionTTL)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Check if summary exists
		var existingSummary sessionSummaryModel
		err := tx.Where("app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?",
			key.AppName, key.UserID, key.SessionID, filterKey).
			First(&existingSummary).Error

		if err == gorm.ErrRecordNotFound {
			// Create new summary
			summaryModel := &sessionSummaryModel{
				AppName:   key.AppName,
				UserID:    key.UserID,
				SessionID: key.SessionID,
				FilterKey: filterKey,
				Summary:   payload,
				UpdatedAt: sum.UpdatedAt,
				ExpiresAt: expiresAt,
			}
			return tx.Create(summaryModel).Error
		}

		if err != nil {
			return fmt.Errorf("query existing summary failed: %w", err)
		}

		// Compare timestamps to decide whether to update
		if !existingSummary.UpdatedAt.Before(sum.UpdatedAt) {
			// Existing summary is newer or equal, skip update
			return nil
		}

		// Update existing summary
		return tx.Model(&existingSummary).Updates(map[string]interface{}{
			"summary":    payload,
			"updated_at": sum.UpdatedAt,
			"expires_at": expiresAt,
		}).Error
	})
}

// GetSessionSummaryText returns the latest summary text from the session state if present.
func (s *Service) GetSessionSummaryText(ctx context.Context, sess *session.Session) (string, bool) {
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

	// Query from database
	now := time.Now()
	var summaryModels []sessionSummaryModel
	if err := s.db.WithContext(ctx).
		Where("app_name = ? AND user_id = ? AND session_id = ? AND (expires_at IS NULL OR expires_at > ?)",
			key.AppName, key.UserID, key.SessionID, now).
		Find(&summaryModels).Error; err == nil && len(summaryModels) > 0 {
		summaries := make(map[string]*session.Summary)
		for _, sm := range summaryModels {
			var summary session.Summary
			if err := json.Unmarshal(sm.Summary, &summary); err == nil {
				summaries[sm.FilterKey] = &summary
			}
		}
		if len(summaries) > 0 {
			return pickSummaryText(summaries)
		}
	}

	return "", false
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

	// Perform the actual summary generation for the requested filterKey.
	updated, err := isession.SummarizeSession(ctx, s.opts.summarizer, job.session, job.filterKey, job.force)
	if err != nil {
		log.Errorf("summary worker failed to generate summary: %v", err)
		return
	}
	if !updated {
		return
	}

	// Persist to database.
	job.session.SummariesMu.RLock()
	sum := job.session.Summaries[job.filterKey]
	job.session.SummariesMu.RUnlock()

	payload, err := json.Marshal(sum)
	if err != nil {
		log.Errorf("summary worker failed to marshal summary: %v", err)
		return
	}

	now := time.Now()
	var expiresAt time.Time
	if s.opts.sessionTTL > 0 {
		expiresAt = now.Add(s.opts.sessionTTL)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Check if summary exists
		var existingSummary sessionSummaryModel
		err := tx.Where("app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?",
			job.sessionKey.AppName, job.sessionKey.UserID, job.sessionKey.SessionID, job.filterKey).
			First(&existingSummary).Error

		if err == gorm.ErrRecordNotFound {
			// Create new summary
			summaryModel := &sessionSummaryModel{
				AppName:   job.sessionKey.AppName,
				UserID:    job.sessionKey.UserID,
				SessionID: job.sessionKey.SessionID,
				FilterKey: job.filterKey,
				Summary:   payload,
				UpdatedAt: sum.UpdatedAt,
				ExpiresAt: expiresAt,
			}
			return tx.Create(summaryModel).Error
		}

		if err != nil {
			return fmt.Errorf("query existing summary failed: %w", err)
		}

		// Compare timestamps to decide whether to update
		if !existingSummary.UpdatedAt.Before(sum.UpdatedAt) {
			// Existing summary is newer or equal, skip update
			return nil
		}

		// Update existing summary
		return tx.Model(&existingSummary).Updates(map[string]interface{}{
			"summary":    payload,
			"updated_at": sum.UpdatedAt,
			"expires_at": expiresAt,
		}).Error
	})

	if err != nil {
		log.Errorf("summary worker failed to store summary: %v", err)
	}
}
