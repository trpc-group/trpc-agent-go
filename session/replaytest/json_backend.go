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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// JSONSessionService is a lightweight local persistent session backend. It uses
// a JSON file as the source of truth and reloads/writes it at service API
// boundaries, which exercises serialization and persistence semantics without
// CGO or an external database.
type JSONSessionService struct {
	mu       sync.Mutex
	path     string
	ownedDir string
	opts     jsonSessionOptions
}

// JSONSessionOption configures JSONSessionService.
type JSONSessionOption func(*jsonSessionOptions)

type jsonSessionOptions struct {
	path                      string
	summarizer                summary.SessionSummarizer
	summaryFilterAllowlist    []string
	cascadeFullSessionSummary *bool
}

type jsonSessionStore struct {
	Sessions      map[string]*session.Session  `json:"sessions,omitempty"`
	AppStates     map[string]session.StateMap  `json:"app_states,omitempty"`
	UserStates    map[string]session.StateMap  `json:"user_states,omitempty"`
	SummaryOwners map[string]map[string]string `json:"summary_owners,omitempty"`
}

// WithJSONSessionPath stores the persistent simulator data at path.
func WithJSONSessionPath(path string) JSONSessionOption {
	return func(o *jsonSessionOptions) {
		o.path = path
	}
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

// NewJSONSessionService creates a JSON file-backed session service.
func NewJSONSessionService(opts ...JSONSessionOption) *JSONSessionService {
	cfg := jsonSessionOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	ownedDir := ""
	if cfg.path == "" {
		ownedDir = mustTempDir("trpc-agent-replay-session-*")
		cfg.path = filepath.Join(ownedDir, "session.json")
	}
	return &JSONSessionService{
		path:     cfg.path,
		ownedDir: ownedDir,
		opts:     cfg,
	}
}

var _ session.Service = (*JSONSessionService)(nil)
var _ session.TrackService = (*JSONSessionService)(nil)
var _ SummaryOwnerProvider = (*JSONSessionService)(nil)

func (s *JSONSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}
	if key.SessionID == "" {
		key.SessionID = uuid.New().String()
	}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	for k, v := range state {
		sess.SetState(k, v)
	}
	var out *session.Session
	err := s.withSessionStore(true, func(store *jsonSessionStore) error {
		store.Sessions[sessionKeyString(key)] = cloneSessionJSON(sess)
		ensureJSONUserState(store, session.UserKey{AppName: key.AppName, UserID: key.UserID})
		out = mergeJSONState(
			store.AppStates[key.AppName],
			store.UserStates[userKeyString(session.UserKey{AppName: key.AppName, UserID: key.UserID})],
			sess.Clone(),
		)
		return nil
	})
	return cloneSessionJSON(out), err
}

func (s *JSONSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applySessionOptions(options...)
	if err := session.ValidateGetSessionOptions(opt, false); err != nil {
		return nil, err
	}
	var out *session.Session
	err := s.withSessionStore(false, func(store *jsonSessionStore) error {
		stored := store.Sessions[sessionKeyString(key)]
		if stored == nil {
			return nil
		}
		copied := stored.Clone()
		copied.ApplyEventFiltering(
			session.WithEventNum(opt.EventNum),
			session.WithEventTime(opt.EventTime),
		)
		applyJSONTrackFiltering(copied, opt)
		out = mergeJSONState(
			store.AppStates[key.AppName],
			store.UserStates[userKeyString(session.UserKey{AppName: key.AppName, UserID: key.UserID})],
			copied,
		)
		return nil
	})
	return cloneSessionJSON(out), err
}

func (s *JSONSessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	opt := applySessionOptions(options...)
	if err := session.ValidateListSessionsOptions(opt); err != nil {
		return nil, err
	}
	out := []*session.Session{}
	err := s.withSessionStore(false, func(store *jsonSessionStore) error {
		scope := userKeyString(userKey)
		for _, stored := range store.Sessions {
			if stored == nil || stored.AppName != userKey.AppName || stored.UserID != userKey.UserID {
				continue
			}
			copied := stored.Clone()
			if opt.ListSessionOnlyMeta {
				copied.Events = nil
				copied.Tracks = nil
			} else {
				copied.ApplyEventFiltering(
					session.WithEventNum(opt.EventNum),
					session.WithEventTime(opt.EventTime),
				)
				applyJSONTrackFiltering(copied, opt)
			}
			out = append(out, mergeJSONState(store.AppStates[userKey.AppName], store.UserStates[scope], copied))
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
				return out[i].ID < out[j].ID
			}
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		})
		if opt.ListSessionPage != nil {
			out = applyJSONListPage(out, opt.ListSessionPage.Offset, opt.ListSessionPage.Limit)
		}
		return nil
	})
	return cloneSessionSliceJSON(out), err
}

