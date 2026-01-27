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

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// CreateSessionSummary triggers summarization for the session (not implemented).
func (s *Service) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	// TODO: implement async summary generation
	return fmt.Errorf("CreateSessionSummary not implemented")
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing (not implemented).
func (s *Service) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	// TODO: implement async summary job queue
	return fmt.Errorf("EnqueueSummaryJob not implemented")
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

