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
	"slices"
	"sync"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/zset"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

var (
	_ session.Service      = (*Service)(nil)
	_ session.TrackService = (*Service)(nil)
)

// Service is the redis session service.
// It acts as a facade, routing requests to hashidx (default) or zset (legacy) implementations.
// HashIdx is the improved storage with separated data and index, while zset is the legacy
// ZSet-based storage kept for backward compatibility during migration.
type Service struct {
	opts            ServiceOpts
	redisClient     redis.UniversalClient
	eventPairChans  []chan *sessionEventPair     // channel for session events to persistence
	trackEventChans []chan *trackEventPair       // channel for track events to persistence
	asyncWorker     *isummary.AsyncSummaryWorker // async summary worker
	persistWg       sync.WaitGroup               // wait group for persist workers
	once            sync.Once                    // ensure Close is called only once

	zsetClient    *zset.Client    // legacy ZSet-based storage client
	hashidxClient *hashidx.Client // improved Hash+Index storage client
}

type sessionEventPair struct {
	key     session.Key
	event   *event.Event
	version string
}

type trackEventPair struct {
	key         session.Key
	event       *session.TrackEvent
	version     string
	tracksState []byte // serialized tracks state from session.State["tracks"]
}

// compatEnabled returns true if zset storage awareness is needed.
// Both Transition and Legacy modes need to read/check zset.
func (s *Service) compatEnabled() bool {
	return s.opts.compatMode == CompatModeTransition || s.opts.compatMode == CompatModeLegacy
}

// transitionEnabled returns true if transition mode is active.
// In transition mode:
//   - New session creation goes to zset only
//   - Reads route based on actual session location (hashidx or zset)
func (s *Service) transitionEnabled() bool {
	return s.opts.compatMode == CompatModeTransition
}

func (s *Service) startSpan(ctx context.Context, name string, key session.Key) (context.Context, trace.Span) {
	if !s.opts.enableTracing {
		return ctx, trace.SpanFromContext(ctx)
	}
	ctx, span := atrace.Tracer.Start(ctx, name)
	span.SetAttributes(
		attribute.String("app_name", key.AppName),
		attribute.String("user_id", key.UserID),
		attribute.String("session_id", key.SessionID),
	)
	return ctx, span
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

	// Initialize ZSet config
	zsetCfg := zset.Config{
		SessionTTL:        sessionTTL,
		AppStateTTL:       appStateTTL,
		UserStateTTL:      userStateTTL,
		SessionEventLimit: opts.sessionEventLimit,
		KeyPrefix:         opts.keyPrefix,
	}

	// Initialize HashIdx config
	hashidxCfg := hashidx.Config{
		SessionTTL:        sessionTTL,
		AppStateTTL:       appStateTTL,
		UserStateTTL:      userStateTTL,
		SessionEventLimit: opts.sessionEventLimit,
		KeyPrefix:         opts.keyPrefix,
	}

	s := &Service{
		opts:          opts,
		redisClient:   redisClient,
		zsetClient:    zset.NewClient(redisClient, zsetCfg),
		hashidxClient: hashidx.NewClient(redisClient, hashidxCfg),
	}

	// Initialize Async Persistence
	if opts.enableAsyncPersist {
		s.startAsyncPersistWorker()
	}

	// Start async summary workers if summary generation is configured.
	if isummary.HasSummarizer(opts.summarizer, opts.summarizerResolver) && opts.asyncSummaryNum > 0 {
		s.asyncWorker = isummary.NewAsyncSummaryWorker(isummary.AsyncSummaryConfig{
			Summarizer:         opts.summarizer,
			SummarizerResolver: opts.summarizerResolver,
			AsyncSummaryNum:    opts.asyncSummaryNum,
			SummaryQueueSize:   opts.summaryQueueSize,
			SummaryJobTimeout:  opts.summaryJobTimeout,
			CreateSummaryFunc:  s.CreateSessionSummary,
		})
		s.asyncWorker.Start()
	}

	return s, nil
}

