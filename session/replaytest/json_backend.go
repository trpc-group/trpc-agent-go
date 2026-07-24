//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// JSONSessionService is a lightweight persistent-simulation session backend.
// It delegates storage to the in-memory service but JSON round-trips sessions
// at API boundaries to exercise serialization semantics without CGO or an
// external database.
type JSONSessionService struct {
	inner *sessioninmemory.SessionService
}

// JSONSessionOption configures JSONSessionService.
type JSONSessionOption func(*jsonSessionOptions)

type jsonSessionOptions struct {
	summarizer                summary.SessionSummarizer
	summaryFilterAllowlist    []string
	cascadeFullSessionSummary *bool
}

// WithJSONSessionSummarizer configures deterministic summary generation.
func WithJSONSessionSummarizer(s summary.SessionSummarizer) JSONSessionOption {
	return func(o *jsonSessionOptions) {
		o.summarizer = s
	}
}

// WithJSONSessionSummaryFilterAllowlist restricts branch summary keys.
func WithJSONSessionSummaryFilterAllowlist(keys ...string) JSONSessionOption {
	return func(o *jsonSessionOptions) {
		o.summaryFilterAllowlist = append([]string(nil), keys...)
	}
}

// WithJSONSessionCascadeFullSessionSummary controls branch-to-full summary cascade.
func WithJSONSessionCascadeFullSessionSummary(enable bool) JSONSessionOption {
	return func(o *jsonSessionOptions) {
		enabled := enable
		o.cascadeFullSessionSummary = &enabled
	}
}

// NewJSONSessionService creates a JSON round-trip session service.
func NewJSONSessionService(opts ...JSONSessionOption) *JSONSessionService {
	cfg := jsonSessionOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	innerOpts := []sessioninmemory.ServiceOpt{}
	if cfg.summarizer != nil {
		innerOpts = append(innerOpts, sessioninmemory.WithSummarizer(cfg.summarizer))
	}
	if len(cfg.summaryFilterAllowlist) > 0 {
		innerOpts = append(innerOpts, sessioninmemory.WithSummaryFilterAllowlist(cfg.summaryFilterAllowlist...))
	}
	if cfg.cascadeFullSessionSummary != nil {
		innerOpts = append(innerOpts, sessioninmemory.WithCascadeFullSessionSummary(*cfg.cascadeFullSessionSummary))
	}
	return &JSONSessionService{inner: sessioninmemory.NewSessionService(innerOpts...)}
}

var _ session.Service = (*JSONSessionService)(nil)
var _ session.TrackService = (*JSONSessionService)(nil)

func (s *JSONSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	sess, err := s.inner.CreateSession(ctx, key, state, options...)
	return cloneSessionJSON(sess), err
}

func (s *JSONSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	sess, err := s.inner.GetSession(ctx, key, options...)
	return cloneSessionJSON(sess), err
}

func (s *JSONSessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	sessions, err := s.inner.ListSessions(ctx, userKey, options...)
	if err != nil {
		return nil, err
	}
	out := make([]*session.Session, len(sessions))
	for i, sess := range sessions {
		out[i] = cloneSessionJSON(sess)
	}
	return out, nil
}

func (s *JSONSessionService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	return s.inner.DeleteSession(ctx, key, options...)
}

func (s *JSONSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return s.inner.UpdateAppState(ctx, appName, cloneStateJSON(state))
}

func (s *JSONSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return s.inner.DeleteAppState(ctx, appName, key)
}

func (s *JSONSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	state, err := s.inner.ListAppStates(ctx, appName)
	return cloneStateJSON(state), err
}

func (s *JSONSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return s.inner.UpdateUserState(ctx, userKey, cloneStateJSON(state))
}

func (s *JSONSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	state, err := s.inner.ListUserStates(ctx, userKey)
	return cloneStateJSON(state), err
}

func (s *JSONSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return s.inner.DeleteUserState(ctx, userKey, key)
}

func (s *JSONSessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	return s.inner.UpdateSessionState(ctx, key, cloneStateJSON(state))
}

