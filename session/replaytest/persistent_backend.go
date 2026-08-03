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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// This file provides a file-persisted "simulated persistent" backend for
// cross-backend replay comparison (acceptance criterion #1: at least one
// persistent or simulated-persistent backend alongside InMemory).
//
// Sessions and memories are JSON-serialized to disk on every write, so a
// fresh service instance pointed at the same directory sees prior data —
// the persistence semantic a persistent backend must demonstrate.

// fileStore is the on-disk state shared by the persistent session and
// memory services.
type fileStore struct {
	Sessions map[string]*session.Session `json:"sessions"`
	Memories map[string][]*memory.Entry  `json:"memories"`
}

func newFileStore() *fileStore {
	return &fileStore{
		Sessions: map[string]*session.Session{},
		Memories: map[string][]*memory.Entry{},
	}
}

func loadStore(path string) (*fileStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newFileStore(), nil
		}
		return nil, err
	}
	st := newFileStore()
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}
	if st.Sessions == nil {
		st.Sessions = map[string]*session.Session{}
	}
	if st.Memories == nil {
		st.Memories = map[string][]*memory.Entry{}
	}
	return st, nil
}

func saveStore(path string, st *fileStore) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sessKey(k session.Key) string {
	return k.AppName + "|" + k.UserID + "|" + k.SessionID
}

func memKey(k memory.UserKey) string {
	return k.AppName + "|" + k.UserID
}

// ─────────────────────────────────────────────────────────────────────
// persistentSessionService — session.Service + session.TrackService
// ─────────────────────────────────────────────────────────────────────

type persistentSessionService struct {
	mu   sync.Mutex
	path string
}

func newPersistentSessionService(dir string) *persistentSessionService {
	return &persistentSessionService{path: filepath.Join(dir, "sessions.json")}
}

func (s *persistentSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, opts ...session.Option) (*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := loadStore(s.path)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     state,
		CreatedAt: now,
		UpdatedAt: now,
		Summaries: map[string]*session.Summary{},
		Tracks:    map[session.Track]*session.TrackEvents{},
	}
	st.Sessions[sessKey(key)] = sess
	return sess, saveStore(s.path, st)
}

func (s *persistentSessionService) GetSession(ctx context.Context, key session.Key, opts ...session.Option) (*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := loadStore(s.path)
	if err != nil {
		return nil, err
	}
	sess, ok := st.Sessions[sessKey(key)]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", key.SessionID)
	}
	return sess, nil
}

func (s *persistentSessionService) ListSessions(ctx context.Context, userKey session.UserKey, opts ...session.Option) ([]*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := loadStore(s.path)
	if err != nil {
		return nil, err
	}
	var out []*session.Session
	for k, sess := range st.Sessions {
		if strings.HasPrefix(k, userKey.AppName+"|"+userKey.UserID+"|") {
			out = append(out, sess)
		}
	}
	return out, nil
}

func (s *persistentSessionService) DeleteSession(ctx context.Context, key session.Key, opts ...session.Option) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := loadStore(s.path)
	if err != nil {
		return err
	}
	delete(st.Sessions, sessKey(key))
	return saveStore(s.path, st)
}

func (s *persistentSessionService) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event, opts ...session.Option) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := loadStore(s.path)
	if err != nil {
		return err
	}
	stored := st.Sessions[sessKey(session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID})]
	if stored == nil {
		return fmt.Errorf("session not found: %s", sess.ID)
	}
	stored.Events = append(stored.Events, *evt)
	stored.UpdatedAt = time.Now()
	if len(evt.StateDelta) > 0 {
		if stored.State == nil {
			stored.State = session.StateMap{}
		}
		for k, v := range evt.StateDelta {
			stored.State[k] = v
		}
	}
	return saveStore(s.path, st)
}

func (s *persistentSessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := loadStore(s.path)
	if err != nil {
		return err
	}
	stored := st.Sessions[sessKey(session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID})]
	if stored == nil {
		return fmt.Errorf("session not found: %s", sess.ID)
	}
	if stored.Summaries == nil {
		stored.Summaries = map[string]*session.Summary{}
	}
	stored.Summaries[filterKey] = &session.Summary{
		// Match the deterministic text produced by the in-memory backend's
		// fakeSummarizer so cross-backend comparison stays diff-free.
		Summary:   "replay-test summary",
		UpdatedAt: time.Now(),
	}
	return saveStore(s.path, st)
}

func (s *persistentSessionService) AppendTrackEvent(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := loadStore(s.path)
	if err != nil {
		return err
	}
	stored := st.Sessions[sessKey(session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID})]
	if stored == nil {
		return fmt.Errorf("session not found: %s", sess.ID)
	}
	if stored.Tracks == nil {
		stored.Tracks = map[session.Track]*session.TrackEvents{}
	}
	te := stored.Tracks[evt.Track]
	if te == nil {
		te = &session.TrackEvents{Track: evt.Track}
		stored.Tracks[evt.Track] = te
	}
	te.Events = append(te.Events, *evt)
	return saveStore(s.path, st)
}