// checkSessionExists checks if session exists in zset and hashidx using pipeline.
// Returns (zsetExists, hashidxExists, error).
// If both exist, logs an error for data inconsistency investigation.
// TODO: Remove this defensive check after the system is stable.
func (s *Service) checkSessionExists(ctx context.Context, key session.Key) (bool, bool, error) {
	zsetExists, hashidxExists := false, false
	pipe := s.redisClient.Pipeline()

	// Add hashidx check to pipeline
	hashidxCmd := s.hashidxClient.ExistsPipelined(ctx, pipe, key)

	// Add zset check to pipeline if zset awareness is enabled
	var zsetCmd *redis.BoolCmd
	if s.compatEnabled() {
		zsetCmd = s.zsetClient.ExistsPipelined(ctx, pipe, key)
	}

	// Execute pipeline (go-redis handles multi-slot routing in cluster mode)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return false, false, fmt.Errorf("check session exists pipeline: %w", err)
	}

	// Extract results
	if hashidxResult, err := hashidxCmd.Result(); err != nil && err != redis.Nil {
		return false, false, fmt.Errorf("check hashidx exists: %w", err)
	} else {
		hashidxExists = hashidxResult > 0
	}

	if zsetCmd != nil {
		if zsetResult, err := zsetCmd.Result(); err != nil && err != redis.Nil {
			return false, false, fmt.Errorf("check zset exists: %w", err)
		} else {
			zsetExists = zsetResult
		}
	}

	return zsetExists, hashidxExists, nil
}

// CreateSession creates a new session.
// Strategy:
//   - Transition mode: create in zset only (identical to old instances)
//   - Legacy/None mode: create in hashidx only
//   - If session already exists in either storage: return existing session
func (s *Service) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	ctx, span := s.startSpan(ctx, "create_session", key)
	defer span.End()

	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}

	// Check if session already exists (both storages)
	if key.SessionID != "" {
		zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("check session exists: %w", err)
		}

		// If either side exists, return existing session (no supplementary creation)
		// This ensures session "belongs" to whichever storage created it first.
		if zsetExists || hashidxExists {
			sess, storageType, err := s.getSessionInternal(ctx, key, applyOptions(opts...), zsetExists, hashidxExists)
			if err != nil {
				return nil, fmt.Errorf("get existing session: %w", err)
			}
			if sess != nil {
				span.SetAttributes(
					attribute.String("storage", storageType),
					attribute.Bool("existing", true),
				)
				s.recordStorageRoute(ctx, opCreateSession, storageType)
				return sess, nil
			}
		}
	}

	// Generate session ID if not provided
	if key.SessionID == "" {
		key.SessionID = uuid.New().String()
	}

	// Transition mode: create new session in zset only
	if s.transitionEnabled() {
		sess, err := s.zsetClient.CreateSession(ctx, key, state)
		if err != nil {
			return nil, err
		}
		sess, err = s.mergeAppUserState(ctx, key, sess)
		if err != nil {
			return nil, err
		}
		span.SetAttributes(attribute.String("storage", util.StorageTypeZset))
		s.recordStorageRoute(ctx, opCreateSession, util.StorageTypeZset)
		return sess, nil
	}

	// Default: create new session in hashidx
	sess, err := s.hashidxClient.CreateSession(ctx, key, state)
	if err != nil {
		return nil, err
	}

	// Merge appState and userState into session (matches zset behavior)
	sess, err = s.mergeAppUserState(ctx, key, sess)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(attribute.String("storage", util.StorageTypeHashIdx))
	s.recordStorageRoute(ctx, opCreateSession, util.StorageTypeHashIdx)
	return sess, nil
}

