//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestHashIdxTrackCursorAppendOrderAndScope(t *testing.T) {
	mr := miniredis.RunT(t)
	svc, err := NewService(WithRedisClientURL("redis://"+mr.Addr()), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "cursor"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	ts := time.Unix(1700000000, 0)
	appendEvent := func(at time.Time, payload string) {
		t.Helper()
		require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
			Track: "track", Timestamp: at, Payload: json.RawMessage(payload),
		}))
	}
	appendEvent(ts, `"first"`)
	appendEvent(ts.Add(-time.Second), `"backdated"`)
	snapshot, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.NotNil(t, snapshot.Cursor)
	require.Len(t, snapshot.Events, 2)
	require.JSONEq(t, `"first"`, string(snapshot.Events[0].Payload))
	require.JSONEq(t, `"backdated"`, string(snapshot.Events[1].Payload))

	for i := 0; i < 513; i++ {
		appendEvent(ts, `"same"`)
	}
	cursor := *snapshot.Cursor
	for _, size := range []int{512, 1} {
		page, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(cursor))
		require.NoError(t, err)
		require.NotNil(t, page.Cursor)
		require.NotEqual(t, cursor, *page.Cursor)
		require.Len(t, page.Events, size)
		for _, event := range page.Events {
			require.JSONEq(t, `"same"`, string(event.Payload))
		}
		cursor = *page.Cursor
	}
	empty, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(cursor))
	require.NoError(t, err)
	require.Empty(t, empty.Events)
	require.NotNil(t, empty.Cursor)
	require.Equal(t, cursor, *empty.Cursor)

	for _, opts := range [][]session.Option{
		{session.WithTrackCursor("invalid")},
		{session.WithTrackCursor(""), session.WithEventNum(1)},
		{session.WithTrackCursor(*snapshot.Cursor), session.WithEventTime(ts)},
		{session.WithTrackCursor(*snapshot.Cursor), func(o *session.Options) { o.EventPage = &session.EventPage{} }},
	} {
		_, err := svc.GetTrackEvents(ctx, key, "track", opts...)
		require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	}
	_, err = svc.GetTrackEvents(ctx, key, "another-track", session.WithTrackCursor(*snapshot.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	require.NoError(t, svc.DeleteSession(ctx, key))
	_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*snapshot.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	_, err = svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*snapshot.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
}

func TestZSetTrackCursorKeepsLegacySnapshot(t *testing.T) {
	mr := miniredis.RunT(t)
	svc, err := NewService(WithRedisClientURL("redis://"+mr.Addr()), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "legacy"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track: "track", Timestamp: time.Now(), Payload: json.RawMessage(`"legacy"`),
	}))
	legacy, err := svc.GetTrackEvents(ctx, key, "track")
	require.NoError(t, err)
	snapshot, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.Len(t, snapshot.Events, 1)
	require.Equal(t, legacy, snapshot)
	require.Nil(t, snapshot.Cursor)
	_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor("existing"))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
}

func TestHashIdxEmptyCursorSurvivesAppendButNotExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	svc, err := NewService(WithRedisClientURL("redis://"+mr.Addr()), WithCompatMode(CompatModeNone), WithTrackEventTTL(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "empty"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	snapshot, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.Empty(t, snapshot.Events)
	require.NotNil(t, snapshot.Cursor)
	mr.FastForward(750 * time.Millisecond)
	again, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.Equal(t, snapshot.Cursor, again.Cursor)
	mr.FastForward(500 * time.Millisecond)
	_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*snapshot.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid, "a second snapshot must not extend the marker TTL")

	fresh, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.NotNil(t, fresh.Cursor)
	appendEvent := func(payload string) {
		t.Helper()
		require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
			Track: "track", Timestamp: time.Now(), Payload: json.RawMessage(payload),
		}))
	}
	appendEvent(`"first"`)
	page, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*fresh.Cursor))
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	mr.FastForward(2 * time.Second)
	appendEvent(`"replacement"`)
	_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*fresh.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid, "the same sequence cannot revive an expired track")
}
