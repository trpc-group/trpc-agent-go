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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const cleanupTimeout = 5 * time.Minute

func (s *Service) startCleanupRoutine() {
	interval := s.opts.cleanupInterval
	if interval <= 0 {
		return
	}

	s.cleanupTicker = time.NewTicker(interval)
	go func() {
		log.InfofContext(
			context.Background(),
			"started cleanup routine for sqlite session service "+
				"(interval: %v)",
			interval,
		)
		for {
			select {
			case <-s.cleanupTicker.C:
				ctx, cancel := context.WithTimeout(
					context.Background(),
					cleanupTimeout,
				)
				s.cleanupExpiredData(ctx)
				cancel()
			case <-s.cleanupDone:
				log.InfoContext(
					context.Background(),
					"cleanup routine stopped for sqlite session "+
						"service",
				)
				return
			}
		}
	}()
}

func (s *Service) stopCleanupRoutine() {
	s.cleanupOnce.Do(func() {
		if s.cleanupTicker != nil {
			s.cleanupTicker.Stop()
		}
		if s.cleanupDone != nil {
			close(s.cleanupDone)
		}
	})
}

func (s *Service) cleanupExpiredData(ctx context.Context) {
	now := time.Now().UTC()
	if s.opts.sessionTTL > 0 {
		s.cleanupExpiredSessions(ctx, now)
	}
	if s.opts.appStateTTL > 0 {
		s.cleanupExpiredAppStates(ctx, now)
	}
	if s.opts.userStateTTL > 0 {
		s.cleanupExpiredUserStates(ctx, now)
	}
}

func (s *Service) cleanupExpiredSessions(ctx context.Context, now time.Time) {
	nowNs := now.UTC().UnixNano()
	if s.opts.softDelete {
		s.softDeleteExpiredSessions(ctx, nowNs)
		return
	}
	s.hardDeleteExpiredSessions(ctx, nowNs)
}

func (s *Service) softDeleteExpiredSessions(
	ctx context.Context,
	nowNs int64,
) {
	const whereExpired = `expires_at IS NOT NULL AND expires_at <= ?`

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.ErrorfContext(ctx, "begin tx: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.softDeleteExpiredSessionsTx(
		ctx,
		tx,
		nowNs,
		whereExpired,
	); err != nil {
		log.ErrorfContext(ctx, "cleanup expired sessions: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.ErrorfContext(ctx, "commit cleanup: %v", err)
		return
	}
}

func (s *Service) softDeleteExpiredSessionsTx(
	ctx context.Context,
	tx *sql.Tx,
	nowNs int64,
	whereExpired string,
) error {
	args := []any{nowNs, nowNs}

	_, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET deleted_at = ?
WHERE %s AND deleted_at IS NULL`,
			s.tableSessionStates,
			whereExpired,
		),
		args...,
	)
	if err != nil {
		return err
	}

	if err := s.softDeleteExpiredBySession(ctx, tx, s.tableSessionEvents,
		nowNs, whereExpired); err != nil {
		return err
	}
	if err := s.softDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionTracks,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}
	if err := s.softDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionSummaries,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}
	return nil
}

func (s *Service) softDeleteExpiredBySession(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	nowNs int64,
	whereExpired string,
) error {
	_, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET deleted_at = ?
WHERE deleted_at IS NULL AND EXISTS (
  SELECT 1 FROM %s st
  WHERE st.app_name = %s.app_name
    AND st.user_id = %s.user_id
    AND st.session_id = %s.session_id
    AND %s
)`,
			table,
			s.tableSessionStates,
			table,
			table,
			table,
			whereExpired,
		),
		nowNs,
		nowNs,
	)
	return err
}

func (s *Service) hardDeleteExpiredSessions(
	ctx context.Context,
	nowNs int64,
) {
	const whereExpired = `expires_at IS NOT NULL AND expires_at <= ?`

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.ErrorfContext(ctx, "begin tx: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.hardDeleteExpiredSessionsTx(
		ctx,
		tx,
		nowNs,
		whereExpired,
	); err != nil {
		log.ErrorfContext(ctx, "cleanup expired sessions: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.ErrorfContext(ctx, "commit cleanup: %v", err)
		return
	}
}

func (s *Service) hardDeleteExpiredSessionsTx(
	ctx context.Context,
	tx *sql.Tx,
	nowNs int64,
	whereExpired string,
) error {
	if err := s.hardDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionEvents,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}
	if err := s.hardDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionTracks,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}
	if err := s.hardDeleteExpiredBySession(
		ctx,
		tx,
		s.tableSessionSummaries,
		nowNs,
		whereExpired,
	); err != nil {
		return err
	}

	_, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s WHERE %s`,
			s.tableSessionStates,
			whereExpired,
		),
		nowNs,
	)
	return err
}

func (s *Service) hardDeleteExpiredBySession(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	nowNs int64,
	whereExpired string,
) error {
	_, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
WHERE EXISTS (
  SELECT 1 FROM %s st
  WHERE st.app_name = %s.app_name
    AND st.user_id = %s.user_id
    AND st.session_id = %s.session_id
    AND %s
)`,
			table,
			s.tableSessionStates,
			table,
			table,
			table,
			whereExpired,
		),
		nowNs,
	)
	return err
}

func (s *Service) cleanupExpiredAppStates(
	ctx context.Context,
	now time.Time,
) {
	nowNs := now.UTC().UnixNano()
	if s.opts.softDelete {
		_, err := s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET deleted_at = ?
WHERE expires_at IS NOT NULL AND expires_at <= ?
AND deleted_at IS NULL`,
				s.tableAppStates,
			),
			nowNs,
			nowNs,
		)
		if err != nil {
			log.ErrorfContext(ctx, "cleanup app states: %v", err)
		}
		return
	}

	_, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
WHERE expires_at IS NOT NULL AND expires_at <= ?`,
			s.tableAppStates,
		),
		nowNs,
	)
	if err != nil {
		log.ErrorfContext(ctx, "cleanup app states: %v", err)
	}
}

func (s *Service) cleanupExpiredUserStates(
	ctx context.Context,
	now time.Time,
) {
	nowNs := now.UTC().UnixNano()
	if s.opts.softDelete {
		_, err := s.db.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET deleted_at = ?
WHERE expires_at IS NOT NULL AND expires_at <= ?
AND deleted_at IS NULL`,
				s.tableUserStates,
			),
			nowNs,
			nowNs,
		)
		if err != nil {
			log.ErrorfContext(ctx, "cleanup user states: %v", err)
		}
		return
	}

	_, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
WHERE expires_at IS NOT NULL AND expires_at <= ?`,
			s.tableUserStates,
		),
		nowNs,
	)
	if err != nil {
		log.ErrorfContext(ctx, "cleanup user states: %v", err)
	}
}
