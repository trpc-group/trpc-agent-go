//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package customservice demonstrates a minimal out-of-tree memory.Service
// implementation that uses memory/memoryutils for canonical ID and metadata
// handling instead of importing memory/internal/memory.
package customservice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/memoryutils"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*MapService)(nil)

// MapService is a minimal in-memory memory.Service backed by nested maps.
// It exists to show how external persistence adapters should call memoryutils
// on add/update/read paths while owning only storage layout and locking.
type MapService struct {
	mu    sync.RWMutex
	users map[string]map[string]map[string]*memory.Entry // app -> user -> id -> entry
}

// NewMapService creates an empty MapService.
func NewMapService() *MapService {
	return &MapService{
		users: make(map[string]map[string]map[string]*memory.Entry),
	}
}

// AddMemory adds or upserts a memory using memoryutils for metadata and ID generation.
func (s *MapService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	now := time.Now()
	memObj := &memory.Memory{
		Memory:      content,
		Topics:      topics,
		LastUpdated: &now,
	}
	memoryutils.ApplyMetadata(memObj, memory.ResolveAddOptions(opts))
	id := memoryutils.GenerateMemoryID(memObj, userKey.AppName, userKey.UserID)

	entry := &memory.Entry{
		ID:        id,
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    memObj,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.putEntry(entry)
	return nil
}

// UpdateMemory updates a memory entry and rotates the storage key when identity changes.
func (s *MapService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	content string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.getEntry(memoryKey)
	if !ok {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	now := time.Now()
	ep := memory.ResolveUpdateOptions(opts)
	newID := memoryutils.ApplyMemoryUpdate(
		entry,
		memoryKey.AppName,
		memoryKey.UserID,
		content,
		topics,
		ep,
		now,
	)
	if newID != memoryKey.MemoryID {
		if _, conflict := s.getEntry(memory.Key{
			AppName:  memoryKey.AppName,
			UserID:   memoryKey.UserID,
			MemoryID: newID,
		}); conflict {
			return fmt.Errorf("memory with id %s already exists", newID)
		}
		s.deleteEntry(memoryKey)
	}
	s.putEntry(entry)
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}
	return nil
}

// DeleteMemory removes one memory row.
func (s *MapService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.getEntry(memoryKey); !ok {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}
	s.deleteEntry(memoryKey)
	return nil
}

// ClearMemories removes all memories for a user.
func (s *MapService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	byUser, ok := s.users[userKey.AppName]
	if !ok {
		return nil
	}
	delete(byUser, userKey.UserID)
	return nil
}

// ReadMemories returns recent memories for a user up to limit.
func (s *MapService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := s.listEntries(userKey.AppName, userKey.UserID)
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// SearchMemories performs a simple substring search with optional kind filtering.
func (s *MapService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	so := memory.ResolveSearchOptions(query, opts)
	q := strings.TrimSpace(strings.ToLower(so.Query))
	if q == "" {
		return []*memory.Entry{}, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*memory.Entry
	for _, entry := range s.listEntries(userKey.AppName, userKey.UserID) {
		if entry == nil || entry.Memory == nil {
			continue
		}
		if so.Kind != "" && memoryutils.EffectiveKind(entry.Memory) != so.Kind {
			continue
		}
		if !strings.Contains(strings.ToLower(entry.Memory.Memory), q) {
			continue
		}
		out = append(out, cloneEntry(entry))
	}
	return out, nil
}

// Tools returns no tools; this example focuses on the storage adapter only.
func (s *MapService) Tools() []tool.Tool {
	return nil
}

// EnqueueAutoMemoryJob is a no-op for this minimal adapter.
func (s *MapService) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	return nil
}

// Close is a no-op for this in-memory adapter.
func (s *MapService) Close() error {
	return nil
}

func (s *MapService) putEntry(entry *memory.Entry) {
	byUser := s.users[entry.AppName]
	if byUser == nil {
		byUser = make(map[string]map[string]*memory.Entry)
		s.users[entry.AppName] = byUser
	}
	byID := byUser[entry.UserID]
	if byID == nil {
		byID = make(map[string]*memory.Entry)
		byUser[entry.UserID] = byID
	}
	byID[entry.ID] = cloneEntry(entry)
}

func (s *MapService) getEntry(key memory.Key) (*memory.Entry, bool) {
	byUser, ok := s.users[key.AppName]
	if !ok {
		return nil, false
	}
	byID, ok := byUser[key.UserID]
	if !ok {
		return nil, false
	}
	entry, ok := byID[key.MemoryID]
	if !ok {
		return nil, false
	}
	return cloneEntry(entry), true
}

func (s *MapService) deleteEntry(key memory.Key) {
	byUser, ok := s.users[key.AppName]
	if !ok {
		return
	}
	byID, ok := byUser[key.UserID]
	if !ok {
		return
	}
	delete(byID, key.MemoryID)
}

func (s *MapService) listEntries(appName, userID string) []*memory.Entry {
	byUser, ok := s.users[appName]
	if !ok {
		return nil
	}
	byID, ok := byUser[userID]
	if !ok {
		return nil
	}
	out := make([]*memory.Entry, 0, len(byID))
	for _, entry := range byID {
		out = append(out, cloneEntry(entry))
	}
	return out
}

func cloneEntry(entry *memory.Entry) *memory.Entry {
	if entry == nil {
		return nil
	}
	cp := *entry
	if entry.Memory != nil {
		mem := *entry.Memory
		memoryutils.NormalizeMemory(&mem)
		cp.Memory = &mem
	}
	return &cp
}
