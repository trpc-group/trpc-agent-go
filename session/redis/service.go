//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis provides the redis session service.
package redis

import (
	"context"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	v1 "trpc.group/trpc-go/trpc-agent-go/session/redis/internal/v1"
	v2 "trpc.group/trpc-go/trpc-agent-go/session/redis/internal/v2"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
)

var (
	_ session.Service      = (*Service)(nil)
	_ session.TrackService = (*Service)(nil)
)

// Service is the redis session service.
// It acts as a facade, routing requests to V2 (default) or V1 (legacy) implementations.
type Service struct {
	opts            ServiceOpts
	redisClient     redis.UniversalClient
	eventPairChans  []chan *sessionEventPair     // channel for session events to persistence (V1 & V2 shared or separate?) -> V2 has its own logic, but we can share if structure matches.
	trackEventChans []chan *trackEventPair       // channel for track events to persistence (V1).
	asyncWorker     *isummary.AsyncSummaryWorker // async summary worker
	persistWg       sync.WaitGroup               // wait group for persist workers
	once            sync.Once                    // ensure Close is called only once

	v1Client *v1.Client
	v2Client *v2.Client
}

type sessionEventPair struct {
	key     session.Key
	event   *event.Event
	version string
}

type trackEventPair struct {
	key     session.Key
	event   *session.TrackEvent
	version string
}

// legacyEnabled returns true if V1 legacy support is enabled (read fallback).
// When enabled:
//   - GetSession: V2 first, fallback to V1 if not found
//   - ListSessions: merge V2 and V1 results
//   - CreateSession: V2 only (V1 created via dual-write if needDualWrite)
//   - AppendEvent: route by session version tag, or dual-write if needDualWrite
func (s *Service) legacyEnabled() bool {
	return s.opts.compatMode >= CompatModeLegacy
}

// needDualWrite returns true if dual-write mode is enabled.
// Dual-write ensures backward compatibility during rolling upgrades:
//
// Session Meta:
//   - V2 creates session: writes to both V2 and V1 (V1 nodes can read)
//   - V1 creates session: only in V1 (V2 nodes read via fallback, no V2 meta created)
//
// Event Data:
//   - Always writes to both V2 and V1 (regardless of which version created the session)
//
// This asymmetry is intentional:
//   - V2 meta count may be less than V1 (only V2-created sessions have V2 meta)
//   - Event data is always complete in both storages
//   - V2 nodes can read V1 sessions via fallback, no need to create V2 meta copy
func (s *Service) needDualWrite() bool {
	return s.opts.compatMode == CompatModeDualWrite
}

// NewService creates a new redis session service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderURL(opts.url),
		storage.WithExtraOptions(opts.extraOptions...),
	}
	// if instance name set, and url not set, use instance name to create redis client
	if opts.url == "" && opts.instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetRedisInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("redis instance %s not found", opts.instanceName)
		}
	}

	redisClient, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create redis client failed: %w", err)
	}

	// Initialize V1 config
	v1Cfg := v1.Config{
		SessionTTL:        opts.sessionTTL,
		AppStateTTL:       opts.appStateTTL,
		UserStateTTL:      opts.userStateTTL,
		SessionEventLimit: opts.sessionEventLimit,
		KeyPrefix:         opts.keyPrefix,
	}

	// Initialize V2 config
	v2Cfg := v2.Config{
		SessionTTL:        opts.sessionTTL,
		AppStateTTL:       opts.appStateTTL,
		UserStateTTL:      opts.userStateTTL,
		SessionEventLimit: opts.sessionEventLimit,
		KeyPrefix:         opts.keyPrefix,
	}

	s := &Service{
		opts:        opts,
		redisClient: redisClient,
		v1Client:    v1.NewClient(redisClient, v1Cfg),
		v2Client:    v2.NewClient(redisClient, v2Cfg),
	}

	// Initialize Async Persistence
	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}

	// Start async summary workers if summarizer is configured
	if opts.summarizer != nil && opts.asyncSummaryNum > 0 {
		s.asyncWorker = isummary.NewAsyncSummaryWorker(isummary.AsyncSummaryConfig{
			Summarizer:        opts.summarizer,
			AsyncSummaryNum:   opts.asyncSummaryNum,
			SummaryQueueSize:  opts.summaryQueueSize,
			SummaryJobTimeout: opts.summaryJobTimeout,
			CreateSummaryFunc: s.CreateSessionSummary,
		})
		s.asyncWorker.Start()
	}

	return s, nil
}

