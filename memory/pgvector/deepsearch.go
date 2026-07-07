//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

func (s *Service) deepSearchEnabled() bool {
	return s.opts.deepSearchModel != nil
}

// EnsureIndex ensures row-attached DeepSearch indexes for one user.
func (s *Service) EnsureIndex(ctx context.Context, userKey memory.UserKey) error {
	if !s.deepSearchEnabled() {
		return errors.New("deepsearch is not enabled")
	}
	rows, err := s.deepSearchRows(ctx, userKey)
	if err != nil {
		return err
	}
	if deepsearch.RowsCurrent(rows) {
		return nil
	}
	entries := make([]*memory.Entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, row.Entry)
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
	now := time.Now()
	indexes := make(map[string]*deepsearch.Index, len(documents))
	for _, document := range documents {
		indexes[document.ID] = deepsearch.NewIndex(document, now)
	}
	return s.writeDeepSearchIndexes(ctx, userKey, indexes)
}

func (s *Service) deepSearchRows(
	ctx context.Context,
	userKey memory.UserKey,
) ([]deepsearch.EntryRow, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(
		"SELECT memory_id, app_name, user_id, memory_content, topics, "+
			"memory_kind, event_time, participants, location, created_at, updated_at, "+
			"deepsearch_index FROM %s WHERE app_name = $1 AND user_id = $2",
		s.tableName,
	)
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}
	rows := make([]deepsearch.EntryRow, 0)
	err := s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			entry, index, err := scanDeepSearchRow(sqlRows)
			if err != nil {
				return err
			}
			rows = append(rows, deepsearch.EntryRow{
				Entry: entry,
				Index: index,
			})
		}
		return nil
	}, query, userKey.AppName, userKey.UserID)
	if err != nil {
		return nil, fmt.Errorf("read deepsearch rows: %w", err)
	}
	return rows, nil
}

func scanDeepSearchRow(rows *sql.Rows) (*memory.Entry, *deepsearch.Index, error) {
	var rawIndex []byte
	entry, err := scanMemoryEntryWithExtra(rows, &rawIndex)
	if err != nil {
		return nil, nil, err
	}
	if len(rawIndex) == 0 {
		return entry, nil, nil
	}
	var index deepsearch.Index
	if err := json.Unmarshal(rawIndex, &index); err != nil {
		return nil, nil, fmt.Errorf("unmarshal deepsearch index: %w", err)
	}
	return entry, &index, nil
}

func scanMemoryEntryWithExtra(rows *sql.Rows, extra ...any) (*memory.Entry, error) {
	var (
		id           string
		appName      string
		userID       string
		memoryStr    string
		topics       []string
		kind         string
		eventTime    sql.NullTime
		participants []string
		location     sql.NullString
		createdAt    time.Time
		updatedAt    time.Time
	)
	dest := []any{
		&id,
		&appName,
		&userID,
		&memoryStr,
		pq.Array(&topics),
		&kind,
		&eventTime,
		pq.Array(&participants),
		&location,
		&createdAt,
		&updatedAt,
	}
	dest = append(dest, extra...)
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	mem := &memory.Memory{
		Memory:       memoryStr,
		Topics:       topics,
		LastUpdated:  &updatedAt,
		Kind:         memory.Kind(kind),
		Participants: participants,
	}
	if eventTime.Valid {
		mem.EventTime = &eventTime.Time
	}
	if location.Valid {
		mem.Location = location.String
	}
	return &memory.Entry{
		ID:        id,
		AppName:   appName,
		UserID:    userID,
		Memory:    mem,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func (s *Service) writeDeepSearchIndexes(
	ctx context.Context,
	userKey memory.UserKey,
	indexes map[string]*deepsearch.Index,
) error {
	for memoryID, index := range indexes {
		raw, err := json.Marshal(index)
		if err != nil {
			return fmt.Errorf("marshal deepsearch index: %w", err)
		}
		query := fmt.Sprintf(
			"UPDATE %s SET deepsearch_index = $1, deepsearch_text = $2, "+
				"deepsearch_fingerprint = $3, deepsearch_version = $4, deepsearch_indexed_at = $5 "+
				"WHERE memory_id = $6 AND app_name = $7 AND user_id = $8",
			s.tableName,
		)
		if s.opts.softDelete {
			query += " AND deleted_at IS NULL"
		}
		if _, err := s.db.ExecContext(
			ctx,
			query,
			raw,
			deepsearch.IndexText(index),
			index.SourceFingerprint,
			index.Version,
			index.IndexedAt,
			memoryID,
			userKey.AppName,
			userKey.UserID,
		); err != nil {
			return fmt.Errorf("write deepsearch index for %s: %w", memoryID, err)
		}
	}
	return nil
}

// SearchCues searches row-attached DeepSearch cues.
func (s *Service) SearchCues(
	ctx context.Context,
	req deepsearch.CueSearchRequest,
) (*deepsearch.CueSearchResult, error) {
	if err := s.EnsureIndex(ctx, req.UserKey); err != nil {
		return nil, err
	}
	rows, err := s.deepSearchRows(ctx, req.UserKey)
	if err != nil {
		return nil, err
	}
	return deepsearch.SearchCues(rows, req), nil
}

// ExpandTags expands row-attached DeepSearch tags.
func (s *Service) ExpandTags(
	ctx context.Context,
	req deepsearch.TagExpandRequest,
) (*deepsearch.TagExpandResult, error) {
	if err := s.EnsureIndex(ctx, req.UserKey); err != nil {
		return nil, err
	}
	rows, err := s.deepSearchRows(ctx, req.UserKey)
	if err != nil {
		return nil, err
	}
	return deepsearch.ExpandTags(rows, req), nil
}

// LoadContents loads row-attached DeepSearch content.
func (s *Service) LoadContents(
	ctx context.Context,
	req deepsearch.ContentLoadRequest,
) (*deepsearch.ContentLoadResult, error) {
	if err := s.EnsureIndex(ctx, req.UserKey); err != nil {
		return nil, err
	}
	rows, err := s.deepSearchRows(ctx, req.UserKey)
	if err != nil {
		return nil, err
	}
	return deepsearch.LoadContents(rows, req), nil
}
