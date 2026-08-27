//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package zset

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

func TestClient_GetTrackEventPageCursorUsesDigestAndContinuesSameScore(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "page-zset-same-score"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	baseTime := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		require.NoError(t, c.AppendTrackEvent(ctx, key, &session.TrackEvent{
			Track:     "alpha",
			Payload:   json.RawMessage(fmt.Sprintf(`"secret-%d"`, i)),
			Timestamp: baseTime,
		}))
	}

	first, err := c.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		EventLimit: 1,
	})
	require.NoError(t, err)
	require.Len(t, first.Entries, 1)
	require.True(t, first.HasMore)
	cursor, err := trackpage.Decode(first.Entries[0].Cursor)
	require.NoError(t, err)
	assert.NotContains(t, cursor.ID, "secret")

	second, err := c.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     first.Entries[0].Cursor,
		EventLimit: 10,
	})
	require.NoError(t, err)
	require.Len(t, second.Entries, 2)
	require.False(t, second.HasMore)

	seen := map[string]bool{string(first.Entries[0].Event.Payload): true}
	for _, entry := range second.Entries {
		assert.False(t, seen[string(entry.Event.Payload)])
		seen[string(entry.Event.Payload)] = true
	}
}