// checkSessionExists checks if session exists in V1 and V2 using pipeline.
// Returns (v1Exists, v2Exists, error).
// If both exist, logs an error for data inconsistency investigation.
// TODO: Remove this defensive check after the system is stable.
func (s *Service) checkSessionExists(ctx context.Context, key session.Key) (v1Exists, v2Exists bool, err error) {
	pipe := s.redisClient.Pipeline()

	// Add V2 check to pipeline
	v2Cmd := s.v2Client.ExistsPipelined(ctx, pipe, key)

	// Add V1 check to pipeline if legacy support is enabled
	var v1Cmd *redis.BoolCmd
	if s.legacyEnabled() {
		v1Cmd = s.v1Client.ExistsPipelined(ctx, pipe, key)
	}

	// Execute pipeline (go-redis handles multi-slot routing in cluster mode)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return false, false, fmt.Errorf("check session exists pipeline: %w", err)
	}

	// Extract results
	if v2Result, err := v2Cmd.Result(); err != nil && err != redis.Nil {
		return false, false, fmt.Errorf("check v2 exists: %w", err)
	} else {
		v2Exists = v2Result > 0
	}

	if v1Cmd != nil {
		if v1Result, err := v1Cmd.Result(); err != nil && err != redis.Nil {
			return false, false, fmt.Errorf("check v1 exists: %w", err)
		} else {
			v1Exists = v1Result
		}
	}

	// Log info if both exist (expected during dual-write mode)
	if v1Exists && v2Exists {
		// log.InfofContext(ctx, "session exists in both V1 and V2: %s/%s/%s",
		// key.AppName, key.UserID, key.SessionID)
	}

	return v1Exists, v2Exists, nil
}

// CreateSession creates a new session.
// Strategy:
//   - If V2 exists: return existing session
//   - If only V1 exists (legacy data): in dual-write mode, create V2 meta then return
//   - Otherwise: create new session in V2 (and V1 if dual-write enabled)
func (s *Service) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}

	// Check if session already exists
	if key.SessionID != "" {
		v1Exists, v2Exists, err := s.checkSessionExists(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("check session exists: %w", err)
		}

		// Case 1: V2 exists - return existing session
		if v2Exists {
			sess, err := s.getSessionInternal(ctx, key, applyOptions(opts...), v1Exists, v2Exists)
			if err != nil {
				return nil, fmt.Errorf("get existing session: %w", err)
			}
			if sess != nil {
				return sess, nil
			}
		}

		// Case 2: Only V1 exists (legacy data) - need to create V2 meta in dual-write mode
		// This ensures V2 can work independently after migration completes.
		if v1Exists && !v2Exists && s.needDualWrite() {
			// Get session from V1
			sess, err := s.getSessionInternal(ctx, key, applyOptions(opts...), v1Exists, false)
			if err != nil {
				return nil, fmt.Errorf("get V1 session: %w", err)
			}
			if sess != nil {
				// Create V2 meta for this V1 session (events will be dual-written separately)
				if _, err := s.v2Client.CreateSession(ctx, key, state); err != nil {
					return nil, fmt.Errorf("create V2 meta for V1 session: %w", err)
				}
				return sess, nil
			}
		}

		// Case 3: V1 exists but not in dual-write mode - just return V1 session
		if v1Exists {
			sess, err := s.getSessionInternal(ctx, key, applyOptions(opts...), v1Exists, false)
			if err != nil {
				return nil, fmt.Errorf("get existing session: %w", err)
			}
			if sess != nil {
				return sess, nil
			}
		}
	}

	// Create new session in V2 (primary storage)
	sess, err := s.v2Client.CreateSession(ctx, key, state)
	if err != nil {
		return nil, err
	}

	// Dual-write to V1 for backward compatibility during rolling upgrades.
	// This allows older V1-only instances to still access new sessions.
	if s.needDualWrite() {
		if _, err := s.v1Client.CreateSession(ctx, key, state); err != nil {
			return nil, fmt.Errorf("dual-write session to V1 failed: %w", err)
		}
	}

	return sess, nil
}

// GetSession gets a session.
// Strategy: V2 First -> Legacy Fallback.
func (s *Service) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)

	hctx := &session.GetSessionContext{
		Context: ctx,
		Key:     key,
		Options: opt,
	}
	final := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		// TODO: Remove this defensive check after the system is stable
		v1Exists, v2Exists, err := s.checkSessionExists(c.Context, c.Key)
		if err != nil {
			log.WarnfContext(c.Context, "checkSessionExists failed: %v", err)
		}
		return s.getSessionInternal(c.Context, c.Key, c.Options, v1Exists, v2Exists)
	}
	sess, err := hook.RunGetSessionHooks(s.opts.getSessionHooks, hctx, final)
	if err != nil {
		return nil, fmt.Errorf("redis session service get session state failed: %w", err)
	}
	return sess, nil
}

