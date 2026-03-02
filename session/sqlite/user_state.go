//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// UpdateUserState updates user state.
func (s *Service) UpdateUserState(
	ctx context.Context,
	userKey session.UserKey,
	state session.StateMap,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	now := time.Now().UTC()
	expiresAt := calculateExpiresAt(now, s.opts.userStateTTL)

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		if err := s.upsertUserState(
			ctx,
			userKey.AppName,
			userKey.UserID,
			k,
			v,
			now,
			expiresAt,
		); err != nil {
			return fmt.Errorf("update user state: %w", err)
		}
	}
	return nil
}

func (s *Service) upsertUserState(
	ctx context.Context,
	appName string,
	userID string,
	key string,
	value []byte,
	now time.Time,
	expiresAt *int64,
) error {
	var id int64
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id FROM %s
WHERE app_name = ? AND user_id = ? AND key = ?
AND deleted_at IS NULL
LIMIT 1`,
			s.tableUserStates,
		),
		appName,
		userID,
		key,
	).Scan(&id)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`INSERT INTO %s (
  app_name, user_id, key, value, created_at, updated_at, expires_at,
  deleted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`,
				s.tableUserStates,
			),
			appName,
			userID,
			key,
			value,
			now.UTC().UnixNano(),
			now.UTC().UnixNano(),
			expiresAt,
		)
		return err
	}

	_, err = s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s
SET value = ?, updated_at = ?, expires_at = ?
WHERE id = ?`,
			s.tableUserStates,
		),
		value,
		now.UTC().UnixNano(),
		expiresAt,
		id,
	)
	return err
}

// ListUserStates lists user states.
func (s *Service) ListUserStates(
	ctx context.Context,
	userKey session.UserKey,
) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	const sqlStmt = `SELECT key, value FROM %s
WHERE app_name = ? AND user_id = ?
AND (expires_at IS NULL OR expires_at > ?)
AND deleted_at IS NULL`
	query := fmt.Sprintf(sqlStmt, s.tableUserStates)

	rows, err := s.db.QueryContext(
		ctx,
		query,
		userKey.AppName,
		userKey.UserID,
		time.Now().UTC().UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("list user states: %w", err)
	}
	defer rows.Close()

	out := make(session.StateMap)
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan user state: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user states: %w", err)
	}
	return out, nil
}

// DeleteUserState deletes a user state key.
func (s *Service) DeleteUserState(
	ctx context.Context,
	userKey session.UserKey,
	key string,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	if s.opts.softDelete {
		_, err := s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s
SET deleted_at = ?
WHERE app_name = ? AND user_id = ? AND key = ?
AND deleted_at IS NULL`,
				s.tableUserStates,
			),
			time.Now().UTC().UnixNano(),
			userKey.AppName,
			userKey.UserID,
			key,
		)
		if err != nil {
			return fmt.Errorf("delete user state: %w", err)
		}
		return nil
	}

	_, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s WHERE app_name = ? AND user_id = ? AND key = ?`,
			s.tableUserStates,
		),
		userKey.AppName,
		userKey.UserID,
		key,
	)
	if err != nil {
		return fmt.Errorf("delete user state: %w", err)
	}
	return nil
}
