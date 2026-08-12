//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

func TestInitDBRejectsLegacySummarySchemaIntegration(t *testing.T) {
	dsn := os.Getenv("TRPC_AGENT_GO_CLICKHOUSE_TEST_DSN")
	if dsn == "" {
		t.Skip("set TRPC_AGENT_GO_CLICKHOUSE_TEST_DSN to run ClickHouse integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("r%x", time.Now().UnixNano())
	client, err := storage.GetClientBuilder()(
		storage.WithClientBuilderDSN(dsn),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()
		for _, tableDef := range tableDefs {
			table := sqldb.BuildTableName(prefix, tableDef.name)
			_ = client.Exec(cleanupCtx, "DROP TABLE IF EXISTS "+table)
		}
		_ = client.Close()
	})

	summaryTable := sqldb.BuildTableName(
		prefix,
		sqldb.TableNameSessionSummaries,
	)
	require.NoError(t, client.Exec(ctx, fmt.Sprintf(`
CREATE TABLE %s (
	app_name String,
	user_id String,
	session_id String,
	filter_key String,
	summary JSON,
	created_at DateTime64(6),
	updated_at DateTime64(6),
	expires_at Nullable(DateTime64(6)),
	deleted_at Nullable(DateTime64(6))
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) %% 64)
ORDER BY (app_name, user_id, session_id, filter_key)
SETTINGS allow_nullable_key = 1`, summaryTable)))

	svc := &Service{
		opts:                  ServiceOpts{tablePrefix: prefix},
		chClient:              client,
		tableSessionSummaries: summaryTable,
	}
	err = svc.initDB(ctx)
	require.ErrorContains(t, err, "has incompatible schema")
	require.ErrorContains(t, err, "ReplacingMergeTree(version_at)")
}

func TestDeleteSessionProjectionTombstonesIntegration(t *testing.T) {
	dsn := os.Getenv("TRPC_AGENT_GO_CLICKHOUSE_TEST_DSN")
	if dsn == "" {
		t.Skip("set TRPC_AGENT_GO_CLICKHOUSE_TEST_DSN to run ClickHouse integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("r%x", time.Now().UnixNano())
	svc, err := NewService(
		WithClickHouseDSN(dsn),
		WithTablePrefix(prefix),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()
		for _, table := range []string{
			svc.tableSessionStates,
			svc.tableSessionEvents,
			svc.tableSessionSummaries,
			svc.tableAppStates,
			svc.tableUserStates,
		} {
			_ = svc.chClient.Exec(cleanupCtx, "DROP TABLE IF EXISTS "+table)
		}
		_ = svc.Close()
	})

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "session",
	}
	_, err = svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	createdAt := time.Now().UTC().Add(-time.Second)
	require.NoError(t, svc.chClient.Exec(
		ctx,
		fmt.Sprintf(`INSERT INTO %s
(app_name, user_id, session_id, event_id, event, extra_data, created_at, updated_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, svc.tableSessionEvents),
		key.AppName,
		key.UserID,
		key.SessionID,
		"event",
		`{}`,
		`{}`,
		createdAt,
		createdAt,
		nil,
	))
	require.NoError(t, svc.chClient.Exec(
		ctx,
		fmt.Sprintf(`INSERT INTO %s
(app_name, user_id, session_id, filter_key, summary, created_at, updated_at, version_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, svc.tableSessionSummaries),
		key.AppName,
		key.UserID,
		key.SessionID,
		"",
		`{"summary":"active"}`,
		createdAt,
		createdAt,
		createdAt,
		nil,
	))
	var activeBefore uint64
	require.NoError(t, svc.chClient.QueryRow(
		ctx,
		[]any{&activeBefore},
		fmt.Sprintf(`SELECT count() FROM %s FINAL
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
			svc.tableSessionSummaries,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	))
	require.Equal(t, uint64(1), activeBefore)

	require.NoError(t, svc.DeleteSession(ctx, key))
	var active, deleted uint64
	require.NoError(t, svc.chClient.QueryRow(
		ctx,
		[]any{&active, &deleted},
		fmt.Sprintf(`SELECT
	countIf(deleted_at IS NULL), countIf(deleted_at IS NOT NULL)
FROM %s FINAL
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?`,
			svc.tableSessionSummaries,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		"",
	))
	require.Zero(t, active)
	require.Equal(t, uint64(1), deleted)
	require.NoError(t, svc.chClient.QueryRow(
		ctx,
		[]any{&active, &deleted},
		fmt.Sprintf(`SELECT
	countIf(deleted_at IS NULL), countIf(deleted_at IS NOT NULL)
FROM %s FINAL
WHERE app_name = ? AND user_id = ? AND session_id = ? AND event_id = ?`,
			svc.tableSessionEvents,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
		"event",
	))
	require.Zero(t, active)
	require.Equal(t, uint64(1), deleted)
}
