//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

// CreateSessionSummary generates a summary for the session and persists it.
//
// When the configured summarizer is empty the call is a no-op. Persistence
// uses an upsert keyed by (app_name, user_id, session_id, filter_key) so the
// operation is race-free with itself.
func (s *Service) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
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

	sess.SummariesMu.RLock()
	sum := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()
	if sum == nil {
		return nil
	}

	summaryBytes, err := json.Marshal(sum)
	if err != nil {
		return fmt.Errorf("marshal summary failed: %w", err)
	}

	now := time.Now()
	filter := activeFilterNoExpiry(bson.M{
		"app_name":   sess.AppName,
		"user_id":    sess.UserID,
		"session_id": sess.ID,
		"filter_key": filterKey,
	})
	update := bson.M{
		"$set": bson.M{
			"summary":    summaryBytes,
			"updated_at": sum.UpdatedAt,
		},
		"$setOnInsert": bson.M{
			"app_name":   sess.AppName,
			"user_id":    sess.UserID,
			"session_id": sess.ID,
			"filter_key": filterKey,
			"created_at": now,
		},
	}
	if _, err := s.client.UpdateOne(ctx, s.database, s.collSessionSummaries, filter, update,
		options.Update().SetUpsert(true)); err != nil {
		return fmt.Errorf("upsert summary failed: %w", err)
	}
	return nil
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
//
// Async workers are wired in a follow-up PR; for now we fall back to
// synchronous CreateSessionSummary, applying the same dispatch policy that
// the async worker would.
func (s *Service) EnqueueSummaryJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
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
	return isummary.CreateSessionSummaryWithCascade(
		isummary.DetachContext(ctx),
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

// GetSessionSummaryText returns the latest summary text for the session.
//
// Lookup order: in-memory session.Summaries first, then the persisted
// summary for the requested filter key, then the full-session summary as
// fallback when a non-empty filter key was requested.
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

	if text, ok := isummary.GetSummaryTextFromSession(sess, opts...); ok {
		return text, true
	}

	filterKey := isummary.GetFilterKeyFromOptions(opts...)
	if text, ok := s.loadSummaryText(ctx, key, filterKey, sess.CreatedAt); ok {
		return text, true
	}
	if filterKey != session.SummaryFilterKeyAllContents {
		return s.loadSummaryText(ctx, key, session.SummaryFilterKeyAllContents, sess.CreatedAt)
	}
	return "", false
}

// loadSummaryText fetches a single persisted summary text for a (key, filterKey)
// pair, applying the same expiry / softDelete / staleness filters used by the
// postgres backend.
func (s *Service) loadSummaryText(
	ctx context.Context,
	key session.Key,
	filterKey string,
	sessionCreatedAt time.Time,
) (string, bool) {
	filter := activeFilter(time.Now(), bson.M{
		"app_name":   key.AppName,
		"user_id":    key.UserID,
		"session_id": key.SessionID,
		"filter_key": filterKey,
		"updated_at": bson.M{"$gte": sessionCreatedAt},
	})
	var doc sessionSummaryDoc
	err := s.client.FindOne(ctx, s.database, s.collSessionSummaries, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) || err != nil {
		return "", false
	}
	var sum session.Summary
	if err := json.Unmarshal(doc.Summary, &sum); err != nil {
		return "", false
	}
	if sum.Summary == "" {
		return "", false
	}
	return sum.Summary, true
}
