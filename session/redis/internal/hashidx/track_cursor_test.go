//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestReadTrackCursorValidationAndExpiry(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "cursor-validation"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	timestamp := time.Unix(1700000000, 0)
	require.NoError(t, c.AppendTrackEvent(ctx, key, &session.TrackEvent{Track: "track", Timestamp: timestamp, Payload: json.RawMessage(`1`)}, nil))
	empty := ""
	snapshot, err := c.ReadTrackCursor(ctx, key, "track", &session.Options{TrackCursor: &empty, EventTime: timestamp, EventNum: 1})
	require.NoError(t, err)
	require.Len(t, snapshot.Events, 1)
	require.JSONEq(t, `1`, string(snapshot.Events[0].Payload))
	page, err := c.ReadTrackCursor(ctx, key, "track", &session.Options{TrackCursor: &snapshot.Cursor})
	require.NoError(t, err)
	require.Empty(t, page.Events)
	require.Equal(t, snapshot.Cursor, page.Cursor)
	for _, cursor := range []string{"!", snapshot.Cursor} {
		_, err := c.ReadTrackCursor(ctx, key, "other-track", &session.Options{TrackCursor: &cursor})
		require.Error(t, err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(snapshot.Cursor)
	require.NoError(t, err)
	var fields map[string]string
	require.NoError(t, json.Unmarshal(raw, &fields))
	fields["Position"] = "invalid-position"
	raw, err = json.Marshal(fields)
	require.NoError(t, err)
	invalid := base64.RawURLEncoding.EncodeToString(raw)
	_, err = c.ReadTrackCursor(ctx, key, "track", &session.Options{TrackCursor: &invalid})
	require.ErrorContains(t, err, "position")
	// Removing the backing track must invalidate a nonzero continuation.
	require.NoError(t, rdb.Del(ctx, c.keys.TrackDataKey(key, "track"), c.keys.TrackTimeIndexKey(key, "track")).Err())
	_, err = c.ReadTrackCursor(ctx, key, "track", &session.Options{TrackCursor: &snapshot.Cursor})
	require.ErrorContains(t, err, "expired")
	// An initial cursor read must also fail when session metadata has expired.
	require.NoError(t, rdb.Del(ctx, c.keys.SessionMetaKey(key)).Err())
	_, err = c.ReadTrackCursor(ctx, key, "track", &session.Options{TrackCursor: &empty})
	require.ErrorContains(t, err, "expired")
}