func (s *JSONSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	if err := s.inner.AppendEvent(ctx, cloneSessionJSON(sess), cloneEventJSON(evt), options...); err != nil {
		return err
	}
	if sess != nil {
		sess.UpdateUserSession(evt, options...)
	}
	return nil
}

func (s *JSONSessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	return s.inner.CreateSessionSummary(ctx, cloneSessionJSON(sess), filterKey, force)
}

func (s *JSONSessionService) EnqueueSummaryJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	return s.inner.EnqueueSummaryJob(ctx, cloneSessionJSON(sess), filterKey, force)
}

func (s *JSONSessionService) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	return s.inner.GetSessionSummaryText(ctx, cloneSessionJSON(sess), opts...)
}

func (s *JSONSessionService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	if err := s.inner.AppendTrackEvent(ctx, cloneSessionJSON(sess), cloneTrackEventJSON(trackEvent), opts...); err != nil {
		return err
	}
	if sess != nil {
		return sess.AppendTrackEvent(trackEvent, opts...)
	}
	return nil
}

func (s *JSONSessionService) Close() error {
	return s.inner.Close()
}

// JSONMemoryService is a JSON round-trip memory backend.
type JSONMemoryService struct {
	inner *memoryinmemory.MemoryService
}

// NewJSONMemoryService creates a JSON round-trip memory service.
func NewJSONMemoryService() *JSONMemoryService {
	return &JSONMemoryService{inner: memoryinmemory.NewMemoryService()}
}

var _ memory.Service = (*JSONMemoryService)(nil)

func (s *JSONMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryText string,
	topics []string,
	opts ...memory.AddOption,
) error {
	return s.inner.AddMemory(ctx, userKey, memoryText, append([]string(nil), topics...), opts...)
}

func (s *JSONMemoryService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	memoryText string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	return s.inner.UpdateMemory(ctx, memoryKey, memoryText, append([]string(nil), topics...), opts...)
}

func (s *JSONMemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	return s.inner.DeleteMemory(ctx, memoryKey)
}

func (s *JSONMemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	return s.inner.ClearMemories(ctx, userKey)
}

func (s *JSONMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	entries, err := s.inner.ReadMemories(ctx, userKey, limit)
	return cloneMemoryEntriesJSON(entries), err
}

func (s *JSONMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	entries, err := s.inner.SearchMemories(ctx, userKey, query, opts...)
	return cloneMemoryEntriesJSON(entries), err
}

func (s *JSONMemoryService) Tools() []tool.Tool {
	return s.inner.Tools()
}

func (s *JSONMemoryService) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	return s.inner.EnqueueAutoMemoryJob(ctx, cloneSessionJSON(sess))
}

func (s *JSONMemoryService) Close() error {
	return s.inner.Close()
}

func cloneSessionJSON(sess *session.Session) *session.Session {
	if sess == nil {
		return nil
	}
	var out session.Session
	mustRoundTripJSON(sess, &out)
	return &out
}

func cloneEventJSON(evt *event.Event) *event.Event {
	if evt == nil {
		return nil
	}
	var out event.Event
	mustRoundTripJSON(evt, &out)
	return &out
}

func cloneTrackEventJSON(evt *session.TrackEvent) *session.TrackEvent {
	if evt == nil {
		return nil
	}
	var out session.TrackEvent
	mustRoundTripJSON(evt, &out)
	return &out
}

func cloneStateJSON(state session.StateMap) session.StateMap {
	if state == nil {
		return nil
	}
	var out session.StateMap
	mustRoundTripJSON(state, &out)
	return out
}

func cloneMemoryEntriesJSON(entries []*memory.Entry) []*memory.Entry {
	if entries == nil {
		return nil
	}
	var out []*memory.Entry
	mustRoundTripJSON(entries, &out)
	return out
}

func mustRoundTripJSON(in any, out any) {
	raw, err := json.Marshal(in)
	if err != nil {
		panic(fmt.Sprintf("marshal replay JSON backend: %v", err))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		panic(fmt.Sprintf("unmarshal replay JSON backend: %v", err))
	}
}
