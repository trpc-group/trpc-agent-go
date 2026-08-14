//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"fmt"
	"time"

	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

// CreateSessionSummary creates or updates a session
// summary. It delegates to the configured summarizer
// and persists the result.
func (s *Service) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if s.opts.summarizer == nil {
		return nil
	}
	if sess == nil {
		return session.ErrNilSession
	}

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf(
			"check session key failed: %w", err,
		)
	}
	if !isummary.NewSummaryDispatchPolicy(
		s.opts.summaryFilterAllowlist,
		s.opts.shouldCascadeFullSessionSummary(),
	).AllowsFilterKey(filterKey) {
		return nil
	}

	updated, err := isummary.SummarizeSession(
		ctx, s.opts.summarizer,
		sess, filterKey, force,
	)
	if err != nil || !updated {
		return err
	}

	sess.SummariesMu.RLock()
	sum := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()

	if sum == nil {
		return nil
	}

	summaryBytes, err := json.Marshal(sum)
	if err != nil {
		return fmt.Errorf(
			"marshal summary failed: %w", err,
		)
	}

	write := sessionrevision.NewWrite(ctx, sess)
	err = s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		var stateRaw []byte
		var expiresAt *time.Time
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT state, expires_at FROM %s WHERE app_name = $1 AND user_id = $2
			AND session_id = $3 AND deleted_at IS NULL FOR UPDATE`,
			s.tableSessionStates,
		), key.AppName, key.UserID, key.SessionID).Scan(&stateRaw, &expiresAt); err != nil {
			return fmt.Errorf("load session revision for summary: %w", err)
		}
		var state SessionState
		record, err := sessionrevision.DecodeState(stateRaw, &state)
		if err != nil {
			return fmt.Errorf("decode session revision for summary: %w", err)
		}
		if err := s.revisionStore().ApplyMutation(
			record, write,
		); err != nil {
			return fmt.Errorf("apply session revision for summary: %w", err)
		}
		stateRaw, err = sessionrevision.EncodeState(state, record)
		if err != nil {
			return fmt.Errorf("encode session revision for summary: %w", err)
		}
		if _, err = tx.ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s SET state = $1
			WHERE app_name = $2 AND user_id = $3 AND session_id = $4 AND deleted_at IS NULL`,
			s.tableSessionStates,
		), stateRaw, key.AppName, key.UserID, key.SessionID); err != nil {
			return fmt.Errorf("persist session revision for summary: %w", err)
		}
		_, err = tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO %s
			(app_name, user_id, session_id,
			 filter_key, summary, updated_at,
			 expires_at, deleted_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
			ON CONFLICT (app_name, user_id,
				session_id, filter_key)
			WHERE deleted_at IS NULL
			DO UPDATE SET
			  summary = EXCLUDED.summary,
			  updated_at = EXCLUDED.updated_at,
			  expires_at = EXCLUDED.expires_at`,
			s.tableSessionSummaries,
		), sess.AppName, sess.UserID, sess.ID,
			filterKey, summaryBytes, sum.UpdatedAt, nil)
		if err != nil {
			return fmt.Errorf("upsert summary failed: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// EnqueueSummaryJob enqueues a summary job for
// asynchronous processing.
func (s *Service) EnqueueSummaryJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if s.opts.summarizer == nil {
		return nil
	}
	if sess == nil {
		return session.ErrNilSession
	}

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf(
			"check session key failed: %w", err,
		)
	}

	if s.asyncWorker != nil {
		return s.asyncWorker.EnqueueJob(
			ctx, sess, filterKey, force,
		)
	}
	// Fallback to synchronous processing with the same detached context that
	// async workers use.
	return isummary.CreateSessionSummaryWithCascade(
		isummary.DetachContext(ctx),
		sess,
		filterKey,
		force,
		isummary.NewSummaryDispatchPolicy(
			s.opts.summaryFilterAllowlist,
			s.opts.shouldCascadeFullSessionSummary(),
		),
		s.CreateSessionSummary,
	)
}

// GetSessionSummaryText gets the summary text for a
// session. Returns the full-session summary by default.
func (s *Service) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	if sess == nil {
		return "", false
	}

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return "", false
	}

	if text, ok := isummary.GetSummaryTextFromSession(
		sess, opts...,
	); ok {
		return text, true
	}

	filterKey := isummary.GetFilterKeyFromOptions(
		opts...,
	)

	var summaryText string
	err := s.pgClient.Query(ctx,
		func(rows *sql.Rows) error {
			if rows.Next() {
				var summaryBytes []byte
				if err := rows.Scan(
					&summaryBytes,
				); err != nil {
					return err
				}
				var sum session.Summary
				if err := json.Unmarshal(
					summaryBytes, &sum,
				); err != nil {
					return fmt.Errorf(
						"unmarshal summary failed: %w",
						err,
					)
				}
				summaryText = sum.Summary
			}
			return nil
		},
		fmt.Sprintf(
			`SELECT summary FROM %s
			WHERE app_name = $1 AND user_id = $2
			AND session_id = $3 AND filter_key = $4
			AND (expires_at IS NULL
				OR expires_at > $5)
			AND updated_at >= $6
			AND deleted_at IS NULL`,
			s.tableSessionSummaries,
		),
		key.AppName, key.UserID, key.SessionID,
		filterKey, time.Now(), sess.CreatedAt,
	)
	if err == nil && summaryText != "" {
		return summaryText, true
	}

	// Fallback to full-session summary.
	if err == nil &&
		filterKey !=
			session.SummaryFilterKeyAllContents {
		err = s.pgClient.Query(ctx,
			func(rows *sql.Rows) error {
				if rows.Next() {
					var summaryBytes []byte
					if err := rows.Scan(
						&summaryBytes,
					); err != nil {
						return err
					}
					var sum session.Summary
					if err := json.Unmarshal(
						summaryBytes, &sum,
					); err != nil {
						return fmt.Errorf(
							"unmarshal summary "+
								"failed: %w", err,
						)
					}
					summaryText = sum.Summary
				}
				return nil
			},
			fmt.Sprintf(
				`SELECT summary FROM %s
				WHERE app_name = $1
				AND user_id = $2
				AND session_id = $3
				AND filter_key = $4
				AND (expires_at IS NULL
					OR expires_at > $5)
				AND updated_at >= $6
				AND deleted_at IS NULL`,
				s.tableSessionSummaries,
			),
			key.AppName, key.UserID, key.SessionID,
			session.SummaryFilterKeyAllContents,
			time.Now(), sess.CreatedAt,
		)
		if err == nil && summaryText != "" {
			return summaryText, true
		}
	}
	return "", false
}
