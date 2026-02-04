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

	"github.com/google/uuid"
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

	// Normalize TTL values: negative TTL means no expiration (use 0)
	sessionTTL := opts.sessionTTL
	if sessionTTL < 0 {
		sessionTTL = 0
	}
	appStateTTL := opts.appStateTTL
	if appStateTTL < 0 {
		appStateTTL = 0
	}
	userStateTTL := opts.userStateTTL
	if userStateTTL < 0 {
		userStateTTL = 0
	}

	// Initialize V1 config
	v1Cfg := v1.Config{
		SessionTTL:        sessionTTL,
		AppStateTTL:       appStateTTL,
		UserStateTTL:      userStateTTL,
		SessionEventLimit: opts.sessionEventLimit,
		KeyPrefix:         opts.keyPrefix,
	}

	// Initialize V2 config
	v2Cfg := v2.Config{
		SessionTTL:        sessionTTL,
		AppStateTTL:       appStateTTL,
		UserStateTTL:      userStateTTL,
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
// Strategy (per xxx.md):
//   - If either V1 or V2 exists: return existing session (no supplementary creation)
//   - Only when BOTH v1/v2 don't exist: create in both storages (dual-write mode)
//   - Strict dual-write: both must succeed, any failure returns error with best-effort rollback
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

		// If either side exists, return existing session (no supplementary creation)
		// This ensures session "belongs" to whichever storage created it first.
		if v1Exists || v2Exists {
			sess, err := s.getSessionInternal(ctx, key, applyOptions(opts...), v1Exists, v2Exists)
			if err != nil {
				return nil, fmt.Errorf("get existing session: %w", err)
			}
			if sess != nil {
				return sess, nil
			}
		}
	}

	// Create new session - only reaches here when BOTH v1/v2 don't exist
	// Generate sessionID upfront to ensure V1 and V2 use the same ID in dual-write mode.
	if key.SessionID == "" {
		key.SessionID = uuid.New().String()
	}

	if s.needDualWrite() {
		// Strict dual-write: create in both V2 and V1, both must succeed
		sess, err := s.v2Client.CreateSession(ctx, key, state)
		if err != nil {
			return nil, fmt.Errorf("create session in V2 failed: %w", err)
		}

		if _, err := s.v1Client.CreateSession(ctx, key, state); err != nil {
			// V1 creation failed - best effort rollback V2
			if delErr := s.v2Client.DeleteSession(ctx, key); delErr != nil {
				log.WarnfContext(ctx, "failed to rollback V2 session after V1 creation failed: %v", delErr)
			}
			return nil, fmt.Errorf("dual-write session to V1 failed: %w", err)
		}

		// Merge appState and userState into session (matches V1 behavior)
		return s.mergeAppUserState(ctx, key, sess)
	}

	// Non-dual-write mode: create in V2 only
	sess, err := s.v2Client.CreateSession(ctx, key, state)
	if err != nil {
		return nil, err
	}

	// Merge appState and userState into session (matches V1 behavior)
	return s.mergeAppUserState(ctx, key, sess)
}

