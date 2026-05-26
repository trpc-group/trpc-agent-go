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

	for _, evt := range []event.Event{
		redisWindowEvent("u1", model.RoleUser, "one"),
		redisWindowEvent("a1", model.RoleAssistant, "two"),
		redisWindowEvent("u2", model.RoleUser, "three"),
		redisWindowEvent("u3", model.RoleUser, "four"),
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
