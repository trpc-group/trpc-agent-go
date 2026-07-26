//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

func TestManualSchemaMatchesAutoInitStorageColumns(t *testing.T) {
	data, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}
	schema := string(data)
	for _, legacy := range []string{
		"state       JSON",
		"extra_data  JSON",
		"event       JSON",
		"summary     JSON",
	} {
		if strings.Contains(schema, legacy) {
			t.Fatalf("manual schema still contains legacy fragment %q", legacy)
		}
	}
	for _, want := range []string{
		"state       String",
		"extra_data  String",
		"event       String",
		"summary     String",
		"version_at  DateTime64(9)",
		"ReplacingMergeTree(version_at)",
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("manual schema missing %q", want)
		}
	}
	summaryStart := strings.Index(schema, "CREATE TABLE IF NOT EXISTS session_summaries")
	if summaryStart < 0 {
		t.Fatalf("manual schema missing session_summaries table")
	}
	summaryEnd := strings.Index(schema[summaryStart:], "COMMENT 'Session summaries table';")
	if summaryEnd < 0 {
		t.Fatalf("manual schema missing session_summaries terminator")
	}
	summaryBlock := schema[summaryStart : summaryStart+summaryEnd]
	if strings.Contains(summaryBlock, "ReplacingMergeTree(updated_at)") {
		t.Fatalf("session_summaries still uses updated_at as replacement version")
	}
}

func TestManualSchemaWorksWithSkipDBInit(t *testing.T) {
	dsn := os.Getenv("TRPC_SESSION_CLICKHOUSE_DSN")
	if dsn == "" {
		dsn = os.Getenv("TRPC_REPLAY_CLICKHOUSE_DSN")
	}
	if dsn == "" {
		t.Skip("TRPC_SESSION_CLICKHOUSE_DSN or TRPC_REPLAY_CLICKHOUSE_DSN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := storage.GetClientBuilder()(
		storage.WithClientBuilderDSN(dsn),
	)
	require.NoError(t, err)
	defer client.Close()

	prefix := fmt.Sprintf("manual_schema_test_%d", time.Now().UnixNano())
	tableNames := []string{
		"session_states",
		"session_events",
		"session_summaries",
		"app_states",
		"user_states",
	}
	defer func() {
		for _, table := range tableNames {
			require.NoError(t, client.Exec(ctx, "DROP TABLE IF EXISTS "+prefix+"_"+table))
		}
	}()

	data, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	schema := sqlWithoutLineComments(string(data))
	for _, table := range tableNames {
		schema = strings.ReplaceAll(
			schema,
			"CREATE TABLE IF NOT EXISTS "+table,
			"CREATE TABLE IF NOT EXISTS "+prefix+"_"+table,
		)
	}
	for _, statement := range strings.Split(schema, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" || !strings.Contains(statement, "CREATE TABLE") {
			continue
		}
		require.NoError(t, client.Exec(ctx, statement))
	}

	svc, err := NewService(
		WithClickHouseDSN(dsn),
		WithSkipDBInit(true),
		WithTablePrefix(prefix),
		WithEnableAsyncPersist(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{
		AppName:   "manual-schema-test",
		UserID:    "user",
		SessionID: fmt.Sprintf("session-%d", time.Now().UnixNano()),
	}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, sess, &event.Event{
		ID:        "manual-schema-event",
		Author:    "schema-test",
		Timestamp: time.Now().UTC(),
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "manual schema event",
				},
			}},
		},
	}))
	var rawEventCount uint64
	require.NoError(t, client.QueryRow(
		ctx,
		[]any{&rawEventCount},
		"SELECT count() FROM "+prefix+"_session_events WHERE event_id = ?",
		"manual-schema-event",
	))
	require.Equal(t, uint64(1), rawEventCount)
	var filteredEventCount uint64
	require.NoError(t, client.QueryRow(
		ctx,
		[]any{&filteredEventCount},
		"SELECT count() FROM "+prefix+"_session_events FINAL WHERE app_name = ? AND user_id = ? AND session_id = ? AND created_at >= (SELECT created_at FROM "+prefix+"_session_states FINAL WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL) AND deleted_at IS NULL",
		key.AppName, key.UserID, key.SessionID,
		key.AppName, key.UserID, key.SessionID,
	))
	require.Equal(t, uint64(1), filteredEventCount)
	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, got.Events, 1)
	require.Equal(t, "manual-schema-event", got.Events[0].ID)
}

func sqlWithoutLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