func (s *JSONSessionService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		delete(store.Sessions, sessionKeyString(key))
		delete(store.SummaryOwners, sessionKeyString(key))
		return nil
	})
}

func (s *JSONSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		if store.AppStates[appName] == nil {
			store.AppStates[appName] = make(session.StateMap)
		}
		applyJSONStatePatch(store.AppStates[appName], state, session.StateAppPrefix)
		return nil
	})
}

func (s *JSONSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		if state := store.AppStates[appName]; state != nil {
			delete(state, strings.TrimPrefix(key, session.StateAppPrefix))
		}
		return nil
	})
}

func (s *JSONSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}
	var out session.StateMap
	err := s.withSessionStore(false, func(store *jsonSessionStore) error {
		out = cloneStateJSON(store.AppStates[appName])
		if out == nil {
			out = make(session.StateMap)
		}
		return nil
	})
	return out, err
}

func (s *JSONSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		scope := userKeyString(userKey)
		if store.UserStates[scope] == nil {
			store.UserStates[scope] = make(session.StateMap)
		}
		applyJSONStatePatch(store.UserStates[scope], state, session.StateUserPrefix)
		return nil
	})
}

func (s *JSONSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	var out session.StateMap
	err := s.withSessionStore(false, func(store *jsonSessionStore) error {
		out = cloneStateJSON(store.UserStates[userKeyString(userKey)])
		if out == nil {
			out = make(session.StateMap)
		}
		return nil
	})
	return out, err
}

func (s *JSONSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		if state := store.UserStates[userKeyString(userKey)]; state != nil {
			delete(state, strings.TrimPrefix(key, session.StateUserPrefix))
		}
		return nil
	})
}

func (s *JSONSessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf("json session service update session state failed: %s is not allowed, use UpdateAppState instead", k)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf("json session service update session state failed: %s is not allowed, use UpdateUserState instead", k)
		}
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		stored := store.Sessions[sessionKeyString(key)]
		if stored == nil {
			return fmt.Errorf("json session service update session state failed: session not found")
		}
		for k, v := range state {
			stored.SetState(k, v)
		}
		stored.UpdatedAt = time.Now()
		return nil
	})
}

func (s *JSONSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	if evt == nil {
		return fmt.Errorf("event is nil")
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		stored := store.Sessions[sessionKeyString(key)]
		if stored == nil {
			return fmt.Errorf("session not found: %s", key.SessionID)
		}
		sess.UpdateUserSession(evt, options...)
		stored.UpdateUserSession(cloneEventJSON(evt), options...)
		return nil
	})
}

func (s *JSONSessionService) CreateSessionSummary(
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
	if !s.summaryPolicy().AllowsFilterKey(filterKey) {
		return nil
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		stored := store.Sessions[sessionKeyString(key)]
		if stored == nil {
			return fmt.Errorf("session not found: %s", key.SessionID)
		}
		working := cloneSessionJSON(sess)
		updated, err := isummary.SummarizeSession(ctx, s.opts.summarizer, working, filterKey, force)
		if err != nil || !updated {
			return err
		}
		working.SummariesMu.RLock()
		sum := working.Summaries[filterKey]
		working.SummariesMu.RUnlock()
		if sum == nil {
			return nil
		}
		if stored.Summaries == nil {
			stored.Summaries = make(map[string]*session.Summary)
		}
		stored.Summaries[filterKey] = sum.Clone()
		stored.UpdatedAt = sum.UpdatedAt
		if sess.Summaries == nil {
			sess.Summaries = make(map[string]*session.Summary)
		}
		sess.Summaries[filterKey] = sum.Clone()
		owners := ensureJSONSummaryOwners(store, key)
		owners[filterKey] = key.SessionID
		return nil
	})
}

func (s *JSONSessionService) EnqueueSummaryJob(
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
	return isummary.CreateSessionSummaryWithCascade(
		isummary.DetachContext(ctx),
		sess,
		filterKey,
		force,
		s.summaryPolicy(),
		s.CreateSessionSummary,
	)
}

