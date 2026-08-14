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
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionSummarySchemaCompatibilityIntegration(t *testing.T) {
	dsn := mysqlIntegrationDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(ctx))

	t.Run("current index with migration name", func(t *testing.T) {
		prefix := prepareSummaryIntegrationSchema(t, db, dsn)
		tableName := sqldb.BuildTableName(prefix, sqldb.TableNameSessionSummaries)
		canonicalName := sqldb.BuildIndexName(
			prefix, sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive,
		)
		migrationName := canonicalName + "_v2"
		_, err := db.ExecContext(ctx, fmt.Sprintf(
			"ALTER TABLE `%s` RENAME INDEX `%s` TO `%s`",
			tableName, canonicalName, migrationName,
		))
		require.NoError(t, err)

		svc := newSummaryIntegrationService(t, dsn, prefix)
		require.NoError(t, svc.Close())
	})

	t.Run("legacy unique soft delete preserves history", func(t *testing.T) {
		prefix := prepareSummaryIntegrationSchema(t, db, dsn)
		tableName := sqldb.BuildTableName(prefix, sqldb.TableNameSessionSummaries)
		replaceSummaryIndexWithLegacyUnique(t, ctx, db, prefix)

		svc := newSummaryIntegrationService(t, dsn, prefix)
		t.Cleanup(func() { require.NoError(t, svc.Close()) })
		key := createSummaryIntegrationSession(t, ctx, svc)

		updatedAt := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
		deletedAt := updatedAt.Add(-time.Hour)
		insertSummaryIntegrationRow(t, ctx, db, tableName, key, "active-1", updatedAt, nil)
		insertSummaryIntegrationRow(t, ctx, db, tableName, key, "active-2", updatedAt, nil)
		insertSummaryIntegrationRow(t, ctx, db, tableName, key, "historical", updatedAt, deletedAt)

		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, svc.softDeleteSummaries(
			ctx, tx, "app_name = ?", []any{key.AppName}, updatedAt.Add(time.Hour),
		))
		require.NoError(t, tx.Commit())

		var activeCount int
		err = db.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT COUNT(*) FROM `%s` WHERE app_name = ? AND deleted_at IS NULL", tableName,
		), key.AppName).Scan(&activeCount)
		require.NoError(t, err)
		require.Zero(t, activeCount)

		var historicalDeletedAt time.Time
		err = db.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT deleted_at FROM `%s` WHERE app_name = ? AND deleted_at IS NOT NULL", tableName,
		), key.AppName).Scan(&historicalDeletedAt)
		require.NoError(t, err)
		require.Equal(t, deletedAt, historicalDeletedAt)
	})

	t.Run("legacy lookup keeps deterministic newest summary", func(t *testing.T) {
		prefix := prepareSummaryIntegrationSchema(t, db, dsn)
		tableName := sqldb.BuildTableName(prefix, sqldb.TableNameSessionSummaries)
		replaceSummaryIndexWithLegacyLookup(t, ctx, db, prefix)

		svc := newSummaryIntegrationService(t, dsn, prefix)
		t.Cleanup(func() { require.NoError(t, svc.Close()) })
		key := createSummaryIntegrationSession(t, ctx, svc)

		createdAt := time.Date(2026, time.August, 14, 7, 0, 0, 0, time.UTC)
		duplicateUpdatedAt := createdAt.Add(time.Hour)
		insertSummaryIntegrationRow(t, ctx, db, tableName, key, "first", duplicateUpdatedAt, nil)
		insertSummaryIntegrationRow(t, ctx, db, tableName, key, "second", duplicateUpdatedAt, nil)

		sess := &session.Session{
			ID: key.SessionID, AppName: key.AppName, UserID: key.UserID, CreatedAt: createdAt,
		}
		text, ok := svc.GetSessionSummaryText(ctx, sess)
		require.True(t, ok)
		require.Equal(t, "second", text)

		summaries, err := svc.getSummariesList(ctx, []session.Key{key}, []time.Time{createdAt})
		require.NoError(t, err)
		require.Len(t, summaries, 1)
		require.Equal(t, "second", summaries[0][""].Summary)

		olderUpdatedAt := duplicateUpdatedAt.Add(time.Minute)
		newerUpdatedAt := olderUpdatedAt.Add(time.Minute)
		olderBytes := marshalSummaryIntegration(t, "older", olderUpdatedAt)
		newerBytes := marshalSummaryIntegration(t, "newer", newerUpdatedAt)

		olderPrepared := make(chan struct{})
		releaseOlder := make(chan struct{})
		olderErr := make(chan error, 1)
		go func() {
			close(olderPrepared)
			<-releaseOlder
			olderErr <- svc.upsertSessionSummary(ctx, key, "", olderBytes, olderUpdatedAt)
		}()
		<-olderPrepared
		newerErr := svc.upsertSessionSummary(ctx, key, "", newerBytes, newerUpdatedAt)
		close(releaseOlder)
		require.NoError(t, newerErr)
		require.NoError(t, <-olderErr)
		requireSummaryIntegrationRows(t, ctx, db, tableName, key, "newer", newerUpdatedAt)

		regeneratedBytes := marshalSummaryIntegration(t, "regenerated", newerUpdatedAt)
		require.NoError(t, svc.upsertSessionSummary(
			ctx, key, "", regeneratedBytes, newerUpdatedAt,
		))
		requireSummaryIntegrationRows(t, ctx, db, tableName, key, "regenerated", newerUpdatedAt)
	})
}

func mysqlIntegrationDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TRPC_AGENT_GO_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set TRPC_AGENT_GO_MYSQL_TEST_DSN to run MySQL integration tests")
	}
	cfg, err := drivermysql.ParseDSN(dsn)
	require.NoError(t, err)
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	return cfg.FormatDSN()
}

func prepareSummaryIntegrationSchema(
	t *testing.T,
	db *sql.DB,
	dsn string,
) string {
	t.Helper()
	prefix := fmt.Sprintf("it_%x_", time.Now().UnixNano())
	t.Cleanup(func() {
		for i := len(tableDefs) - 1; i >= 0; i-- {
			tableName := sqldb.BuildTableName(prefix, tableDefs[i].name)
			_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tableName))
		}
	})

	svc := newSummaryIntegrationService(t, dsn, prefix)
	require.NoError(t, svc.Close())
	return prefix
}

func newSummaryIntegrationService(t *testing.T, dsn, prefix string) *Service {
	t.Helper()
	svc, err := NewService(
		WithMySQLClientDSN(dsn),
		WithTablePrefix(prefix),
		WithSessionTTL(time.Hour),
	)
	require.NoError(t, err)
	return svc
}

func replaceSummaryIndexWithLegacyUnique(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	prefix string,
) {
	t.Helper()
	tableName := sqldb.BuildTableName(prefix, sqldb.TableNameSessionSummaries)
	indexName := sqldb.BuildIndexName(
		prefix, sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive,
	)
	_, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE `%s` DROP INDEX `%s`", tableName, indexName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(
		"ALTER TABLE `%s` ADD UNIQUE INDEX `%s` "+
			"(app_name(191), user_id(191), session_id(191), filter_key(191), deleted_at)",
		tableName, indexName,
	))
	require.NoError(t, err)
}

func replaceSummaryIndexWithLegacyLookup(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	prefix string,
) {
	t.Helper()
	tableName := sqldb.BuildTableName(prefix, sqldb.TableNameSessionSummaries)
	indexName := sqldb.BuildIndexName(
		prefix, sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive,
	)
	_, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE `%s` DROP INDEX `%s`", tableName, indexName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(
		"ALTER TABLE `%s` ADD INDEX `%s_lookup` "+
			"(app_name(191), user_id(191), session_id(191), deleted_at)",
		tableName, indexName,
	))
	require.NoError(t, err)
}

func createSummaryIntegrationSession(
	t *testing.T,
	ctx context.Context,
	svc *Service,
) session.Key {
	t.Helper()
	key := session.Key{
		AppName: "integration-app", UserID: "integration-user", SessionID: "integration-session",
	}
	_, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	return key
}

func insertSummaryIntegrationRow(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	tableName string,
	key session.Key,
	text string,
	updatedAt time.Time,
	deletedAt any,
) {
	t.Helper()
	summaryBytes := marshalSummaryIntegration(t, text, updatedAt)
	_, err := db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s
			(app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`, tableName,
	), key.AppName, key.UserID, key.SessionID, "", string(summaryBytes), updatedAt, deletedAt)
	require.NoError(t, err)
}

func marshalSummaryIntegration(t *testing.T, text string, updatedAt time.Time) []byte {
	t.Helper()
	summaryBytes, err := json.Marshal(&session.Summary{Summary: text, UpdatedAt: updatedAt})
	require.NoError(t, err)
	return summaryBytes
}

func requireSummaryIntegrationRows(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	tableName string,
	key session.Key,
	wantText string,
	wantUpdatedAt time.Time,
) {
	t.Helper()
	rows, err := db.QueryContext(ctx, fmt.Sprintf(
		`SELECT summary, updated_at FROM %s
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?
		AND deleted_at IS NULL ORDER BY id`, tableName,
	), key.AppName, key.UserID, key.SessionID, "")
	require.NoError(t, err)
	defer rows.Close()

	count := 0
	for rows.Next() {
		var summaryBytes []byte
		var updatedAt time.Time
		require.NoError(t, rows.Scan(&summaryBytes, &updatedAt))
		var sum session.Summary
		require.NoError(t, json.Unmarshal(summaryBytes, &sum))
		require.Equal(t, wantText, sum.Summary)
		require.Equal(t, wantUpdatedAt, updatedAt)
		count++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 2, count)
}
