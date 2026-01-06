//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

func TestService_Cleanup_SoftDeleteExpired(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
		opts: ServiceOpts{
			sessionTTL:   time.Hour,
			appStateTTL:  time.Hour,
			userStateTTL: time.Hour,
		},
	}
	ctx := context.Background()
	now := time.Now()

	// 1. softDeleteExpiredSessions
	callCount := 0
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		callCount++
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			expAt := now.Add(-time.Hour)
			return newMockRows([][]any{
				{"app1", "user1", "sess1", "{}", now, &expAt},
			}), nil
		}
		if strings.Contains(query, "FROM "+s.tableSessionEvents) {
			return newMockRows([][]any{
				{"evt1", "{}", now, now},
			}), nil
		}
		if strings.Contains(query, "FROM "+s.tableSessionSummaries) {
			return newMockRows([][]any{
				{"key1", "{}", now, now},
			}), nil
		}
		return newMockRows([][]any{}), nil
	}

	batchCount := 0
	mockCli.batchInsertFunc = func(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
		batchCount++
		return fn(&mockBatch{})
	}

	s.softDeleteExpiredSessions(ctx, now)

	// Expect calls:
	// 1. Query sessions
	// 2. BatchInsert sessions
	// 3. Query events (for sess1)
	// 4. BatchInsert events
	// 5. Query summaries (for sess1)
	// 6. BatchInsert summaries
	assert.Equal(t, 3, callCount)
	assert.Equal(t, 3, batchCount)
}

func TestService_Cleanup_SoftDeleteExpiredAppUser(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:        mockCli,
		tableAppStates:  "app_states",
		tableUserStates: "user_states",
		opts: ServiceOpts{
			appStateTTL:  time.Hour,
			userStateTTL: time.Hour,
		},
	}
	ctx := context.Background()
	now := time.Now()

	callCount := 0
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		callCount++
		expAt := now.Add(-time.Hour)
		if strings.Contains(query, "FROM "+s.tableAppStates) {
			return newMockRows([][]any{
				{"app1", "k1", "v1", &expAt},
			}), nil
		}
		if strings.Contains(query, "FROM "+s.tableUserStates) {
			return newMockRows([][]any{
				{"app1", "user1", "k1", "v1", &expAt},
			}), nil
		}
		return newMockRows([][]any{}), nil
	}

	batchCount := 0
	mockCli.batchInsertFunc = func(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
		batchCount++
		return fn(&mockBatch{})
	}

	s.softDeleteExpiredAppStates(ctx, now)
	s.softDeleteExpiredUserStates(ctx, now)

	assert.Equal(t, 2, callCount)
	assert.Equal(t, 2, batchCount)
}

func TestService_Cleanup_PhysicalDelete(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient: mockCli,
		opts: ServiceOpts{
			deletedRetention: time.Hour,
		},
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}

	execCount := 0
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		execCount++
		return nil
	}

	s.cleanupDeletedData(context.Background(), time.Now())
	// Expect 5 Exec calls (session states, events, summaries, app states, user states)
	assert.Equal(t, 5, execCount)
}