func (s *JSONSessionService) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	if sess == nil {
		return "", false
	}
	if text, ok := isummary.GetSummaryTextFromSession(sess, opts...); ok {
		return text, true
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}
	var text string
	var ok bool
	_ = s.withSessionStore(false, func(store *jsonSessionStore) error {
		stored := store.Sessions[sessionKeyString(key)]
		if stored == nil {
			return nil
		}
		text, ok = isummary.GetSummaryTextFromSession(stored, opts...)
		return nil
	})
	return text, ok
}

func (s *JSONSessionService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	if trackEvent == nil {
		return fmt.Errorf("track event is nil")
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	return s.withSessionStore(true, func(store *jsonSessionStore) error {
		stored := store.Sessions[sessionKeyString(key)]
		if stored == nil {
			return fmt.Errorf("session not found: %s", key.SessionID)
		}
		if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
			return fmt.Errorf("append track event: %w", err)
		}
		if err := stored.AppendTrackEvent(cloneTrackEventJSON(trackEvent), opts...); err != nil {
			return fmt.Errorf("append track event: %w", err)
		}
		return nil
	})
}

// SummaryOwnerIDs returns the persisted owner session ID for each summary
// filter key stored under key.
func (s *JSONSessionService) SummaryOwnerIDs(ctx context.Context, key session.Key) (map[string]string, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	out := map[string]string{}
	err := s.withSessionStore(false, func(store *jsonSessionStore) error {
		for filterKey, owner := range store.SummaryOwners[sessionKeyString(key)] {
			out[filterKey] = owner
		}
		return nil
	})
	if len(out) == 0 {
		return nil, err
	}
	return out, err
}

func (s *JSONSessionService) Close() error {
	if s.ownedDir != "" {
		return os.RemoveAll(s.ownedDir)
	}
	return nil
}

func (s *JSONSessionService) summaryPolicy() isummary.SummaryDispatchPolicy {
	cascade := true
	if s.opts.cascadeFullSessionSummary != nil {
		cascade = *s.opts.cascadeFullSessionSummary
	}
	return isummary.NewSummaryDispatchPolicy(s.opts.summaryFilterAllowlist, cascade)
}

func (s *JSONSessionService) withSessionStore(write bool, fn func(*jsonSessionStore) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.readSessionStore()
	if err != nil {
		return err
	}
	if err := fn(store); err != nil {
		return err
	}
	if !write {
		return nil
	}
	return s.writeSessionStore(store)
}

func (s *JSONSessionService) readSessionStore() (*jsonSessionStore, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return newJSONSessionStore(), nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return newJSONSessionStore(), nil
	}
	var store jsonSessionStore
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, err
	}
	store.init()
	return &store, nil
}

