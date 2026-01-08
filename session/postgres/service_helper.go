//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package postgres

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// refreshSessionSummaryTTLs updates the expires_at timestamps of all summaries for a session.
// This ensures summaries remain valid when the session TTL is refreshed.
func (s *Service) refreshSessionSummaryTTLs(ctx context.Context, key session.Key) error {
	now := time.Now()
	expiresAt := now.Add(s.opts.sessionTTL)

	_, err := s.pgClient.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s
		SET expires_at = $1
		WHERE app_name = $2 AND user_id = $3 AND session_id = $4
		AND deleted_at IS NULL`, s.tableSessionSummaries),
		expiresAt, key.AppName, key.UserID, key.SessionID)

	if err != nil {
		return fmt.Errorf("refresh session summary TTLs failed: %w", err)
	}

	return nil
}
