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
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/spaolacci/murmur3"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isession "trpc.group/trpc-go/trpc-agent-go/session/internal/session"
)

// luaSummariesSetIfNewer atomically merges one filterKey summary into the stored
// JSON map only if the incoming UpdatedAt is newer-or-equal.
// KEYS[1] = sesssum:{app}:{user}
// ARGV[1] = sessionID
// ARGV[2] = filterKey
// ARGV[3] = newSummaryJSON -> {"Summary":"...","UpdatedAt":"RFC3339 time"}
var luaSummariesSetIfNewer = redis.NewScript(
	"local cur = redis.call('HGET', KEYS[1], ARGV[1])\n" +
		"local fk = ARGV[2]\n" +
		"local newSum = cjson.decode(ARGV[3])\n" +
		"if not cur or cur == '' then\n" +
		"  local m = {}\n" +
		"  m[fk] = newSum\n" +
		"  redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(m))\n" +
		"  return 1\n" +
		"end\n" +
		"local map = cjson.decode(cur)\n" +
		"local old = map[fk]\n" +
		"local old_ts = nil\n" +
		"local new_ts = nil\n" +
		"if old and old['updated_at'] then old_ts = old['updated_at'] end\n" +
		"if newSum and newSum['updated_at'] then new_ts = newSum['updated_at'] end\n" +
		"if not old or (old_ts and new_ts and old_ts <= new_ts) then\n" +
		"  map[fk] = newSum\n" +
		"  redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(map))\n" +
		"  return 1\n" +
		"end\n" +
		"return 0\n",
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
	sumKey := getSessionSummaryKey(key)
	if _, err := luaSummariesSetIfNewer.Run(
		ctx, s.redisClient, []string{sumKey}, key.SessionID, filterKey, string(payload),
	).Result(); err != nil {
		return fmt.Errorf("store summaries (lua) failed: %w", err)
	}
	if s.sessionTTL > 0 {
		if err := s.redisClient.Expire(ctx, sumKey, s.sessionTTL).Err(); err != nil {
			return fmt.Errorf("expire summaries failed: %w", err)
		}
	}
	return nil
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
	// Prefer separate summaries hash.
	if bytes, err := s.redisClient.HGet(ctx, getSessionSummaryKey(key), key.SessionID).Bytes(); err == nil && len(bytes) > 0 {
		var summaries map[string]*session.Summary
		if err := json.Unmarshal(bytes, &summaries); err == nil && len(summaries) > 0 {
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

	// Persist to Redis.
	job.session.SummariesMu.RLock()
	sum := job.session.Summaries[job.filterKey]
	job.session.SummariesMu.RUnlock()
	payload, err := json.Marshal(sum)
	if err != nil {
		log.Errorf("summary worker failed to marshal summary: %v", err)
		return
	}
	sumKey := getSessionSummaryKey(job.sessionKey)
	if _, err := luaSummariesSetIfNewer.Run(
		ctx, s.redisClient, []string{sumKey}, job.sessionKey.SessionID, job.filterKey, string(payload),
	).Result(); err != nil {
		log.Errorf("summary worker failed to store summary: %v", err)
		return
	}
	if s.sessionTTL > 0 {
		if err := s.redisClient.Expire(ctx, sumKey, s.sessionTTL).Err(); err != nil {
			log.Errorf("summary worker failed to set summary TTL: %v", err)
		}
	}
}