func (s *JSONSessionService) writeSessionStore(store *jsonSessionStore) error {
	store.init()
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newJSONSessionStore() *jsonSessionStore {
	store := &jsonSessionStore{}
	store.init()
	return store
}

func (s *jsonSessionStore) init() {
	if s.Sessions == nil {
		s.Sessions = make(map[string]*session.Session)
	}
	if s.AppStates == nil {
		s.AppStates = make(map[string]session.StateMap)
	}
	if s.UserStates == nil {
		s.UserStates = make(map[string]session.StateMap)
	}
	if s.SummaryOwners == nil {
		s.SummaryOwners = make(map[string]map[string]string)
	}
}

// JSONMemoryService is a lightweight local persistent memory backend.
type JSONMemoryService struct {
	mu       sync.Mutex
	path     string
	ownedDir string
}

// JSONMemoryOption configures JSONMemoryService.
type JSONMemoryOption func(*jsonMemoryOptions)

type jsonMemoryOptions struct {
	path string
}

type jsonMemoryStore struct {
	Entries map[string]map[string]*memory.Entry `json:"entries,omitempty"`
}

// WithJSONMemoryPath stores the persistent simulator memory data at path.
func WithJSONMemoryPath(path string) JSONMemoryOption {
	return func(o *jsonMemoryOptions) {
		o.path = path
	}
}

// NewJSONMemoryService creates a JSON file-backed memory service.
func NewJSONMemoryService(opts ...JSONMemoryOption) *JSONMemoryService {
	cfg := jsonMemoryOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	ownedDir := ""
	if cfg.path == "" {
		ownedDir = mustTempDir("trpc-agent-replay-memory-*")
		cfg.path = filepath.Join(ownedDir, "memory.json")
	}
	return &JSONMemoryService{path: cfg.path, ownedDir: ownedDir}
}

var _ memory.Service = (*JSONMemoryService)(nil)

func (s *JSONMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryText string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	meta := memory.ResolveAddOptions(opts)
	now := time.Now()
	entry := newJSONMemoryEntry(userKey, memoryText, topics, meta, now)
	return s.withMemoryStore(true, func(store *jsonMemoryStore) error {
		scope := memoryScope(userKey)
		if store.Entries[scope] == nil {
			store.Entries[scope] = make(map[string]*memory.Entry)
		}
		store.Entries[scope][entry.ID] = cloneMemoryEntryJSON(entry)
		return nil
	})
}

func (s *JSONMemoryService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	memoryText string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	meta := memory.ResolveUpdateOptions(opts)
	result := memory.ResolveUpdateResult(opts)
	now := time.Now()
	return s.withMemoryStore(true, func(store *jsonMemoryStore) error {
		scope := memoryScope(memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID})
		entries := store.Entries[scope]
		if entries == nil {
			return fmt.Errorf("user %s not found", memoryKey.UserID)
		}
		entry := entries[memoryKey.MemoryID]
		if entry == nil {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}
		if entry.Memory == nil {
			entry.Memory = &memory.Memory{}
		}
		entry.AppName = memoryKey.AppName
		entry.UserID = memoryKey.UserID
		entry.Memory.Memory = strings.TrimSpace(memoryText)
		entry.Memory.Topics = append([]string(nil), topics...)
		entry.Memory.LastUpdated = &now
		applyJSONMemoryMetadataPatch(entry.Memory, meta)
		entry.UpdatedAt = now
		newID := jsonMemoryID(entry.AppName, entry.UserID, entry.Memory)
		if newID != memoryKey.MemoryID {
			if _, conflict := entries[newID]; conflict {
				return fmt.Errorf("memory with id %s already exists", newID)
			}
			delete(entries, memoryKey.MemoryID)
		}
		entry.ID = newID
		entries[newID] = cloneMemoryEntryJSON(entry)
		if result != nil {
			result.MemoryID = newID
		}
		return nil
	})
}

func (s *JSONMemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	return s.withMemoryStore(true, func(store *jsonMemoryStore) error {
		scope := memoryScope(memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID})
		entries := store.Entries[scope]
		if entries == nil {
			return fmt.Errorf("user %s not found", memoryKey.UserID)
		}
		if entries[memoryKey.MemoryID] == nil {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}
		delete(entries, memoryKey.MemoryID)
		return nil
	})
}

func (s *JSONMemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	return s.withMemoryStore(true, func(store *jsonMemoryStore) error {
		delete(store.Entries, memoryScope(userKey))
		return nil
	})
}

func (s *JSONMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	var out []*memory.Entry
	err := s.withMemoryStore(false, func(store *jsonMemoryStore) error {
		for _, entry := range store.Entries[memoryScope(userKey)] {
			out = append(out, cloneMemoryEntryJSON(entry))
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
				if out[i].CreatedAt.Equal(out[j].CreatedAt) {
					return out[i].ID < out[j].ID
				}
				return out[i].CreatedAt.After(out[j].CreatedAt)
			}
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		})
		if limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		return nil
	})
	return cloneMemoryEntriesJSON(out), err
}

func (s *JSONMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	temp := memoryinmemory.NewMemoryService()
	defer temp.Close()
	err := s.withMemoryStore(false, func(store *jsonMemoryStore) error {
		entries := make([]*memory.Entry, 0, len(store.Entries[memoryScope(userKey)]))
		for _, entry := range store.Entries[memoryScope(userKey)] {
			entries = append(entries, cloneMemoryEntryJSON(entry))
		}
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].ID < entries[j].ID
		})
		for _, entry := range entries {
			meta := metadataFromMemory(entry.Memory)
			if err := temp.AddMemory(
				ctx,
				userKey,
				entry.Memory.Memory,
				append([]string(nil), entry.Memory.Topics...),
				memory.WithMetadata(meta),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	entries, err := temp.SearchMemories(ctx, userKey, query, opts...)
	return cloneMemoryEntriesJSON(entries), err
}

func (s *JSONMemoryService) Tools() []tool.Tool {
	temp := memoryinmemory.NewMemoryService()
	defer temp.Close()
	return temp.Tools()
}

func (s *JSONMemoryService) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	return nil
}

