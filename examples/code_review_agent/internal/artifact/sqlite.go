//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package artifact provides a SQLite-backed implementation of artifact.Service
package artifact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	trpcartifact "trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspacefacade"
)

var _ trpcartifact.Service = (*sqliteArtifactService)(nil)

type sqliteArtifactService struct {
	db       *sql.DB
	maxBytes int64
}

// New creates an Artifact Service using an initialized caller-owned database.
func New(db *sql.DB) (service trpcartifact.Service, err error) {
	if db == nil {
		return nil, errors.New("sqlite artifact service requires a database")
	}
	return &sqliteArtifactService{
		db: db, maxBytes: workspacefacade.DefaultArtifactMaxBytes,
	}, nil
}

func (s *sqliteArtifactService) SaveArtifact(
	ctx context.Context,
	info trpcartifact.SessionInfo,
	filename string,
	item *trpcartifact.Artifact,
) (version int, err error) {
	if err := validateArtifactRequest(info, filename); err != nil {
		return 0, err
	}
	if item == nil {
		return 0, errors.New("artifact is required")
	}
	if int64(len(item.Data)) > s.maxBytes {
		return 0, fmt.Errorf("artifact %q exceeds the %d-byte limit", filename, s.maxBytes)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin artifact save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var next int
	err = tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(version) + 1, 0) FROM artifact_versions
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filename = ?`,
		info.AppName, info.UserID, info.SessionID, filename).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("select next artifact version: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO artifact_versions (
	app_name, user_id, session_id, filename, version, data, mime_type,
	display_name, url, size_bytes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, info.AppName, info.UserID,
		info.SessionID, filename, next, item.Data, item.MimeType,
		nullableString(item.Name), nullableString(item.URL), len(item.Data))
	if err != nil {
		return 0, fmt.Errorf("insert artifact version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit artifact save: %w", err)
	}
	return next, nil
}

func (s *sqliteArtifactService) LoadArtifact(
	ctx context.Context,
	info trpcartifact.SessionInfo,
	filename string,
	version *int,
) (artifactItem *trpcartifact.Artifact, err error) {
	if err := validateArtifactRequest(info, filename); err != nil {
		return nil, err
	}
	query := `SELECT data, mime_type, display_name, url FROM artifact_versions
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filename = ?`
	args := []any{info.AppName, info.UserID, info.SessionID, filename}
	if version == nil {
		query += " ORDER BY version DESC LIMIT 1"
	} else {
		query += " AND version = ?"
		args = append(args, *version)
	}
	var item trpcartifact.Artifact
	var name, url sql.NullString
	err = s.db.QueryRowContext(ctx, query, args...).Scan(&item.Data, &item.MimeType, &name, &url)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load artifact: %w", err)
	}
	item.Name = name.String
	item.URL = url.String
	return &item, nil
}

func (s *sqliteArtifactService) ListArtifactKeys(ctx context.Context, info trpcartifact.SessionInfo) (artifactKeys []string, err error) {
	if err := validateSessionInfo(info); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT filename FROM artifact_versions
WHERE app_name = ? AND user_id = ? AND session_id = ? ORDER BY filename`,
		info.AppName, info.UserID, info.SessionID)
	if err != nil {
		return nil, fmt.Errorf("list artifact keys: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan artifact key: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *sqliteArtifactService) DeleteArtifact(ctx context.Context, info trpcartifact.SessionInfo, filename string) error {
	if err := validateArtifactRequest(info, filename); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM artifact_versions
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filename = ?`,
		info.AppName, info.UserID, info.SessionID, filename)
	if err != nil {
		return fmt.Errorf("delete artifact: %w", err)
	}
	return nil
}

func (s *sqliteArtifactService) ListVersions(
	ctx context.Context,
	info trpcartifact.SessionInfo,
	filename string,
) (artifactVersions []int, err error) {
	if err := validateArtifactRequest(info, filename); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM artifact_versions
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filename = ? ORDER BY version`,
		info.AppName, info.UserID, info.SessionID, filename)
	if err != nil {
		return nil, fmt.Errorf("list artifact versions: %w", err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan artifact version: %w", err)
		}
		versions = append(versions, version)
	}
	sort.Ints(versions)
	return versions, rows.Err()
}

func validateArtifactRequest(info trpcartifact.SessionInfo, filename string) error {
	if err := validateSessionInfo(info); err != nil {
		return err
	}
	if filename == "" {
		return errors.New("artifact filename is required")
	}
	return nil
}

func validateSessionInfo(info trpcartifact.SessionInfo) error {
	if info.AppName == "" || info.UserID == "" || info.SessionID == "" {
		return errors.New("artifact session info requires app name, user id, and session id")
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
