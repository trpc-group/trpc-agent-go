//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redis

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
)

func TestService_GetEventWindow(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	for _, evt := range []event.Event{
		redisWindowEvent("u1", model.RoleUser, "one"),
		redisWindowEvent("a1", model.RoleAssistant, "two"),
		redisWindowEvent("u2", model.RoleUser, "three"),
		redisWindowToolEvent("t1", "calc", "four"),
		redisWindowEvent("u3", model.RoleUser, "five"),
	} {
		evt := evt
		require.NoError(t, svc.AppendEvent(ctx, sess, &evt))
	}

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "u2",
		Before:        1,
		After:         1,
		Roles:         []model.Role{model.RoleUser},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2", "u3"}, redisWindowIDs(got))
}

func TestService_GetEventWindowZSetStorage(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-zset"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	for idx, evt := range []event.Event{
		redisWindowEvent("u1", model.RoleUser, "one"),
		redisWindowEvent("a1", model.RoleAssistant, "two"),
		redisWindowEvent("u2", model.RoleUser, "three"),
		redisWindowEvent("u3", model.RoleUser, "four"),
	} {
		evt := evt
		evt.Timestamp = base.Add(time.Duration(idx) * time.Minute)
		require.NoError(t, svc.AppendEvent(ctx, sess, &evt))
	}

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "u2",
		Before:        1,
		After:         1,
		Roles:         []model.Role{model.RoleUser},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2", "u3"}, redisWindowIDs(got))
}

func TestService_GetEventWindowZSetAnchorFilteredByRole(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-zset-filtered"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("anchor", model.RoleAssistant, "answer")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		Roles:         []model.Role{model.RoleUser},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestService_GetEventWindowZSetAnchorNotFound(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-zset-missing"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("u1", model.RoleUser, "one")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestService_GetEventWindowAnchorNotFound(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("u1", model.RoleUser, "one")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestService_GetEventWindowAnchorFilteredByRole(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-filtered"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("anchor", model.RoleAssistant, "answer")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		Roles:         []model.Role{model.RoleUser},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestService_GetEventWindowHashIdxAnchorIndexMissing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-missing-index"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("anchor", model.RoleUser, "one")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	eventTimeIndexKey := hashidx.GetEventTimeIndexKey(svc.opts.keyPrefix, key)
	require.NoError(t, svc.redisClient.ZRem(ctx, eventTimeIndexKey, "anchor").Err())

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestService_GetEventWindowValidation(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	svc := &Service{}

	_, err := svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key: key,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event id is required")

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		Before:        -1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "before >= 0")

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		Before:        eventWindowScanCap,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "scan cap")

	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		Before:        math.MaxInt,
		After:         math.MaxInt,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "scan cap")
}

func TestService_GetEventWindowMissingSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "missing"}
	_, err = svc.GetEventWindow(context.Background(), session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestService_GetEventWindowTrimsAnchorID(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-trim"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("anchor", model.RoleUser, "one")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: " anchor ",
	})
	require.NoError(t, err)
	require.Equal(t, "anchor", got.AnchorEventID)
	require.Equal(t, []string{"anchor"}, redisWindowIDs(got))
}

func TestService_GetEventWindowIgnoresMalformedHashIdxRowOutsideWindow(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-bad-row"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("anchor", model.RoleUser, "one")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	eventDataKey := hashidx.GetEventDataKey(svc.opts.keyPrefix, key)
	eventTimeIndexKey := hashidx.GetEventTimeIndexKey(svc.opts.keyPrefix, key)
	require.NoError(t, svc.redisClient.HSet(ctx, eventDataKey, "bad", "{bad-json").Err())
	require.NoError(t, svc.redisClient.ZAdd(ctx, eventTimeIndexKey, goredis.Z{
		Score:  float64(time.Now().UTC().Add(time.Hour).UnixNano()),
		Member: "bad",
	}).Err())

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"anchor"}, redisWindowIDs(got))
}

func TestService_GetEventWindowMalformedHashIdxNeighbor(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-bad-neighbor"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("anchor", model.RoleUser, "one")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	eventDataKey := hashidx.GetEventDataKey(svc.opts.keyPrefix, key)
	eventTimeIndexKey := hashidx.GetEventTimeIndexKey(svc.opts.keyPrefix, key)
	require.NoError(t, svc.redisClient.HSet(ctx, eventDataKey, "bad", "{bad-json").Err())
	require.NoError(t, svc.redisClient.ZAdd(ctx, eventTimeIndexKey, goredis.Z{
		Score:  float64(time.Now().UTC().Add(time.Hour).UnixNano()),
		Member: "bad",
	}).Err())

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		After:         1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal redis event window entry")
}

func TestService_GetEventWindowMalformedZSetNeighbor(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess-zset-bad-neighbor"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	evt := redisWindowEvent("anchor", model.RoleUser, "one")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	require.NoError(t, svc.redisClient.ZAdd(ctx, redisZSetEventKey(svc.opts.keyPrefix, key), goredis.Z{
		Score:  float64(time.Now().UTC().Add(time.Hour).UnixNano()),
		Member: "{bad-json",
	}).Err())

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "anchor",
		After:         1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal redis event window entry")
}

func TestRedisWindowHelpers(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	require.Equal(t, "event:{app}:user:sess", redisZSetEventKey("", key))
	require.Equal(t, "prefix:event:{app}:user:sess", redisZSetEventKey("prefix", key))

	entry, ok, err := redisWindowEntryFromValue(nil, 1)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, entry)

	raw := redisWindowJSON(t, redisWindowEvent("evt", model.RoleUser, "one"))
	entry, ok, err = redisWindowEntryFromValue([]byte(raw), 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(2), entry.rank)
	require.Equal(t, "evt", entry.entry.Event.ID)

	_, _, err = redisWindowEntryFromValue(123, 3)
	require.Error(t, err)

	_, err = redisWindowEntryFromJSON("{bad-json", 4)
	require.Error(t, err)

	entries := []session.EventWindowEntry{
		{Event: event.Event{ID: "first"}},
		{Event: event.Event{ID: "second"}},
	}
	reverseRedisWindowEntries(entries)
	require.Equal(t, "second", entries[0].Event.ID)
	require.Equal(t, "first", entries[1].Event.ID)
}

func redisWindowEvent(id string, role model.Role, content string) event.Event {
	return event.Event{
		ID:        id,
		Timestamp: time.Now().UTC(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    role,
					Content: content,
				},
			}},
		},
	}
}

func redisWindowToolEvent(id, toolName, content string) event.Event {
	evt := redisWindowEvent(id, model.RoleTool, content)
	evt.Response.Choices[0].Message.ToolID = "call-" + id
	evt.Response.Choices[0].Message.ToolName = toolName
	return evt
}

func redisWindowIDs(window *session.EventWindow) []string {
	ids := make([]string, 0, len(window.Entries))
	for _, entry := range window.Entries {
		ids = append(ids, entry.Event.ID)
	}
	return ids
}

func redisWindowJSON(t *testing.T, evt event.Event) string {
	t.Helper()
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	return string(data)
}