func (s *JSONMemoryService) Close() error {
	if s.ownedDir != "" {
		return os.RemoveAll(s.ownedDir)
	}
	return nil
}

func (s *JSONMemoryService) withMemoryStore(write bool, fn func(*jsonMemoryStore) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.readMemoryStore()
	if err != nil {
		return err
	}
	if err := fn(store); err != nil {
		return err
	}
	if !write {
		return nil
	}
	return s.writeMemoryStore(store)
}

func (s *JSONMemoryService) readMemoryStore() (*jsonMemoryStore, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return newJSONMemoryStore(), nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return newJSONMemoryStore(), nil
	}
	var store jsonMemoryStore
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, err
	}
	store.init()
	return &store, nil
}

func (s *JSONMemoryService) writeMemoryStore(store *jsonMemoryStore) error {
	store.init()
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newJSONMemoryStore() *jsonMemoryStore {
	store := &jsonMemoryStore{}
	store.init()
	return store
}

func (s *jsonMemoryStore) init() {
	if s.Entries == nil {
		s.Entries = make(map[string]map[string]*memory.Entry)
	}
}

func applySessionOptions(opts ...session.Option) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}

func applyJSONTrackFiltering(sess *session.Session, opt *session.Options) {
	if sess == nil || sess.Tracks == nil || opt == nil {
		return
	}
	if opt.EventNum <= 0 && opt.EventTime.IsZero() {
		return
	}
	for _, history := range sess.Tracks {
		if history == nil {
			continue
		}
		history.Events = filterJSONTrackEvents(history.Events, opt.EventTime, opt.EventNum)
	}
}

func filterJSONTrackEvents(events []session.TrackEvent, after time.Time, limit int) []session.TrackEvent {
	filtered := events
	if !after.IsZero() {
		out := make([]session.TrackEvent, 0, len(filtered))
		for _, evt := range filtered {
			if !evt.Timestamp.Before(after) {
				out = append(out, evt)
			}
		}
		filtered = out
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return append([]session.TrackEvent(nil), filtered...)
}

func applyJSONListPage(sessions []*session.Session, offset, limit int) []*session.Session {
	if offset >= len(sessions) {
		return []*session.Session{}
	}
	end := offset + limit
	if end > len(sessions) {
		end = len(sessions)
	}
	return sessions[offset:end]
}

func mergeJSONState(appState, userState session.StateMap, sess *session.Session) *session.Session {
	if sess == nil {
		return nil
	}
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
	}
	return sess
}

func applyJSONStatePatch(dst session.StateMap, patch session.StateMap, trimPrefix string) {
	for k, v := range patch {
		k = strings.TrimPrefix(k, trimPrefix)
		if v == nil {
			dst[k] = nil
			continue
		}
		copied := make([]byte, len(v))
		copy(copied, v)
		dst[k] = copied
	}
}

func ensureJSONUserState(store *jsonSessionStore, userKey session.UserKey) {
	scope := userKeyString(userKey)
	if store.UserStates[scope] == nil {
		store.UserStates[scope] = make(session.StateMap)
	}
	if store.AppStates[userKey.AppName] == nil {
		store.AppStates[userKey.AppName] = make(session.StateMap)
	}
}

func ensureJSONSummaryOwners(store *jsonSessionStore, key session.Key) map[string]string {
	scope := sessionKeyString(key)
	if store.SummaryOwners[scope] == nil {
		store.SummaryOwners[scope] = make(map[string]string)
	}
	return store.SummaryOwners[scope]
}

func sessionKeyString(key session.Key) string {
	return strings.Join([]string{key.AppName, key.UserID, key.SessionID}, "\x00")
}

func userKeyString(key session.UserKey) string {
	return strings.Join([]string{key.AppName, key.UserID}, "\x00")
}

func memoryScope(key memory.UserKey) string {
	return strings.Join([]string{key.AppName, key.UserID}, "\x00")
}

