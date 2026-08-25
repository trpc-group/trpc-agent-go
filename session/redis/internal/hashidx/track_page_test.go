//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

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

func TestClient_GetTrackEventPageContinuesSameScore(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "page-hashidx-same-score"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	tracksJSON, err := json.Marshal([]session.Track{"alpha"})
	require.NoError(t, err)
	baseTime := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		require.NoError(t, c.AppendTrackEvent(ctx, key, &session.TrackEvent{
			Track:     "alpha",
			Payload:   json.RawMessage(fmt.Sprintf(`"payload-%d"`, i)),
			Timestamp: baseTime,
		}, tracksJSON))
	}

	first, err := c.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		EventLimit: 1,
	})
	require.NoError(t, err)
	require.Len(t, first.Entries, 1)
	assert.True(t, first.HasMore)
	cursor, err := trackpage.Decode(first.Entries[0].Cursor)
	require.NoError(t, err)
	assert.Equal(t, TrackEventPageCursorKind, cursor.Kind)
	assert.Equal(t, trackpage.TimeToUnixNano(baseTime), cursor.CreatedAt)

	second, err := c.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     first.Entries[0].Cursor,
		EventLimit: 10,
	})
	require.NoError(t, err)
	require.Len(t, second.Entries, 2)
	assert.False(t, second.HasMore)

	seen := map[string]bool{string(first.Entries[0].Event.Payload): true}
	for _, entry := range second.Entries {
		assert.False(t, seen[string(entry.Event.Payload)])
		seen[string(entry.Event.Payload)] = true
	}
}

func TestClient_GetTrackEventPageOrdersDoubleDigitSameScore(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "page-hashidx-double-digit"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	tracksJSON, err := json.Marshal([]session.Track{"alpha"})
	require.NoError(t, err)
	baseTime := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		require.NoError(t, c.AppendTrackEvent(ctx, key, &session.TrackEvent{
			Track:     "alpha",
			Payload:   json.RawMessage(fmt.Sprintf(`"payload-%02d"`, i)),
			Timestamp: baseTime,
		}, tracksJSON))
	}

	var cursor string
	var all []string
	for {
		page, err := c.GetTrackEventPage(ctx, session.TrackEventPageRequest{
			Key:        key,
			Track:      "alpha",
			Cursor:     cursor,
			EventLimit: 5,
		})
		require.NoError(t, err)
		require.NotEmpty(t, page.Entries)
		require.LessOrEqual(t, len(page.Entries), 5)

		payloads := make([]string, 0, len(page.Entries))
		for _, entry := range page.Entries {
			var payload string
			require.NoError(t, json.Unmarshal(entry.Event.Payload, &payload))
			payloads = append(payloads, payload)
		}
		all = append(payloads, all...)
		if !page.HasMore {
			break
		}
		cursor = page.Entries[0].Cursor
	}

	require.Equal(t, []string{
		"payload-00",
		"payload-01",
		"payload-02",
		"payload-03",
		"payload-04",
		"payload-05",
		"payload-06",
		"payload-07",
		"payload-08",
		"payload-09",
		"payload-10",
		"payload-11",
	}, all)
}

func TestClient_GetTrackEventPageRejectsWrongCursorBinding(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "page-hashidx-wrong-cursor"}
	cursor, err := trackpage.CursorForUnixNano(
		TrackEventPageCursorKind,
		key,
		"beta",
		time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC).UnixNano(),
		"1",
	)
	require.NoError(t, err)

	_, err = c.GetTrackEventPage(context.Background(), session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     cursor,
		EventLimit: 1,
	})
	require.Error(t, err)
}
