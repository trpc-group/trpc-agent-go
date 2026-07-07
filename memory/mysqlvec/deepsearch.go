//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqlvec

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
			"memory_kind, event_time, participants, location, "+
			"created_at, updated_at, deepsearch_index FROM %s "+
			"WHERE app_name = ? AND user_id = ?",
		s.tableName,
	)
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}
	rows := make([]deepsearch.EntryRow, 0)
	err := s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		entry, index, err := scanDeepSearchRow(sqlRows)
		if err != nil {
			return err
		}
		rows = append(rows, deepsearch.EntryRow{
			Entry: entry,
			Index: index,
		})
		return nil
	}, query, userKey.AppName, userKey.UserID)
	if err != nil {
		return nil, fmt.Errorf("read deepsearch rows: %w", err)
	}
	return rows, nil
}

func scanDeepSearchRow(rows *sql.Rows) (*memory.Entry, *deepsearch.Index, error) {
	var (
		memoryID         string
		appName          string
		userID           string
		memoryContent    string
		topicsJSON       sql.NullString
		memoryKind       string
		eventTime        sql.NullTime
		participantsJSON sql.NullString
		location         sql.NullString
		createdAt        sql.NullTime
		updatedAt        sql.NullTime
		rawIndex         []byte
	)
	if err := rows.Scan(
		&memoryID, &appName, &userID, &memoryContent,
		&topicsJSON, &memoryKind, &eventTime,
		&participantsJSON, &location,
		&createdAt, &updatedAt, &rawIndex,
	); err != nil {
		return nil, nil, fmt.Errorf("scan deepsearch row: %w", err)
	}
	entry := buildEntry(memoryID, appName, userID, memoryContent,
		topicsJSON, memoryKind, eventTime, participantsJSON,
		location, createdAt, updatedAt)
	if len(rawIndex) == 0 {
		return entry, nil, nil
	}
	var index deepsearch.Index
	if err := json.Unmarshal(rawIndex, &index); err != nil {
		return nil, nil, fmt.Errorf("unmarshal deepsearch index: %w", err)
	}
	return entry, &index, nil
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
			"UPDATE %s SET deepsearch_index = ?, deepsearch_text = ?, "+
				"deepsearch_fingerprint = ?, deepsearch_version = ?, deepsearch_indexed_at = ? "+
				"WHERE memory_id = ? AND app_name = ? AND user_id = ?",
			s.tableName,
		)
		if s.opts.softDelete {
			query += " AND deleted_at IS NULL"
		}
		if _, err := s.db.Exec(
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