// mergeAppUserState queries and merges appState and userState into the session.
// This matches zset behavior where CreateSession/GetSession returns session with merged states.
// It also refreshes TTL for appState and userState keys (matching zset behavior).
func (s *Service) mergeAppUserState(ctx context.Context, key session.Key, sess *session.Session) (*session.Session, error) {
	if sess == nil {
		return nil, nil
	}

	// Query appState
	appState, err := s.hashidxClient.ListAppStates(ctx, key.AppName)
	if err != nil {
		log.WarnfContext(ctx, "failed to get appState for merge: %v", err)
		// Don't fail the whole operation, just skip merging appState
	}

	// Query userState
	userState, err := s.hashidxClient.ListUserStates(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
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

	// Refresh TTL for appState and userState (matches zset behavior)
	// This ensures shared states stay alive as long as any session is active.
	if err := s.hashidxClient.RefreshAppStateTTL(ctx, key.AppName); err != nil {
		log.WarnfContext(ctx, "failed to refresh appState TTL: %v", err)
	}
	if err := s.hashidxClient.RefreshUserStateTTL(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}); err != nil {
		log.WarnfContext(ctx, "failed to refresh userState TTL: %v", err)
	}

	return sess, nil
}

// GetSession gets a session.
// Strategy:
//   - Transition/Legacy mode: check both storages, zset priority if exists
//   - None mode: hashidx only
func (s *Service) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	ctx, span := s.startSpan(ctx, "get_session", key)
	defer span.End()

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
		zsetExists, hashidxExists, err := s.checkSessionExists(c.Context, c.Key)
		if err != nil {
			log.WarnfContext(c.Context, "checkSessionExists failed: %v", err)
		}
		sess, storageType, err := s.getSessionInternal(c.Context, c.Key, c.Options, zsetExists, hashidxExists)
		if err != nil {
			return nil, err
		}
		if sess != nil {
			span.SetAttributes(attribute.String("storage", storageType))
			s.recordStorageRoute(c.Context, opGetSession, storageType)
		}
		return sess, nil
	}
	sess, err := hook.RunGetSessionHooks(s.opts.getSessionHooks, hctx, final)
	if err != nil {
		return nil, fmt.Errorf("redis session service get session state failed: %w", err)
	}
	return sess, nil
}

// getSessionInternal retrieves session based on storage location.
// zsetExists/hashidxExists indicate whether session exists in each storage version.
// Caller should call checkSessionExists first and pass the results.
//
// Read strategy:
//   - If zset exists (transition or legacy enabled): read zset first
//   - Otherwise: read hashidx
func (s *Service) getSessionInternal(
	ctx context.Context,
	key session.Key,
	opts *session.Options,
	zsetExists, hashidxExists bool,
) (*session.Session, string, error) {
	eventLimit := s.getEffectiveEventLimit(opts.EventNum)

	if s.compatEnabled() && zsetExists {
		sess, err := s.zsetClient.GetSession(ctx, key, eventLimit, opts.EventTime)
		return sess, util.StorageTypeZset, err
	}

	if hashidxExists {
		sess, err := s.hashidxClient.GetSession(ctx, key, eventLimit, opts.EventTime)
		return sess, util.StorageTypeHashIdx, err
	}

	return nil, "", nil
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
// Strategy:
//   - Transition/Legacy mode: list hashidx + list zset -> merge with zset priority
//   - None mode: list hashidx only
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	eventLimit := s.getEffectiveEventLimit(opt.EventNum)

	hashidxSessions, err := s.hashidxClient.ListSessions(ctx, userKey, eventLimit, opt.EventTime)
	if err != nil {
		return nil, fmt.Errorf("scan sessions (hashidx): %w", err)
	}

	// List zset (if zset awareness is enabled: transition or legacy)
	if !s.compatEnabled() {
		return hashidxSessions, nil
	}

	zsetSessions, err := s.zsetClient.ListSessions(ctx, userKey, eventLimit, opt.EventTime)
	if err != nil {
		return nil, fmt.Errorf("list sessions (zset): %w", err)
	}

	// Merge: zset priority for duplicates (zset data is more complete during migration)
	zsetMap := make(map[string]*session.Session, len(zsetSessions))
	for _, s1 := range zsetSessions {
		zsetMap[s1.ID] = s1
	}

	sessions := make([]*session.Session, 0, len(hashidxSessions)+len(zsetSessions))
	for _, sess := range hashidxSessions {
		if zsetSess, exists := zsetMap[sess.ID]; exists {
			sessions = append(sessions, zsetSess)
			delete(zsetMap, sess.ID)
		} else {
			sessions = append(sessions, sess)
		}
	}
	for _, s1 := range zsetMap {
		sessions = append(sessions, s1)
	}

	// Sort by UpdatedAt descending to match SQL-based implementations.
	slices.SortFunc(sessions, func(a, b *session.Session) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})

	return sessions, nil
}

