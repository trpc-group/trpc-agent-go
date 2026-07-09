//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gormmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/gorm"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the GORM-backed memory service.
// Storage structure matches memory/postgres:
//
//	Table: memories (configurable)
//	Columns: memory_id, app_name, user_id, memory_data (JSON), created_at, updated_at, deleted_at.
//	Primary Key: memory_id.
//	Index: (app_name, user_id).
type Service struct {
	db        *gorm.DB
	dbClient  storage.Client
	tableName string
	opts      ServiceOpts

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new GORM memory service.
// Provide a database via WithDB (shared injection), WithDialector, or WithGormInstance.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	for _, option := range options {
		option(&opts)
	}

	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	db, dbClient, err := resolveGormDB(opts)
	if err != nil {
		return nil, err
	}

	s := &Service{
		db:          db,
		dbClient:    dbClient,
		tableName:   opts.tableName,
		opts:        opts,
		cachedTools: make(map[string]tool.Tool),
	}

	if !opts.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDBInitTimeout)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			if s.dbClient != nil {
				_ = s.dbClient.Close()
			}
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}

	s.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		s.cachedTools,
	)

	if opts.extractor != nil {
		imemory.ConfigureExtractorEnabledTools(opts.extractor, opts.enabledTools)
		config := imemory.AutoMemoryConfig{
			Extractor:        opts.extractor,
			AsyncMemoryNum:   opts.asyncMemoryNum,
			MemoryQueueSize:  opts.memoryQueueSize,
			MemoryJobTimeout: opts.memoryJobTimeout,
			EnabledTools:     opts.enabledTools,
		}
		s.autoMemoryWorker = imemory.NewAutoMemoryWorker(config, s)
		s.autoMemoryWorker.Start()
	}

	return s, nil
}

// AddMemory adds or updates a memory for a user (idempotent).
func (s *Service) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string,
	topics []string, opts ...memory.AddOption) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	ep := memory.ResolveAddOptions(opts)
	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	imemory.ApplyMetadata(mem, ep)
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)

	if s.opts.memoryLimit > 0 {
		var existing int64
		err := s.memoryTable(ctx).
			Where(
				"memory_id = ? AND app_name = ? AND user_id = ?",
				memoryID, userKey.AppName, userKey.UserID,
			).
			Count(&existing).Error
		if err != nil {
			return wrapDBErr("check existing memory", err)
		}
		if existing == 0 {
			if err := s.enforceMemoryLimit(ctx, userKey); err != nil {
				return err
			}
		}
	}

	entry := &memory.Entry{
		ID:        memoryID,
		AppName:   userKey.AppName,
		Memory:    mem,
		UserID:    userKey.UserID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	memoryData, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal memory entry failed: %w", err)
	}

	row := memoryRow{
		MemoryID:   entry.ID,
		AppName:    userKey.AppName,
		UserID:     userKey.UserID,
		MemoryData: datatypes.JSON(memoryData),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	conflictUpdates := map[string]any{
		"memory_data": row.MemoryData,
		"updated_at":  row.UpdatedAt,
	}
	if s.opts.softDelete {
		conflictUpdates["deleted_at"] = nil
	}

	conflict := clause.OnConflict{
		Columns:   []clause.Column{{Name: "memory_id"}},
		DoUpdates: clause.Assignments(conflictUpdates),
	}
	createQuery := s.memoryTable(ctx).Clauses(conflict)
	if s.opts.softDelete {
		err = createQuery.Create(&row).Error
	} else {
		err = createQuery.Select(
			"memory_id", "app_name", "user_id", "memory_data", "created_at", "updated_at",
		).Create(&row).Error
	}
	if err != nil {
		return wrapDBErr("store memory entry", err)
	}
	return nil
}

func (s *Service) enforceMemoryLimit(ctx context.Context, userKey memory.UserKey) error {
	if s.opts.memoryLimit <= 0 {
		return nil
	}

	var count int64
	err := s.memoryTable(ctx).
		Where("app_name = ? AND user_id = ?", userKey.AppName, userKey.UserID).
		Count(&count).Error
	if err != nil {
		return wrapDBErr("check memory count", err)
	}
	if int(count) >= s.opts.memoryLimit {
		return fmt.Errorf(
			"memory limit exceeded for user %s, limit: %d, current: %d",
			userKey.UserID, s.opts.memoryLimit, count)
	}
	return nil
}