// The remaining session.Service methods are not exercised by the replay
// cases; they exist to satisfy the interface.
func (s *persistentSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return nil
}
func (s *persistentSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return nil
}
func (s *persistentSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return nil, nil
}
func (s *persistentSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return nil
}
func (s *persistentSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return nil, nil
}
func (s *persistentSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return nil
}
func (s *persistentSessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := loadStore(s.path)
	if err != nil {
		return err
	}
	stored := st.Sessions[sessKey(key)]
	if stored == nil {
		return fmt.Errorf("session not found: %s", key.SessionID)
	}
	stored.State = state
	return saveStore(s.path, st)
}
func (s *persistentSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}
func (s *persistentSessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, _ := loadStore(s.path)
	stored := st.Sessions[sessKey(session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID})]
	if stored == nil || len(stored.Summaries) == 0 {
		return "", false
	}
	for _, sum := range stored.Summaries {
		return sum.Summary, true
	}
	return "", false
}
func (s *persistentSessionService) Close() error { return nil }

var _ session.Service = (*persistentSessionService)(nil)
var _ session.TrackService = (*persistentSessionService)(nil)

// ─────────────────────────────────────────────────────────────────────
// persistentMemoryService — memory.Service
// ─────────────────────────────────────────────────────────────────────

type persistentMemoryService struct {
	mu   sync.Mutex
	path string
}

func newPersistentMemoryService(dir string) *persistentMemoryService {
	return &persistentMemoryService{path: filepath.Join(dir, "memories.json")}
}

func (m *persistentMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, mem string, topics []string, opts ...memory.AddOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := loadStore(m.path)
	if err != nil {
		return err
	}
	now := time.Now()
	entry := &memory.Entry{
		ID:        fmt.Sprintf("mem-%d", len(st.Memories[memKey(userKey)])),
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    &memory.Memory{Memory: mem, Topics: topics, LastUpdated: &now},
		CreatedAt: now,
		UpdatedAt: now,
	}
	k := memKey(userKey)
	st.Memories[k] = append(st.Memories[k], entry)
	return saveStore(m.path, st)
}

func (m *persistentMemoryService) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := loadStore(m.path)
	if err != nil {
		return nil, err
	}
	entries := st.Memories[memKey(userKey)]
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (m *persistentMemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, query string, opts ...memory.SearchOption) ([]*memory.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := loadStore(m.path)
	if err != nil {
		return nil, err
	}
	entries := st.Memories[memKey(userKey)]
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return entries, nil
	}
	var out []*memory.Entry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Memory.Memory), q) ||
			strings.Contains(strings.ToLower(strings.Join(e.Memory.Topics, " ")), q) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *persistentMemoryService) UpdateMemory(ctx context.Context, memoryKey memory.Key, mem string, topics []string, opts ...memory.UpdateOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := loadStore(m.path)
	if err != nil {
		return err
	}
	entries := st.Memories[memoryKey.AppName+"|"+memoryKey.UserID]
	for _, e := range entries {
		if e.ID == memoryKey.MemoryID {
			now := time.Now()
			e.Memory.Memory = mem
			e.Memory.Topics = topics
			e.UpdatedAt = now
			break
		}
	}
	return saveStore(m.path, st)
}

func (m *persistentMemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := loadStore(m.path)
	if err != nil {
		return err
	}
	k := memoryKey.AppName + "|" + memoryKey.UserID
	entries := st.Memories[k]
	out := entries[:0]
	for _, e := range entries {
		if e.ID != memoryKey.MemoryID {
			out = append(out, e)
		}
	}
	st.Memories[k] = out
	return saveStore(m.path, st)
}

func (m *persistentMemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := loadStore(m.path)
	if err != nil {
		return err
	}
	delete(st.Memories, memKey(userKey))
	return saveStore(m.path, st)
}

func (m *persistentMemoryService) Tools() []tool.Tool { return nil }
func (m *persistentMemoryService) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	return nil
}
func (m *persistentMemoryService) Close() error { return nil }

var _ memory.Service = (*persistentMemoryService)(nil)

// newPersistentBackend builds a file-persisted backend rooted at dir.
// A fresh call with the same dir observes data written by previous
// instances, demonstrating persistence.
func newPersistentBackend(dir, name string) Backend {
	return Backend{
		Name:           name,
		SessionService: newPersistentSessionService(dir),
		MemoryService:  newPersistentMemoryService(dir),
	}
}
