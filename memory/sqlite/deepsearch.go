//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
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
	return s.writeDeepSearchIndexes(ctx, userKey, rows, indexes)
}

func (s *Service) deepSearchRows(
	ctx context.Context,
	userKey memory.UserKey,
) ([]deepsearch.EntryRow, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT memory_data FROM %s
WHERE app_name = ? AND user_id = ?`, s.tableName)
	if s.opts.softDelete {
		query += " AND deleted_at IS NULL"
	}
	sqlRows, err := s.db.QueryContext(ctx, query, userKey.AppName, userKey.UserID)
	if err != nil {
		return nil, fmt.Errorf("read deepsearch rows: %w", err)
	}
	defer sqlRows.Close()

	rows := make([]deepsearch.EntryRow, 0)
	for sqlRows.Next() {
		var raw []byte
		if err := sqlRows.Scan(&raw); err != nil {
			return nil, err
		}
		entry, index, err := deepsearch.UnmarshalAttachedEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("unmarshal deepsearch row: %w", err)
		}
		imemory.NormalizeEntry(entry)
		rows = append(rows, deepsearch.EntryRow{
			Entry: entry,
			Index: index,
		})
	}
	if err := sqlRows.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) writeDeepSearchIndexes(
	ctx context.Context,
	userKey memory.UserKey,
	rows []deepsearch.EntryRow,
	indexes map[string]*deepsearch.Index,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deepsearch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, row := range rows {
		index := indexes[row.Entry.ID]
		raw, err := deepsearch.MarshalAttachedEntry(row.Entry, index)
		if err != nil {
			return err
		}
		query := fmt.Sprintf(`UPDATE %s SET memory_data = ?, updated_at = ?
WHERE app_name = ? AND user_id = ? AND memory_id = ?`, s.tableName)
		if s.opts.softDelete {
			query += " AND deleted_at IS NULL"
		}
		if _, err := tx.ExecContext(
			ctx,
			query,
			raw,
			row.Entry.UpdatedAt.UTC().UnixNano(),
			userKey.AppName,
			userKey.UserID,
			row.Entry.ID,
		); err != nil {
			return fmt.Errorf("write deepsearch row %s: %w", row.Entry.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		if errors.Is(err, sql.ErrTxDone) {
			return err
		}
		return fmt.Errorf("commit deepsearch transaction: %w", err)
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