// getSessionInternal retrieves session with V2-first fallback to V1.
// v1Exists/v2Exists indicate whether session exists in each storage version.
// Caller should call checkSessionExists first and pass the results.
func (s *Service) getSessionInternal(
	ctx context.Context,
	key session.Key,
	opts *session.Options,
	v1Exists, v2Exists bool,
) (*session.Session, error) {
	// 1. Try V2 (priority)
	if v2Exists {
		sess, err := s.v2Client.GetSession(ctx, key, opts.EventNum)
		if err != nil {
			return nil, err
		}
		return sess, nil
	}

	// 2. Legacy Fallback
	if s.legacyEnabled() && v1Exists {
		sess, err := s.v1Client.GetSession(ctx, key, opts.EventNum, opts.EventTime)
		if err != nil {
			return nil, err
		}
		if sess != nil {
			return sess, nil
		}
	}
	return nil, nil // Not found
}

// ListSessions lists all sessions by user scope of session key.
// Strategy: List V2 (Scan) + List V1 (HGetAll) -> Merge.
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)

	// 1. List V2
	sessions, err := s.v2Client.ListSessions(ctx, userKey, opt.EventNum)
	if err != nil {
		return nil, fmt.Errorf("scan sessions (v2): %w", err)
	}

	// 2. List V1 (if legacy enabled)
	if s.legacyEnabled() {
		sessListV1, err := s.v1Client.ListSessions(ctx, userKey, opt.EventNum, opt.EventTime)
		if err != nil {
			return nil, fmt.Errorf("list sessions (v1): %w", err)
		}

		// Merge: V2 overrides V1 (skip duplicates)
		v2Map := make(map[string]bool)
		for _, sess := range sessions {
			v2Map[sess.ID] = true
		}
		for _, s1 := range sessListV1 {
			if v2Map[s1.ID] {
				// Skip V1 session if it already exists in V2 (expected in dual-write mode)
				log.InfofContext(ctx, "session exists in both V1 and V2 (dual-write): %s/%s/%s",
					userKey.AppName, userKey.UserID, s1.ID)
			} else {
				sessions = append(sessions, s1)
			}
		}
	}

	return sessions, nil
}

// DeleteSession deletes a session.
func (s *Service) DeleteSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	// Delete V2
	errV2 := s.v2Client.DeleteSession(ctx, key)

	// Delete V1 (if legacy enabled)
	if s.legacyEnabled() {
		errV1 := s.v1Client.DeleteSession(ctx, key)
		if errV2 != nil {
			return errV2
		}
		return errV1
	}

	return errV2
}

// AppendEvent appends an event to a session.
func (s *Service) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	event *event.Event,
	opts ...session.Option,
) error {
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	hctx := &session.AppendEventContext{
		Context: ctx,
		Session: sess,
		Event:   event,
		Key:     key,
	}
	final := func(c *session.AppendEventContext, next func() error) error {
		return s.appendEventInternal(c.Context, c.Session, c.Event, c.Key, opts...)
	}
	return hook.RunAppendEventHooks(s.opts.appendEventHooks, hctx, final)
}

// appendEventInternal is the internal implementation of AppendEvent.
func (s *Service) appendEventInternal(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	key session.Key,
	opts ...session.Option,
) error {
	// update user session with the given event
	sess.UpdateUserSession(e, opts...)

	// persist event to redis asynchronously
	if s.opts.enableAsyncPersist {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok &&
					err.Error() == "send on closed channel" {
					log.ErrorfContext(
						ctx,
						"redis session service append event failed: %v",
						r,
					)
					return
				}
				panic(r)
			}
		}()

		ver := getSessionVersion(sess)

		index := sess.Hash % len(s.eventPairChans)
		select {
		case s.eventPairChans[index] <- &sessionEventPair{key: key, event: e, version: ver}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	// Sync Persist
	return s.persistEvent(ctx, getSessionVersion(sess), e, key)
}

// getSessionVersion returns the version tag from session's ServiceMeta.
// Returns empty string if not set.
func getSessionVersion(sess *session.Session) string {
	if sess == nil || sess.ServiceMeta == nil {
		return ""
	}
	return sess.ServiceMeta[v2.ServiceMetaVersionKey]
}

func (s *Service) persistEvent(ctx context.Context, ver string, e *event.Event, key session.Key) error {
	// Dual-write mode: append to both V2 and V1
	if s.needDualWrite() {
		return s.appendEventV2WithDualWrite(ctx, key, e)
	}

	// fast path: use version tag
	if ver == v2.VersionV2 {
		return s.v2Client.AppendEvent(ctx, key, e)
	} else if ver == v1.VersionV1 {
		return s.v1Client.AppendEvent(ctx, key, e)
	}

	// Slow path: no version tag, check storage
	v1Exists, v2Exists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists in persistEvent failed: %v", err)
	}

	if v2Exists {
		return s.v2Client.AppendEvent(ctx, key, e)
	}
	if s.legacyEnabled() && v1Exists {
		return s.v1Client.AppendEvent(ctx, key, e)
	}

	return fmt.Errorf("session not found: %s/%s/%s", key.AppName, key.UserID, key.SessionID)
}

