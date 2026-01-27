//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redisv2

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

// CreateSessionSummary triggers synchronous summarization for the session.
func (s *Service) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	if s.opts.summarizer == nil {
		return nil
	}

	if sess == nil {
		return session.ErrNilSession
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}

	updated, err := isummary.SummarizeSession(ctx, s.opts.summarizer, sess, filterKey, force)
	if err != nil || !updated {
		return err
	}

	return s.persistSummary(ctx, sess, filterKey)
}

// persistSummary saves the summary to Redis.
func (s *Service) persistSummary(ctx context.Context, sess *session.Session, filterKey string) error {
	summ, ok := isummary.GetSummaryTextFromSession(sess, session.WithSummaryFilterKey(filterKey))
	if !ok || summ == "" {
		return nil
	}

	summary := session.Summary{
		Summary:   summ,
		UpdatedAt: time.Now(),
	}
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	sKey := summaryKey(key)

	if err := s.redisClient.HSet(ctx, sKey, filterKey, summaryJSON).Err(); err != nil {
		return fmt.Errorf("save summary: %w", err)
	}

	if s.opts.sessionTTL > 0 {
		s.redisClient.Expire(ctx, sKey, s.opts.sessionTTL)
	}

	return nil
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
func (s *Service) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	if s.asyncWorker == nil {
		return fmt.Errorf("async summary worker not configured")
	}
	return s.asyncWorker.EnqueueJob(ctx, sess, filterKey, force)
}

// GetSessionSummaryText returns the latest summary text for the session.
func (s *Service) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	if sess == nil {
		return "", false
	}

	var summaryOpts session.SummaryOptions
	for _, opt := range opts {
		opt(&summaryOpts)
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	sKey := summaryKey(key)

	result, err := s.redisClient.HGet(ctx, sKey, summaryOpts.FilterKey).Result()
	if err != nil || result == "" {
		return "", false
	}

	var summary session.Summary
	if err := json.Unmarshal([]byte(result), &summary); err != nil {
		return "", false
	}
	return summary.Summary, true
}
