//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	v1 "trpc.group/trpc-go/trpc-agent-go/session/redis/internal/v1"
	v2 "trpc.group/trpc-go/trpc-agent-go/session/redis/internal/v2"
)

// CreateSessionSummary generates a summary for the session (async-ready).
// It performs per-filterKey delta summarization; when filterKey=="", it means full-session summary.
// Strategy: Summary storage version follows session storage version.
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

	// Persist to Redis.
	sess.SummariesMu.RLock()
	sum := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()

	if sum == nil {
		return nil
	}

	// Dual-write mode: write to both V2 and V1
	if s.needDualWrite() {
		if err := s.v2Client.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL); err != nil {
			return err
		}
		if err := s.v1Client.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL); err != nil {
			return fmt.Errorf("dual-write summary to V1 failed: %w", err)
		}
		return nil
	}

	// Fast path: use version tag from session
	ver := getSessionVersion(sess)
	if ver == v2.VersionV2 {
		return s.v2Client.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL)
	} else if ver == v1.VersionV1 {
		return s.v1Client.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL)
	}

	// Slow path: check which storage has the session
	v1Exists, v2Exists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists failed: %v", err)
	}

	if v2Exists {
		return s.v2Client.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL)
	}
	if s.legacyEnabled() && v1Exists {
		return s.v1Client.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL)
	}

	log.WarnfContext(ctx, "session not found when creating summary: %s/%s/%s", key.AppName, key.UserID, key.SessionID)
	return nil
}

// GetSessionSummaryText returns the latest summary text from the session state if present.
// When no options are provided, returns the full-session summary (SummaryFilterKeyAllContents).
// Use session.WithSummaryFilterKey to specify a different filter key.
// Strategy: Summary storage version follows session storage version.
func (s *Service) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	// Check session validity.
	if sess == nil {
		return "", false
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}

	// Try in-memory summaries first.
	if text, ok := isummary.GetSummaryTextFromSession(sess, opts...); ok {
		return text, true
	}

	// Check which storage has the session (summary follows session version)
	v1Exists, v2Exists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists failed: %v", err)
		return "", false
	}

	// Priority: V2 > V1 (summary follows session version)
	if v2Exists {
		summaries, err := s.v2Client.GetSummary(ctx, key)
		if err != nil {
			log.WarnfContext(ctx, "get V2 summary failed: %v", err)
			return "", false
		}
		if summaries != nil {
			return isummary.PickSummaryText(summaries, isummary.GetFilterKeyFromOptions(opts...), sess.CreatedAt)
		}
	}

	if s.legacyEnabled() && v1Exists {
		summaries, err := s.v1Client.GetSummary(ctx, key)
		if err != nil {
			log.WarnfContext(ctx, "get V1 summary failed: %v", err)
			return "", false
		}
		if summaries != nil {
			return isummary.PickSummaryText(summaries, isummary.GetFilterKeyFromOptions(opts...), sess.CreatedAt)
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
		return session.ErrNilSession
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
