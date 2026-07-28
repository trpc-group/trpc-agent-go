//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
)

func TestInitDBTreatsConcurrentTrackIdentityMigrationAsSuccess(t *testing.T) {
	var identityChecks int
	var migrations int
	client := &mockClient{
		queryFunc: func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
			require.Contains(t, query, "FROM system.tables")
			identityChecks++
			if identityChecks == 1 {
				return newMockRows([][]any{{uint64(0)}}), nil
			}
			return newMockRows([][]any{{uint64(1)}}), nil
		},
		execFunc: func(_ context.Context, query string, _ ...any) error {
			if strings.Contains(query, "ADD COLUMN event_id") {
				migrations++
				return errors.New("concurrent alter")
			}
			return nil
		},
	}
	service := &Service{
		chClient:                client,
		tableSessionEvents:      "session_events",
		tableSessionTrackEvents: "session_track_events",
		opts:                    ServiceOpts{tablePrefix: ""},
	}

	require.NoError(t, service.initDB(context.Background()))
	require.Equal(t, 1, migrations)
	require.Equal(t, 2, identityChecks)
}
