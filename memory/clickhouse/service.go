//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service stores user memories in a ClickHouse ReplacingMergeTree table.
// Mutations are persisted as newer rows and reads use FINAL, so callers see
// the active version without waiting for a background merge.
type Service struct {
	opts      serviceOpts
	client    storage.Client
	tableName string

	mu          sync.Mutex
	lastWriteAt time.Time
	closeOnce   sync.Once
	cachedTools map[string]tool.Tool
	tools       []tool.Tool
}

// NewService creates a ClickHouse memory service. A DSN or a registered
// ClickHouse instance is required.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderDSN(opts.dsn),
		storage.WithExtraOptions(opts.extraOptions...),
	}
	if opts.dsn == "" && opts.instanceName != "" {
		var ok bool
		builderOpts, ok = storage.GetClickHouseInstance(opts.instanceName)
		if !ok {
			return nil, fmt.Errorf("clickhouse instance %q not found", opts.instanceName)
		}
	}

	client, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create clickhouse client: %w", err)
	}

	s := &Service{
		opts:        opts,
		client:      client,
		tableName:   opts.tableName,
		cachedTools: make(map[string]tool.Tool),
	}
	if !opts.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("initialize clickhouse memory schema: %w", err)
		}
	}
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer seedCancel()
	if err := s.seedLastWriteAt(seedCtx); err != nil {
		_ = client.Close()
		return nil, err
	}
	s.tools = imemory.BuildToolsList(nil, imemory.AllToolCreators,
		imemory.DefaultEnabledTools, nil, nil, s.cachedTools)
	return s, nil
}

// AddMemory adds or replaces a deterministic memory identity for a user.
func (s *Service) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nextWriteAt()
	mem := &memory.Memory{Memory: content, Topics: slices.Clone(topics), LastUpdated: &now}
	imemory.ApplyMetadata(mem, memory.ResolveAddOptions(opts))
	entry := &memory.Entry{
		ID:        imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID),
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    mem,
		CreatedAt: now,
		UpdatedAt: now,
	}

	entries, err := s.readEntries(ctx, userKey, 0)
	if err != nil {
		return err
	}
	if s.opts.memoryLimit > 0 && len(entries) >= s.opts.memoryLimit {
		for _, existing := range entries {
			if existing.ID == entry.ID {
				return s.writeEntry(ctx, entry, nil)
			}
		}
		return fmt.Errorf("memory limit exceeded for user %s, limit: %d, current: %d",
			userKey.UserID, s.opts.memoryLimit, len(entries))
	}
	return s.writeEntry(ctx, entry, nil)
}

// UpdateMemory updates an active memory. If metadata or content rotates its
// canonical identity, the old identity is replaced with a tombstone.
func (s *Service) UpdateMemory(
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

	entry, ok, err := s.findEntry(ctx, memoryKey)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	now := s.nextWriteAt()
	newID := imemory.ApplyMemoryUpdate(entry, memoryKey.AppName, memoryKey.UserID,
		content, topics, memory.ResolveUpdateOptions(opts), now)
	if newID != memoryKey.MemoryID {
		_, targetExists, err := s.findEntry(ctx, memory.Key{
			AppName: memoryKey.AppName, UserID: memoryKey.UserID, MemoryID: newID,
		})
		if err != nil {
			return err
		}
		if targetExists {
			return fmt.Errorf("memory with id %s already exists", newID)
		}
	}

	if err := s.writeEntry(ctx, entry, nil); err != nil {
		return err
	}
	if newID != memoryKey.MemoryID {
		tombstone := *entry
		tombstone.ID = memoryKey.MemoryID
		tombstone.UpdatedAt = s.nextWriteAt()
		if err := s.writeEntry(ctx, &tombstone, &tombstone.UpdatedAt); err != nil {
			return fmt.Errorf("tombstone rotated memory: %w", err)
		}
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}
	return nil
}

// DeleteMemory removes a memory by writing a newer tombstone version.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok, err := s.findEntry(ctx, memoryKey)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}
	now := s.nextWriteAt()
	entry.UpdatedAt = now
	return s.writeEntry(ctx, entry, &now)
}

// ClearMemories removes every active memory for a user by writing tombstones.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readEntries(ctx, userKey, 0)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		now := s.nextWriteAt()
		entry.UpdatedAt = now
		if err := s.writeEntry(ctx, entry, &now); err != nil {
			return fmt.Errorf("clear memory %s: %w", entry.ID, err)
		}
	}
	return nil
}

