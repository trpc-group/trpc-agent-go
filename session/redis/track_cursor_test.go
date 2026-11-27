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
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrackCursorContinuation(t *testing.T) {
	for _, mode := range []CompatMode{CompatModeNone, CompatModeTransition} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			url, cleanup := setupTestRedis(t)
			defer cleanup()
			svc, err := NewService(WithRedisClientURL(url), WithCompatMode(mode))
			require.NoError(t, err)
			defer svc.Close()
			ctx := context.Background()
			key := session.Key{AppName: "app", UserID: "user", SessionID: "cursor"}
			sess, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			initial, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
			require.NoError(t, err)
			require.NotEmpty(t, initial.Cursor)
			timestamp := time.Unix(1700000000, 0)
			for i := 0; i < 520; i++ {
				require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Timestamp: timestamp, Payload: json.RawMessage(fmt.Sprint(i))}))
			}
			page, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(initial.Cursor), session.WithEventTime(time.Now()), session.WithEventNum(1))
			require.NoError(t, err)
			require.Len(t, page.Events, 512)
			for i, evt := range page.Events {
				require.Equal(t, fmt.Sprint(i), string(evt.Payload))
			}
			page, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(page.Cursor))
			require.NoError(t, err)
			require.Len(t, page.Events, 8)
			for i, evt := range page.Events {
				require.Equal(t, fmt.Sprint(i+512), string(evt.Payload))
			}
			token := page.Cursor
			page, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(token))
			require.NoError(t, err)
			require.Empty(t, page.Events)
			_, err = svc.GetTrackEvents(ctx, key, "another", session.WithTrackCursor(token))
			require.Error(t, err)
			_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor("bad-token"))
			require.Error(t, err)
			require.NoError(t, svc.DeleteSession(ctx, key))
			_, err = svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(token))
			require.Error(t, err)
		})
	}
}

func TestTrackCursorConcurrentAppends(t *testing.T) {
	for _, mode := range []CompatMode{CompatModeNone, CompatModeTransition} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			url, cleanup := setupTestRedis(t)
			defer cleanup()
			svc, err := NewService(WithRedisClientURL(url), WithCompatMode(mode))
			require.NoError(t, err)
			defer svc.Close()
			ctx := context.Background()
			key := session.Key{AppName: "app", UserID: "user", SessionID: "concurrent"}
			sess, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			snapshot, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
			require.NoError(t, err)
			var wg sync.WaitGroup
			errs := make(chan error, 40)
			for i := 0; i < 40; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					errs <- svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Timestamp: time.Unix(1700000000, 0), Payload: json.RawMessage(fmt.Sprint(i))})
				}(i)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			page, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(snapshot.Cursor))
			require.NoError(t, err)
			require.Len(t, page.Events, 40)
			seen := map[string]bool{}
			for _, evt := range page.Events {
				require.False(t, seen[string(evt.Payload)])
				seen[string(evt.Payload)] = true
			}
			snapshot, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
			require.NoError(t, err)
			require.Equal(t, page.Events, snapshot.Events)
		})
	}
}

func TestTrackCursorExpiration(t *testing.T) {
	for _, mode := range []CompatMode{CompatModeNone, CompatModeTransition} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			mr := miniredis.RunT(t)
			svc, err := NewService(WithRedisClientURL("redis://"+mr.Addr()), WithCompatMode(mode), WithTrackEventTTL(time.Second))
			require.NoError(t, err)
			defer svc.Close()
			ctx := context.Background()
			key := session.Key{AppName: "app", UserID: "user", SessionID: "expiry"}
			sess, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			evt := &session.TrackEvent{Track: "track", Timestamp: time.Unix(1700000000, 0), Payload: json.RawMessage(`1`)}
			require.NoError(t, svc.AppendTrackEvent(ctx, sess, evt))
			snapshot, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
			require.NoError(t, err)
			mr.FastForward(2 * time.Second)
			// A new track may reuse sequence numbers, but must not revive the old cursor.
			require.NoError(t, svc.AppendTrackEvent(ctx, sess, evt))
			_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(snapshot.Cursor))
			require.Error(t, err)
		})
	}
}

func TestZSetTrackCursorLegacyCompatibility(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()
	svc, err := NewService(WithRedisClientURL(url), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svc.Close()
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "legacy"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	timestamp := time.Unix(1700000000, 0)
	legacy := session.TrackEvent{Track: "track", Timestamp: timestamp, Payload: json.RawMessage(`"legacy"`)}
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, svc.redisClient.ZAdd(ctx, "track:{app}:user:legacy:track", goredis.Z{Score: float64(timestamp.UnixNano()), Member: raw}).Err())
	snapshot, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.Len(t, snapshot.Events, 1)
	for i := 0; i < 12; i++ {
		require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Timestamp: timestamp, Payload: json.RawMessage(fmt.Sprint(i))}))
	}
	page, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(snapshot.Cursor))
	require.NoError(t, err)
	require.Len(t, page.Events, 12)
	for i, evt := range page.Events {
		require.Equal(t, fmt.Sprint(i), string(evt.Payload))
	}
	latest, err := svc.GetTrackEvents(ctx, key, "track", session.WithEventNum(1))
	require.NoError(t, err)
	require.Equal(t, "11", string(latest.Events[0].Payload))
	// An old writer adding an unsequenced event must fail closed during follow.
	legacy.Payload = json.RawMessage(`"old writer"`)
	raw, err = json.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, svc.redisClient.ZAdd(ctx, "track:{app}:user:legacy:track", goredis.Z{Score: float64(timestamp.UnixNano()), Member: raw}).Err())
	_, err = svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(page.Cursor))
	require.ErrorContains(t, err, "legacy history changed")
	// The continuation index must not collide with another valid track name.
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track:cursor", Timestamp: timestamp, Payload: json.RawMessage(`42`)}))
	other, err := svc.GetTrackEvents(ctx, key, "track:cursor")
	require.NoError(t, err)
	require.Len(t, other.Events, 1)
	require.Equal(t, "42", string(other.Events[0].Payload))
}

func TestZSetTrackCursorPreservesSequenceAfterLegacyStateWrite(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()
	svc, err := NewService(WithRedisClientURL(url), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svc.Close()
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "legacy-state"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := &session.TrackEvent{Track: "track", Timestamp: time.Unix(1700000000, 0), Payload: json.RawMessage(`1`)}
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, evt))
	snapshot, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	raw, err := svc.redisClient.HGet(ctx, "sess:{app}:user", key.SessionID).Bytes()
	require.NoError(t, err)
	var metadata map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &metadata))
	delete(metadata, "trackSequence")
	raw, err = json.Marshal(metadata)
	require.NoError(t, err)
	require.NoError(t, svc.redisClient.HSet(ctx, "sess:{app}:user", key.SessionID, raw).Err())
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, evt))
	page, err := svc.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(snapshot.Cursor))
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	all, err := svc.GetTrackEvents(ctx, key, "track")
	require.NoError(t, err)
	require.Len(t, all.Events, 2)
}
