//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestAppendEventHook(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	t.Run("hook modifies event before storage", func(t *testing.T) {
		hookCalled := false
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				hookCalled = true
				ctx.Event.Tag = "hook_processed"
				return next()
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		// Add a user message first
		userEvt := event.New("inv0", "user")
		userEvt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "Hello"}}},
		}
		err = service.AppendEvent(ctx, sess, userEvt)
		require.NoError(t, err)

		// Then add assistant message
		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hi there"}}},
		}

		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
		assert.True(t, hookCalled)

		// Verify the event was modified by hook
		assert.Equal(t, "hook_processed", evt.Tag)

		// Verify storage
		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		require.Len(t, retrieved.Events, 2)
	})

	t.Run("hook can abort event storage", func(t *testing.T) {
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				return nil // skip next()
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app2", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)

		// Event should not be stored since hook aborted
		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		assert.Len(t, retrieved.Events, 0)
	})

	t.Run("multiple hooks execute in order", func(t *testing.T) {
		order := []string{}
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				order = append(order, "hook1_before")
				err := next()
				order = append(order, "hook1_after")
				return err
			}),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				order = append(order, "hook2_before")
				err := next()
				order = append(order, "hook2_after")
				return err
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app3", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)

		assert.Equal(t, []string{"hook1_before", "hook2_before", "hook2_after", "hook1_after"}, order)
	})
}

func TestGetSessionHook(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	t.Run("hook modifies session after retrieval", func(t *testing.T) {
		hookCalled := false
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				hookCalled = true
				sess, err := next()
				if err != nil || sess == nil {
					return sess, err
				}
				sess.State["hook_added"] = []byte("true")
				return sess, nil
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app4", UserID: "user", SessionID: "sess"}
		_, err = service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		assert.True(t, hookCalled)
		assert.Equal(t, []byte("true"), retrieved.State["hook_added"])
	})

	t.Run("hook can filter events", func(t *testing.T) {
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				sess, err := next()
				if err != nil || sess == nil {
					return sess, err
				}
				filtered := make([]event.Event, 0)
				for _, e := range sess.Events {
					if e.Tag != "skip" {
						filtered = append(filtered, e)
					}
				}
				sess.Events = filtered
				return sess, nil
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app5", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		evt1 := event.New("inv1", "user")
		evt1.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "Q1"}}},
		}
		evt1.Tag = "skip"
		err = service.AppendEvent(ctx, sess, evt1)
		require.NoError(t, err)

		evt2 := event.New("inv2", "assistant")
		evt2.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "A1"}}},
		}
		err = service.AppendEvent(ctx, sess, evt2)
		require.NoError(t, err)

		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		assert.Len(t, retrieved.Events, 1)
		assert.Equal(t, "A1", retrieved.Events[0].Response.Choices[0].Message.Content)
	})

	t.Run("multiple hooks execute in order", func(t *testing.T) {
		order := []string{}
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				order = append(order, "hook1_before")
				sess, err := next()
				order = append(order, "hook1_after")
				return sess, err
			}),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				order = append(order, "hook2_before")
				sess, err := next()
				order = append(order, "hook2_after")
				return sess, err
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app6", UserID: "user", SessionID: "sess"}
		_, err = service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		_, err = service.GetSession(ctx, key)
		require.NoError(t, err)

		assert.Equal(t, []string{"hook1_before", "hook2_before", "hook2_after", "hook1_after"}, order)
	})
}
