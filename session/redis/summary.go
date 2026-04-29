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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

// CreateSessionSummary generates a summary for the session (async-ready).
// It performs per-filterKey delta summarization; when filterKey=="", it means full-session summary.
// Strategy: Summary storage version follows session storage version.
func (s *Service) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	if !isummary.HasSummarizer(s.opts.summarizer) {
		return nil
	}

	if sess == nil {
		return session.ErrNilSession
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	ctx, span := s.startSpan(ctx, "create_session_summary", key)
	defer span.End()

	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}
	if !isummary.NewSummaryDispatchPolicy(
		s.opts.summaryFilterAllowlist,
		s.opts.shouldCascadeFullSessionSummary(),
	).AllowsFilterKey(filterKey) {
		return nil
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

	// Fast path: use version tag from session
	switch ver := getSessionVersion(sess); ver {
	case util.StorageTypeHashIdx:
		s.recordStorageRoute(ctx, opCreateSessionSummary, util.StorageTypeHashIdx)
		return s.hashidxClient.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL)
	case util.StorageTypeZset:
		s.recordStorageRoute(ctx, opCreateSessionSummary, util.StorageTypeZset)
		return s.zsetClient.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL)
	}

	// Slow path: check which storage has the session
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists failed: %v", err)
	}

	if s.compatEnabled() && zsetExists {
		s.recordStorageRoute(ctx, opCreateSessionSummary, util.StorageTypeZset)
		return s.zsetClient.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL)
	}
	if hashidxExists {
		s.recordStorageRoute(ctx, opCreateSessionSummary, util.StorageTypeHashIdx)
		return s.hashidxClient.CreateSummary(ctx, key, filterKey, sum, s.opts.sessionTTL)
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
	ctx, span := s.startSpan(ctx, "get_session_summary_text", key)
	defer span.End()

	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}

	// Try in-memory summaries first.
	if text, ok := isummary.GetSummaryTextFromSession(sess, opts...); ok {
		return text, true
	}

	filterKey := isummary.GetFilterKeyFromOptions(opts...)

	// Fast path: use version tag from session to avoid checkSessionExists round-trip.
	switch ver := getSessionVersion(sess); ver {
	case util.StorageTypeHashIdx:
		s.recordStorageRoute(ctx, opGetSessionSummaryText, util.StorageTypeHashIdx)
		return s.getSummaryFromHashIdx(ctx, key, filterKey, sess.CreatedAt)
	case util.StorageTypeZset:
		s.recordStorageRoute(ctx, opGetSessionSummaryText, util.StorageTypeZset)
		return s.getSummaryFromZSet(ctx, key, filterKey, sess.CreatedAt)
	}

	// Slow path: no version tag, check which storage has the session.
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists failed: %v", err)
		return "", false
	}

	if s.compatEnabled() && zsetExists {
		s.recordStorageRoute(ctx, opGetSessionSummaryText, util.StorageTypeZset)
		return s.getSummaryFromZSet(ctx, key, filterKey, sess.CreatedAt)
	}
	if hashidxExists {
		s.recordStorageRoute(ctx, opGetSessionSummaryText, util.StorageTypeHashIdx)
		return s.getSummaryFromHashIdx(ctx, key, filterKey, sess.CreatedAt)
	}

	return "", false
}

func (s *Service) getSummaryFromHashIdx(ctx context.Context, key session.Key, filterKey string, createdAt time.Time) (string, bool) {
	summaries, err := s.hashidxClient.GetSummary(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "get hashidx summary failed: %v", err)
		return "", false
	}
	if summaries != nil {
		return isummary.PickSummaryText(summaries, filterKey, createdAt)
	}
	return "", false
}

func (s *Service) getSummaryFromZSet(ctx context.Context, key session.Key, filterKey string, createdAt time.Time) (string, bool) {
	summaries, err := s.zsetClient.GetSummary(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "get zset summary failed: %v", err)
		return "", false
	}
	if summaries != nil {
		return isummary.PickSummaryText(summaries, filterKey, createdAt)
	}
	return "", false
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
func (s *Service) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	if !isummary.HasSummarizer(s.opts.summarizer) {
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
	return isummary.CreateSessionSummaryWithCascade(
		ctx,
		sess,
		filterKey,
		force,
		isummary.NewSummaryDispatchPolicy(
			s.opts.summaryFilterAllowlist,
			s.opts.shouldCascadeFullSessionSummary(),
		),
		s.CreateSessionSummary,
	)
}
