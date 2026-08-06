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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// UpdateSessionState updates the session-level state without appending an
// event. Keys with app: or user: prefixes are not allowed.
func (s *Service) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf(
				"%s is not allowed, use UpdateAppState instead",
				k,
			)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf(
				"%s is not allowed, use UpdateUserState instead",
				k,
			)
		}
	}

	s.stateWriteMu.Lock()
	defer s.stateWriteMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update session state: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var current []byte
	err = tx.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT state FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("session not found")
	}
	if err != nil {
		return fmt.Errorf("get session state: %w", err)
	}
	write := sessionrevision.NewWrite(ctx, nil)
	record, revisionSupported, err := s.readRevisionForWrite(ctx, tx, key)
	if err != nil {
		return err
	}
	if err := checkRevisionGeneration(record, write); err != nil {
		return err
	}
	revisionChanged := sessionrevision.ApplyWrite(record, write)

	var sessState SessionState
	if len(current) > 0 {
		if err := json.Unmarshal(current, &sessState); err != nil {
			return fmt.Errorf("unmarshal state: %w", err)
		}
	}
	if sessState.State == nil {
		sessState.State = make(session.StateMap)
	}
	for k, v := range state {
		if v == nil {
			sessState.State[k] = nil
			continue
		}
		copied := make([]byte, len(v))
		copy(copied, v)
		sessState.State[k] = copied
	}

	now := time.Now().UTC()
	sessState.UpdatedAt = now
	updatedBytes, err := json.Marshal(sessState)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	expiresAt := calculateExpiresAt(now, s.opts.sessionTTL)

	_, err = tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s
SET state = ?, updated_at = ?, expires_at = ?
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND deleted_at IS NULL`,
			s.tableSessionStates,
		),
		updatedBytes,
		now.UTC().UnixNano(),
		expiresAt,
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	if revisionSupported && (revisionChanged || write.HasExpectedGeneration) {
		if err := s.writeRevisionTx(ctx, tx, key, record, expiresAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update session state: %w", err)
	}
	return nil
}

func mergeState(
	appState session.StateMap,
	userState session.StateMap,
	sess *session.Session,
) *session.Session {
	if sess == nil {
		return nil
	}
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
	}
	return sess
}