// appendEventV2WithDualWrite appends event to V2 and optionally to V1 for backward compatibility.
// Note: V1 write may fail if the session was created by V2 only (V1 session doesn't exist).
// In this case, we log a warning but don't fail the operation since V2 is the primary storage.
func (s *Service) appendEventV2WithDualWrite(ctx context.Context, key session.Key, e *event.Event) error {
	if err := s.v2Client.AppendEvent(ctx, key, e); err != nil {
		return err
	}

	// Try to dual-write to V1. If V1 session doesn't exist (e.g., session created by V2 only),
	// we skip V1 write since V2 is the primary storage and V1 is only for backward compatibility.
	if err := s.v1Client.AppendEvent(ctx, key, e); err != nil {
		// Check if the error is due to V1 session not existing
		v1Exists, checkErr := s.v1Client.Exists(ctx, key)
		if checkErr == nil && !v1Exists {
			// V1 session doesn't exist, this is expected for V2-created sessions
			// Skip V1 write silently
			return nil
		}
		// V1 exists but write failed, or check failed - log warning but don't fail
		log.WarnfContext(ctx, "dual-write event to V1 failed (non-fatal): %v", err)
	}
	return nil
}

// trimEventOptions defines trimming behavior.
type trimEventOptions struct {
	// ConversationCount is the number of recent conversations to trim.
	// A conversation is defined as all events sharing the same RequestID.
	ConversationCount int
}

// TrimConversationOption customizes trimming.
type TrimConversationOption func(*trimEventOptions)

// WithCount sets the number of conversations to trim.
// Each conversation is a group of events with the same RequestID.
func WithCount(n int) TrimConversationOption {
	return func(o *trimEventOptions) {
		o.ConversationCount = n
	}
}

// TrimConversations trims recent conversations and returns the deleted events.
func (s *Service) TrimConversations(
	ctx context.Context,
	key session.Key,
	options ...TrimConversationOption,
) ([]event.Event, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := &trimEventOptions{
		ConversationCount: 1,
	}
	for _, o := range options {
		o(opt)
	}

	// Strategy: Check V2 Exists -> V2 Trim. Else -> V1 Trim (if legacy).
	v2Exists, err := s.v2Client.Exists(ctx, key)
	if err != nil {
		return nil, err
	}
	if v2Exists {
		return s.v2Client.TrimConversations(ctx, key, opt.ConversationCount)
	}

	if s.legacyEnabled() {
		// Call V1 Trim (Not implemented in v1 client yet)
		return nil, nil // Placeholder
	}

	return nil, nil
}

// Close closes the service.
func (s *Service) Close() error {
	s.once.Do(func() {
		for _, ch := range s.eventPairChans {
			close(ch)
		}
		for _, ch := range s.trackEventChans {
			close(ch)
		}
		s.persistWg.Wait()

		if s.asyncWorker != nil {
			s.asyncWorker.Stop()
		}

		if s.redisClient != nil {
			s.redisClient.Close()
		}
	})

	return nil
}

func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum

	// Initialize event channels
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(chan *sessionEventPair, defaultChanBufferSize)
	}

	// Initialize track event channels
	s.trackEventChans = make([]chan *trackEventPair, persisterNum)
	for i := 0; i < persisterNum; i++ {
		s.trackEventChans[i] = make(chan *trackEventPair, defaultChanBufferSize)
	}

	// Start event persist workers
	s.persistWg.Add(persisterNum)
	for _, eventPairChan := range s.eventPairChans {
		go func(eventPairChan chan *sessionEventPair) {
			defer s.persistWg.Done()
			for eventPair := range eventPairChan {
				ctx, cancel := context.WithTimeout(context.Background(), defaultAsyncPersistTimeout)
				if err := s.persistEvent(ctx, eventPair.version, eventPair.event, eventPair.key); err != nil {
					log.ErrorfContext(ctx, "async persist event failed: %v", err)
				}
				cancel()
			}
		}(eventPairChan)
	}

	// Start track event persist workers
	s.persistWg.Add(persisterNum)
	for _, trackEventChan := range s.trackEventChans {
		go func(trackEventChan chan *trackEventPair) {
			defer s.persistWg.Done()
			for trackPair := range trackEventChan {
				ctx, cancel := context.WithTimeout(context.Background(), defaultAsyncPersistTimeout)
				if err := s.persistTrackEvent(ctx, trackPair.version, trackPair.key, trackPair.event); err != nil {
					log.ErrorfContext(ctx, "async persist track event failed: %v", err)
				}
				cancel()
			}
		}(trackEventChan)
	}
}

func applyOptions(opts ...session.Option) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}
