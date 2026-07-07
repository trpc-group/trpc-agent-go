//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

func (s *MemoryService) deepSearchEnabled() bool {
	return s.opts.deepSearchModel != nil
}

// EnsureIndex ensures row-attached DeepSearch indexes for one user.
func (s *MemoryService) EnsureIndex(ctx context.Context, userKey memory.UserKey) error {
	if !s.deepSearchEnabled() {
		return errors.New("deepsearch is not enabled")
	}
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	app := s.getAppMemories(userKey.AppName)
	entries, current := s.deepSearchSnapshot(app, userKey.UserID)
	if current {
		return nil
	}
	documents, err := deepsearch.BuildDocuments(
		ctx,
		s.opts.deepSearchModel,
		entries,
		s.opts.deepSearchOptions...,
	)
	if err != nil {
		return fmt.Errorf("build deepsearch documents: %w", err)
	}
	indexes := make(map[string]*deepsearch.Index, len(documents))
	now := time.Now()
	for _, document := range documents {
		indexes[document.ID] = deepsearch.NewIndex(document, now)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.memories[userKey.UserID] == nil {
		delete(app.deepSearch, userKey.UserID)
		return nil
	}
	app.deepSearch[userKey.UserID] = indexes
	return nil
}

func (s *MemoryService) deepSearchSnapshot(
	app *appMemories,
	userID string,
) ([]*memory.Entry, bool) {
	app.mu.RLock()
	defer app.mu.RUnlock()
	userMemories := app.memories[userID]
	if len(userMemories) == 0 {
		return nil, true
	}
	indexes := app.deepSearch[userID]
	entries := make([]*memory.Entry, 0, len(userMemories))
	current := len(indexes) == len(userMemories)
	for _, entry := range userMemories {
		entries = append(entries, entry)
		if !deepsearch.IsCurrent(entry, indexes[entry.ID]) {
			current = false
		}
	}
	return entries, current
}

func (s *MemoryService) deepSearchRows(
	ctx context.Context,
	userKey memory.UserKey,
) ([]deepsearch.EntryRow, error) {
	if err := s.EnsureIndex(ctx, userKey); err != nil {
		return nil, err
	}
	app := s.getAppMemories(userKey.AppName)
	app.mu.RLock()
	defer app.mu.RUnlock()
	userMemories := app.memories[userKey.UserID]
	indexes := app.deepSearch[userKey.UserID]
	rows := make([]deepsearch.EntryRow, 0, len(userMemories))
	for _, entry := range userMemories {
		rows = append(rows, deepsearch.EntryRow{
			Entry: entry,
			Index: indexes[entry.ID],
		})
	}
	return rows, nil
}

// SearchCues searches row-attached DeepSearch cues.
func (s *MemoryService) SearchCues(
	ctx context.Context,
	req deepsearch.CueSearchRequest,
) (*deepsearch.CueSearchResult, error) {
	rows, err := s.deepSearchRows(ctx, req.UserKey)
	if err != nil {
		return nil, err
	}
	return deepsearch.SearchCues(rows, req), nil
}

// ExpandTags expands row-attached DeepSearch tags.
func (s *MemoryService) ExpandTags(
	ctx context.Context,
	req deepsearch.TagExpandRequest,
) (*deepsearch.TagExpandResult, error) {
	rows, err := s.deepSearchRows(ctx, req.UserKey)
	if err != nil {
		return nil, err
	}
	return deepsearch.ExpandTags(rows, req), nil
}

// LoadContents loads row-attached DeepSearch content.
func (s *MemoryService) LoadContents(
	ctx context.Context,
	req deepsearch.ContentLoadRequest,
) (*deepsearch.ContentLoadResult, error) {
	rows, err := s.deepSearchRows(ctx, req.UserKey)
	if err != nil {
		return nil, err
	}
	return deepsearch.LoadContents(rows, req), nil
}