// DeleteSession deletes a session.
func (s *Service) DeleteSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) error {
	ctx, span := s.startSpan(ctx, "delete_session", key)
	defer span.End()

	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	var errhashidx, errzset error
	// Delete hashidx
	errhashidx = s.hashidxClient.DeleteSession(ctx, key)

	// Delete zset (if zset awareness is enabled: transition or legacy)
	if s.compatEnabled() {
		errzset = s.zsetClient.DeleteSession(ctx, key)

	}

	if errhashidx != nil {
		return fmt.Errorf("delete session (hashidx): %w", errhashidx)
	}
	if errzset != nil {
		return fmt.Errorf("delete session (zset): %w", errzset)
	}
	return nil
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
	return sess.ServiceMeta[util.ServiceMetaStorageTypeKey]
}

func (s *Service) persistEvent(ctx context.Context, ver string, e *event.Event, key session.Key) error {
	ctx, span := s.startSpan(ctx, "append_event", key)
	defer span.End()

	// fast path: use version tag
	switch ver {
	case util.StorageTypeHashIdx:
		err := s.hashidxClient.AppendEvent(ctx, key, e)
		if err != nil {
			log.WarnfContext(ctx, "append_event failed: storage=hashidx err=%v", err)
			return err
		}
		span.SetAttributes(attribute.String("storage", util.StorageTypeHashIdx))
		s.recordStorageRoute(ctx, opAppendEvent, util.StorageTypeHashIdx)
		return nil
	case util.StorageTypeZset:
		err := s.zsetClient.AppendEvent(ctx, key, e)
		if err != nil {
			log.WarnfContext(ctx, "append_event failed: storage=zset err=%v", err)
			return err
		}
		span.SetAttributes(attribute.String("storage", util.StorageTypeZset))
		s.recordStorageRoute(ctx, opAppendEvent, util.StorageTypeZset)
		return nil
	}

	// Slow path: no version tag, check storage.
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists in persistEvent failed: %v", err)
	}

	if s.compatEnabled() && zsetExists {
		err := s.zsetClient.AppendEvent(ctx, key, e)
		if err != nil {
			log.WarnfContext(ctx, "append_event failed: storage=zset err=%v", err)
			return err
		}
		span.SetAttributes(attribute.String("storage", util.StorageTypeZset))
		s.recordStorageRoute(ctx, opAppendEvent, util.StorageTypeZset)
		return nil
	}
	if hashidxExists {
		err := s.hashidxClient.AppendEvent(ctx, key, e)
		if err != nil {
			log.WarnfContext(ctx, "append_event failed: storage=hashidx err=%v", err)
			return err
		}
		span.SetAttributes(attribute.String("storage", util.StorageTypeHashIdx))
		s.recordStorageRoute(ctx, opAppendEvent, util.StorageTypeHashIdx)
		return nil
	}

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

	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		return nil, err
	}

	// zset first: if zset exists, it's a legacy session.
	if s.compatEnabled() && zsetExists {
		return s.zsetClient.TrimConversations(ctx, key, opt.ConversationCount)
	}
	if hashidxExists {
		return s.hashidxClient.TrimConversations(ctx, key, opt.ConversationCount)
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
				if err := s.persistTrackEvent(ctx, trackPair.version, trackPair.key, trackPair.event, trackPair.tracksState); err != nil {
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
