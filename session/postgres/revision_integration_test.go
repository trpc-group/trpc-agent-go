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
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/replacementtest"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLatestTurnReplacementIntegration(t *testing.T) {
	dsn := os.Getenv("TRPC_AGENT_GO_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set TRPC_AGENT_GO_POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%t", async), func(t *testing.T) {
			svc, err := NewService(
				WithPostgresClientDSN(dsn),
				WithTablePrefix(fmt.Sprintf("r%x_", time.Now().UnixNano())),
				WithEnableAsyncPersist(async),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, svc.Close()) })
			if async {
				replacementtest.RunAsync(t, svc)
			} else {
				replacementtest.Run(t, svc)
			}
		})
	}
	t.Run("soft-deleted summary history", func(t *testing.T) {
		svc, err := NewService(
			WithPostgresClientDSN(dsn),
			WithTablePrefix(fmt.Sprintf("r%x_", time.Now().UnixNano())),
			WithSummarizer(&mockSummarizerImpl{summaryText: "summary"}),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, svc.Close()) })
		replacementtest.RunSoftDeletedSummaryHistory(
			t,
			svc,
			func(key session.Key) (int, error) {
				return countSoftDeletedSummaries(context.Background(), svc, key)
			},
		)
	})
}

func countSoftDeletedSummaries(
	ctx context.Context,
	svc *Service,
	key session.Key,
) (int, error) {
	var count int
	err := svc.pgClient.Query(ctx, func(rows *sql.Rows) error {
		if !rows.Next() {
			return sql.ErrNoRows
		}
		return rows.Scan(&count)
	}, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s
WHERE app_name = $1 AND user_id = $2 AND session_id = $3 AND deleted_at IS NOT NULL`,
		svc.tableSessionSummaries,
	), key.AppName, key.UserID, key.SessionID)
	return count, err
}
