//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/rewindtest"
)

func TestLatestTurnReplacementIntegration(t *testing.T) {
	dsn := os.Getenv("TRPC_AGENT_GO_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set TRPC_AGENT_GO_MYSQL_TEST_DSN to run MySQL integration test")
	}
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%t", async), func(t *testing.T) {
			prefix := fmt.Sprintf("r%x_", time.Now().UnixNano())
			svc, err := NewService(
				WithMySQLClientDSN(dsn),
				WithTablePrefix(prefix),
				WithEnableAsyncPersist(async),
			)
			require.NoError(t, err)
			rawDB, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, svc.Close())
				for _, table := range []string{
					svc.tableSessionTracks,
					svc.tableSessionEvents,
					svc.tableSessionSummaries,
					svc.tableSessionStates,
					svc.tableAppStates,
					svc.tableUserStates,
				} {
					_, _ = rawDB.ExecContext(
						context.Background(),
						fmt.Sprintf("DROP TABLE IF EXISTS `%s`", table),
					)
				}
				require.NoError(t, rawDB.Close())
			})
			if async {
				rewindtest.RunAsync(t, svc)
			} else {
				rewindtest.Run(t, svc)
			}
		})
	}
}