// UpdateMemory updates an existing memory for a user.
func (s *Service) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string,
	topics []string, opts ...memory.UpdateOption) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	var row memoryRow
	err := s.memoryTable(ctx).
		Where("memory_id = ? AND app_name = ? AND user_id = ?",
			memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}
		return wrapDBErr("get memory entry", err)
	}

	entry := &memory.Entry{}
	if err := json.Unmarshal(row.MemoryData, entry); err != nil {
		return fmt.Errorf("unmarshal memory entry failed: %w", err)
	}
	imemory.NormalizeEntry(entry)

	now := time.Now()
	ep := memory.ResolveUpdateOptions(opts)
	newID := imemory.ApplyMemoryUpdate(
		entry,
		memoryKey.AppName,
		memoryKey.UserID,
		memoryStr,
		topics,
		ep,
		now,
	)

	updated, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal updated memory entry failed: %w", err)
	}

	result := s.memoryTable(ctx).
		Where("memory_id = ? AND app_name = ? AND user_id = ?",
			memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID).
		Updates(map[string]any{
			"memory_id":   newID,
			"memory_data": updated,
			"updated_at":  now,
		})
	if result.Error != nil {
		return wrapDBErr("update memory entry", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	if updateResult := memory.ResolveUpdateResult(opts); updateResult != nil {
		updateResult.MemoryID = newID
	}
	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	q := s.memoryTable(ctx).
		Where("memory_id = ? AND app_name = ? AND user_id = ?",
			memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID)

	var err error
	if s.opts.softDelete {
		err = q.Delete(&memoryRow{}).Error
	} else {
		err = q.Unscoped().Delete(&memoryRow{}).Error
	}
	if err != nil {
		return wrapDBErr("delete memory entry", err)
	}
	return nil
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	q := s.memoryTable(ctx).
		Where("app_name = ? AND user_id = ?", userKey.AppName, userKey.UserID)

	var err error
	if s.opts.softDelete {
		err = q.Delete(&memoryRow{}).Error
	} else {
		err = q.Unscoped().Delete(&memoryRow{}).Error
	}
	if err != nil {
		return wrapDBErr("clear memories", err)
	}
	return nil
}

// ReadMemories reads memories for a user.
func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	q := s.memoryTable(ctx).
		Where("app_name = ? AND user_id = ?", userKey.AppName, userKey.UserID).
		Order("updated_at DESC, created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}

	var rows []memoryRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, wrapDBErr("list memories", err)
	}
	return rowsToEntries(rows)
}

// SearchMemories searches memories for a user.
func (s *Service) SearchMemories(ctx context.Context, userKey memory.UserKey,
	query string, opts ...memory.SearchOption) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	var rows []memoryRow
	if err := s.memoryTable(ctx).
		Where("app_name = ? AND user_id = ?", userKey.AppName, userKey.UserID).
		Find(&rows).Error; err != nil {
		return nil, wrapDBErr("search memories", err)
	}

	entries, err := rowsToEntries(rows)
	if err != nil {
		return nil, err
	}

	return imemory.SearchEntries(
		entries,
		memory.ResolveSearchOptions(query, opts),
		s.opts.searchMinScore,
		s.opts.maxSearchResults,
	), nil
}

// Tools returns the list of available memory tools.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async processing.
func (s *Service) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close stops async workers and closes owned GORM connections.
// Injected handles (WithDB) are not closed.
func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	if s.dbClient != nil {
		return s.dbClient.Close()
	}
	return nil
}

func resolveGormDB(opts ServiceOpts) (*gorm.DB, storage.Client, error) {
	if opts.db != nil {
		return opts.db, nil, nil
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithExtraOptions(opts.extraOptions...),
	}
	if opts.dialector != nil {
		builderOpts = append(builderOpts, storage.WithDialector(opts.dialector))
	} else if opts.instanceName != "" {
		var ok bool
		builderOpts, ok = storage.GetGormInstance(opts.instanceName)
		if !ok {
			return nil, nil, fmt.Errorf("gorm instance %s not found", opts.instanceName)
		}
		builderOpts = append(builderOpts, storage.WithExtraOptions(opts.extraOptions...))
	} else {
		return nil, nil, fmt.Errorf("gorm memory service requires WithDB, WithDialector, or WithGormInstance")
	}

	client, err := storage.GetClientBuilder()(context.Background(), builderOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create gorm client failed: %w", err)
	}
	return client.DB(), client, nil
}

func rowsToEntries(rows []memoryRow) ([]*memory.Entry, error) {
	entries := make([]*memory.Entry, 0, len(rows))
	for _, row := range rows {
		e := &memory.Entry{}
		if err := json.Unmarshal(row.MemoryData, e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry failed: %w", err)
		}
		imemory.NormalizeEntry(e)
		entries = append(entries, e)
	}
	return entries, nil
}
