//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrackCursorEmptySnapshotAcrossTrackExpiry(t *testing.T) {
	for _, mode := range []CompatMode{CompatModeNone, CompatModeTransition} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			mr := miniredis.RunT(t)
			url := os.Getenv("TRPC_TEST_REDIS_URL")
			advance := func() { mr.FastForward(2 * time.Second) }
			if url == "" {
				url = "redis://" + mr.Addr()
			} else {
				advance = func() { time.Sleep(1500 * time.Millisecond) }
			}
			svc, err := NewService(WithRedisClientURL(url), WithKeyPrefix("lifecycle-"+uuid.NewString()), WithCompatMode(mode), WithTrackEventTTL(time.Second))
			require.NoError(t, err)
			defer svc.Close()
			ctx := context.Background()
			key := session.Key{AppName: "app", UserID: "user", SessionID: "review"}
			sess, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			defer svc.DeleteSession(ctx, key)
			snapshot, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
			require.NoError(t, err)
			require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Timestamp: time.Now(), Payload: json.RawMessage(`"lost"`)}))
			advance()
			require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Timestamp: time.Now(), Payload: json.RawMessage(`"survivor"`)}))
			page, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(snapshot.Cursor))
			require.Nil(t, page)
			require.Error(t, err, "expired unread history must fail closed rather than silently dropping the first event")
		})
	}
}

func TestTrackCursorEmptySnapshotLifecycle(t *testing.T) {
	for _, mode := range []CompatMode{CompatModeNone, CompatModeTransition} {
		for _, ttl := range []time.Duration{0, time.Second} {
			t.Run(fmt.Sprintf("mode=%d/ttl=%s", mode, ttl), func(t *testing.T) {
				mr := miniredis.RunT(t)
				svc, err := NewService(WithRedisClientURL("redis://"+mr.Addr()), WithCompatMode(mode), WithTrackEventTTL(ttl))
				require.NoError(t, err)
				defer svc.Close()
				ctx := context.Background()
				key := session.Key{AppName: "app", UserID: "user", SessionID: "empty"}
				_, err = svc.CreateSession(ctx, key, nil)
				require.NoError(t, err)
				before := mr.Keys()
				initial, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
				require.NoError(t, err)
				require.Empty(t, initial.Events)
				var added []string
				for _, k := range mr.Keys() {
					if !slices.Contains(before, k) {
						added = append(added, k)
					}
				}
				require.NotEmpty(t, added, "an empty cursor needs a persisted expiration anchor")
				mr.FastForward(250 * time.Millisecond)
				again, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
				require.NoError(t, err)
				require.Equal(t, initial.Cursor, again.Cursor)
				for _, k := range added {
					if ttl > 0 {
						require.Equal(t, 750*time.Millisecond, mr.TTL(k), "snapshot reads must not refresh %s", k)
					} else {
						require.Zero(t, mr.TTL(k))
					}
				}
				if ttl > 0 {
					mr.FastForward(time.Second)
					_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(initial.Cursor))
					require.ErrorContains(t, err, "expired")
					// A new empty snapshot must not revive the expired token either.
					_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
					require.NoError(t, err)
					_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(initial.Cursor))
					require.ErrorContains(t, err, "expired")
				}
				require.NoError(t, svc.DeleteSession(ctx, key))
				for _, k := range added {
					require.False(t, mr.Exists(k), "deleting an unwritten track must remove %s", k)
				}
			})
		}
	}
}