func newJSONMemoryEntry(
	userKey memory.UserKey,
	memoryText string,
	topics []string,
	meta *memory.Metadata,
	now time.Time,
) *memory.Entry {
	mem := &memory.Memory{
		Memory:      strings.TrimSpace(memoryText),
		Topics:      append([]string(nil), topics...),
		LastUpdated: &now,
	}
	applyJSONMemoryMetadata(mem, meta)
	return &memory.Entry{
		ID:        jsonMemoryID(userKey.AppName, userKey.UserID, mem),
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    mem,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func applyJSONMemoryMetadata(mem *memory.Memory, meta *memory.Metadata) {
	if mem == nil {
		return
	}
	if meta != nil {
		if meta.Kind != "" {
			mem.Kind = meta.Kind
		}
		if meta.EventTime != nil {
			copied := meta.EventTime.UTC()
			mem.EventTime = &copied
		}
		mem.Participants = append([]string(nil), meta.Participants...)
		mem.Location = meta.Location
	}
	normalizeJSONMemory(mem)
}

func applyJSONMemoryMetadataPatch(mem *memory.Memory, meta *memory.Metadata) {
	if mem == nil {
		return
	}
	if meta != nil {
		if meta.Kind != "" {
			mem.Kind = meta.Kind
		}
		if meta.EventTime != nil {
			copied := meta.EventTime.UTC()
			mem.EventTime = &copied
		}
		if len(meta.Participants) > 0 {
			mem.Participants = append([]string(nil), meta.Participants...)
		}
		if meta.Location != "" {
			mem.Location = meta.Location
		}
	}
	normalizeJSONMemory(mem)
}

func normalizeJSONMemory(mem *memory.Memory) {
	if mem == nil {
		return
	}
	mem.Memory = strings.TrimSpace(mem.Memory)
	mem.Location = strings.TrimSpace(mem.Location)
	mem.Participants = normalizeJSONParticipants(mem.Participants)
	if mem.Kind == "" {
		mem.Kind = memory.KindFact
	}
}

func normalizeJSONParticipants(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, participant := range in {
		participant = strings.TrimSpace(participant)
		if participant == "" {
			continue
		}
		folded := strings.ToLower(participant)
		if _, ok := seen[folded]; ok {
			continue
		}
		seen[folded] = struct{}{}
		out = append(out, participant)
	}
	sort.Slice(out, func(i, j int) bool {
		li := strings.ToLower(out[i])
		lj := strings.ToLower(out[j])
		if li != lj {
			return li < lj
		}
		return out[i] < out[j]
	})
	return out
}

func jsonMemoryID(appName, userID string, mem *memory.Memory) string {
	canonical := struct {
		AppName      string   `json:"app_name"`
		UserID       string   `json:"user_id"`
		Content      string   `json:"content"`
		Kind         string   `json:"kind,omitempty"`
		EventTime    string   `json:"event_time,omitempty"`
		Participants []string `json:"participants,omitempty"`
		Location     string   `json:"location,omitempty"`
	}{
		AppName: appName,
		UserID:  userID,
	}
	if mem != nil {
		canonical.Content = strings.TrimSpace(mem.Memory)
		canonical.Kind = string(mem.Kind)
		if mem.EventTime != nil {
			canonical.EventTime = mem.EventTime.UTC().Format(time.RFC3339Nano)
		}
		canonical.Participants = normalizeJSONParticipants(mem.Participants)
		canonical.Location = strings.TrimSpace(mem.Location)
	}
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:])
}

func metadataFromMemory(mem *memory.Memory) *memory.Metadata {
	if mem == nil {
		return nil
	}
	return &memory.Metadata{
		Kind:         mem.Kind,
		EventTime:    mem.EventTime,
		Participants: append([]string(nil), mem.Participants...),
		Location:     mem.Location,
	}
}

func cloneSessionJSON(sess *session.Session) *session.Session {
	if sess == nil {
		return nil
	}
	var out session.Session
	mustRoundTripJSON(sess, &out)
	return &out
}

func cloneSessionSliceJSON(sessions []*session.Session) []*session.Session {
	if sessions == nil {
		return nil
	}
	out := make([]*session.Session, len(sessions))
	for i, sess := range sessions {
		out[i] = cloneSessionJSON(sess)
	}
	return out
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

func cloneMemoryEntryJSON(entry *memory.Entry) *memory.Entry {
	if entry == nil {
		return nil
	}
	var out memory.Entry
	mustRoundTripJSON(entry, &out)
	return &out
}

func cloneMemoryEntriesJSON(entries []*memory.Entry) []*memory.Entry {
	if entries == nil {
		return nil
	}
	out := make([]*memory.Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, cloneMemoryEntryJSON(entry))
	}
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

func mustTempDir(pattern string) string {
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		panic(fmt.Sprintf("create replay temp dir: %v", err))
	}
	return dir
}