// mergeAppUserState queries and merges appState and userState into the session.
// This matches V1 behavior where CreateSession/GetSession returns session with merged states.
// It also refreshes TTL for appState and userState keys (matching V1 behavior).
func (s *Service) mergeAppUserState(ctx context.Context, key session.Key, sess *session.Session) (*session.Session, error) {
	if sess == nil {
		return nil, nil
	}

	// Query appState
	appState, err := s.v2Client.ListAppStates(ctx, key.AppName)
	if err != nil {
		log.WarnfContext(ctx, "failed to get appState for merge: %v", err)
		// Don't fail the whole operation, just skip merging appState
	}

	// Query userState
	userState, err := s.v2Client.ListUserStates(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	if err != nil {
		log.WarnfContext(ctx, "failed to get userState for merge: %v", err)
		// Don't fail the whole operation, just skip merging userState
	}

	// Merge states with prefixes
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
	}

	// Refresh TTL for appState and userState (matches V1 behavior)
	// This ensures shared states stay alive as long as any session is active.
	if err := s.v2Client.RefreshAppStateTTL(ctx, key.AppName); err != nil {
		log.WarnfContext(ctx, "failed to refresh appState TTL: %v", err)
	}
	if err := s.v2Client.RefreshUserStateTTL(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}); err != nil {
		log.WarnfContext(ctx, "failed to refresh userState TTL: %v", err)
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

// getSessionInternal retrieves session based on CompatMode.
// v1Exists/v2Exists indicate whether session exists in each storage version.
// Caller should call checkSessionExists first and pass the results.
//
// Read strategy:
//   - If V1 exists (legacy enabled): read V1 first (V1 may have more complete data during migration)
//   - Otherwise: read V2
func (s *Service) getSessionInternal(
	ctx context.Context,
	key session.Key,
	opts *session.Options,
	v1Exists, v2Exists bool,
) (*session.Session, error) {
	// Use sessionEventLimit as default if EventNum is not specified
	eventLimit := s.getEffectiveEventLimit(opts.EventNum)

	// V1 priority: if V1 exists and legacy is enabled, read V1
	// This ensures data completeness during migration (old instances may only write V1)
	if s.legacyEnabled() && v1Exists {
		return s.v1Client.GetSession(ctx, key, eventLimit, opts.EventTime)
	}

	// V2 read
	if v2Exists {
		return s.v2Client.GetSession(ctx, key, eventLimit, opts.EventTime)
	}

	return nil, nil // Not found
}

// getEffectiveEventLimit returns the effective event limit.
// If the provided limit is <= 0, it uses sessionEventLimit as default.
func (s *Service) getEffectiveEventLimit(limit int) int {
	if limit <= 0 {
		return s.opts.sessionEventLimit
	}
	return limit
}

// ListSessions lists all sessions by user scope of session key.
// Strategy: List V2 (Scan) + List V1 (HGetAll) -> Merge with V1 priority for duplicates.
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)

	// Use sessionEventLimit as default if EventNum is not specified
	eventLimit := s.getEffectiveEventLimit(opt.EventNum)

	v2Sessions, err := s.v2Client.ListSessions(ctx, userKey, eventLimit, opt.EventTime)
	if err != nil {
		return nil, fmt.Errorf("scan sessions (v2): %w", err)
	}

	// 2. List V1 (if legacy enabled)
	if !s.legacyEnabled() {
		return v2Sessions, nil
	}

	v1Sessions, err := s.v1Client.ListSessions(ctx, userKey, eventLimit, opt.EventTime)
	if err != nil {
		return nil, fmt.Errorf("list sessions (v1): %w", err)
	}

	// Merge: V1 priority for duplicates (V1 data is more complete during migration while enable dual-write)
	v1Map := make(map[string]*session.Session, len(v1Sessions))
	for _, s1 := range v1Sessions {
		v1Map[s1.ID] = s1
	}

	sessions := make([]*session.Session, 0, len(v2Sessions)+len(v1Sessions))
	for _, sess := range v2Sessions {
		if v1Sess, exists := v1Map[sess.ID]; exists {
			// Use V1 data for duplicates
			sessions = append(sessions, v1Sess)
			delete(v1Map, sess.ID)
		} else {
			sessions = append(sessions, sess)
		}
	}
	// Add V1-only sessions
	for _, s1 := range v1Map {
		sessions = append(sessions, s1)
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
	// Dual-write mode: strict dual-write based on session existence
	if s.needDualWrite() {
		return s.appendEventWithStrictDualWrite(ctx, key, e)
	}

	// fast path: use version tag
	switch ver {
	case v2.VersionV2:
		return s.v2Client.AppendEvent(ctx, key, e)
	case v1.VersionV1:
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

// appendEventWithStrictDualWrite implements strict dual-write semantics (per xxx.md):
//   - If both V1 and V2 exist: must write to both, both must succeed
//   - If only one side exists: write to existing side only (with warning)
//   - Any failure returns error immediately
func (s *Service) appendEventWithStrictDualWrite(ctx context.Context, key session.Key, e *event.Event) error {
	// Check which storages have this session
	v1Exists, v2Exists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		return fmt.Errorf("check session exists failed: %w", err)
	}

	// Case 1: Both exist - strict dual-write, both must succeed
	if v1Exists && v2Exists {
		if err := s.v2Client.AppendEvent(ctx, key, e); err != nil {
			return fmt.Errorf("dual-write to V2 failed: %w", err)
		}
		if err := s.v1Client.AppendEvent(ctx, key, e); err != nil {
			// V2 succeeded but V1 failed - this is a partial write
			// Log error for monitoring, but return error to caller
			log.ErrorfContext(ctx, "dual-write partial failure: V2 succeeded but V1 failed: %v", err)
			return fmt.Errorf("dual-write to V1 failed (V2 succeeded): %w", err)
		}
		return nil
	}

	// Case 2: Only V2 exists - write to V2 only (legacy session path)
	if v2Exists {
		log.WarnfContext(ctx, "dual-write mode but only V2 exists for session %s/%s/%s, writing to V2 only",
			key.AppName, key.UserID, key.SessionID)
		return s.v2Client.AppendEvent(ctx, key, e)
	}

	// Case 3: Only V1 exists - write to V1 only (old session not migrated)
	if v1Exists {
		log.WarnfContext(ctx, "dual-write mode but only V1 exists for session %s/%s/%s, writing to V1 only",
			key.AppName, key.UserID, key.SessionID)
		return s.v1Client.AppendEvent(ctx, key, e)
	}

	// Case 4: Neither exists - error
	return fmt.Errorf("session not found: %s/%s/%s", key.AppName, key.UserID, key.SessionID)
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
		return s.v1Client.TrimConversations(ctx, key, opt.ConversationCount)
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