// ReadMemories reads active memories in descending update order.
func (s *Service) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	return s.readEntries(ctx, userKey, limit)
}

// SearchMemories performs deterministic keyword search over active memories.
func (s *Service) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	entries, err := s.ReadMemories(ctx, userKey, 0)
	if err != nil {
		return nil, err
	}
	return imemory.SearchEntries(entries, memory.ResolveSearchOptions(query, opts),
		s.opts.searchMinScore, s.opts.maxSearchResults), nil
}

// Tools returns the default memory tools in a stable order.
func (s *Service) Tools() []tool.Tool { return slices.Clone(s.tools) }

// EnqueueAutoMemoryJob is a no-op because this service does not configure an
// auto-memory extractor.
func (s *Service) EnqueueAutoMemoryJob(context.Context, *session.Session) error { return nil }

// Close closes the ClickHouse client. It is safe to call multiple times.
func (s *Service) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.client != nil {
			err = s.client.Close()
		}
	})
	return err
}

func (s *Service) readEntries(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	query := fmt.Sprintf(`SELECT memory_data FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND deleted_at IS NULL
		ORDER BY updated_at DESC, memory_id ASC`, s.tableName)
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.client.Query(ctx, query, userKey.AppName, userKey.UserID)
	if err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}
	defer rows.Close()

	entries := make([]*memory.Entry, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		entry := &memory.Entry{}
		if err := json.Unmarshal([]byte(data), entry); err != nil {
			return nil, fmt.Errorf("decode memory: %w", err)
		}
		imemory.NormalizeEntry(entry)
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Service) findEntry(ctx context.Context, key memory.Key) (*memory.Entry, bool, error) {
	rows, err := s.client.Query(ctx, fmt.Sprintf(`SELECT memory_data FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND memory_id = ? AND deleted_at IS NULL`, s.tableName),
		key.AppName, key.UserID, key.MemoryID)
	if err != nil {
		return nil, false, fmt.Errorf("find memory: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, false, nil
	}
	var data string
	if err := rows.Scan(&data); err != nil {
		return nil, false, fmt.Errorf("scan memory: %w", err)
	}
	entry := &memory.Entry{}
	if err := json.Unmarshal([]byte(data), entry); err != nil {
		return nil, false, fmt.Errorf("decode memory: %w", err)
	}
	imemory.NormalizeEntry(entry)
	return entry, true, nil
}

func (s *Service) writeEntry(ctx context.Context, entry *memory.Entry, deletedAt *time.Time) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode memory: %w", err)
	}
	if deletedAt == nil {
		err = s.client.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
			(app_name, user_id, memory_id, memory_data, created_at, updated_at)
			VALUES (?, ?, ?, ?, fromUnixTimestamp64Micro(?), fromUnixTimestamp64Micro(?))`, s.tableName),
			entry.AppName, entry.UserID, entry.ID, string(data), entry.CreatedAt.UnixMicro(), entry.UpdatedAt.UnixMicro())
	} else {
		err = s.client.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
			(app_name, user_id, memory_id, memory_data, created_at, updated_at, deleted_at)
			VALUES (?, ?, ?, ?, fromUnixTimestamp64Micro(?), fromUnixTimestamp64Micro(?), fromUnixTimestamp64Micro(?))`, s.tableName),
			entry.AppName, entry.UserID, entry.ID, string(data), entry.CreatedAt.UnixMicro(), entry.UpdatedAt.UnixMicro(), deletedAt.UnixMicro())
	}
	if err != nil {
		return fmt.Errorf("write memory: %w", err)
	}
	return nil
}

func (s *Service) nextWriteAt() time.Time {
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(s.lastWriteAt) {
		now = s.lastWriteAt.Add(time.Microsecond)
	}
	s.lastWriteAt = now
	return now
}

func (s *Service) seedLastWriteAt(ctx context.Context) error {
	var updatedAtMicro int64
	query := fmt.Sprintf(
		"SELECT coalesce(max(toUnixTimestamp64Micro(updated_at)), 0) FROM %s FINAL",
		s.tableName,
	)
	if err := s.client.QueryRow(ctx, []any{&updatedAtMicro}, query); err != nil {
		return fmt.Errorf("read latest clickhouse memory version: %w", err)
	}
	if updatedAtMicro > 0 {
		s.lastWriteAt = time.UnixMicro(updatedAtMicro).UTC()
	}
	return nil
}
