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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_TrimConversations(t *testing.T) {
	tests := []struct {
		name           string
		setupEvents    func(t *testing.T, service *Service, key session.Key) []event.Event
		trimOptions    []TrimConversationOption
		expectedCount  int
		expectedReqIDs []string
		wantErr        bool
		errContains    string
	}{
		{
			name: "trim single conversation by default",
			setupEvents: func(t *testing.T, service *Service, key session.Key) []event.Event {
				ctx := context.Background()
				sess, err := service.CreateSession(ctx, key, nil)
				require.NoError(t, err)

				events := []event.Event{
					{ID: "e1", RequestID: "req1", Timestamp: time.Now().Add(-2 * time.Hour)},
					{ID: "e2", RequestID: "req1", Timestamp: time.Now().Add(-1 * time.Hour)},
					{ID: "e3", RequestID: "req2", Timestamp: time.Now()},
				}
				for i := range events {
					events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
					require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
				}
				return events
			},
			trimOptions:    nil,
			expectedCount:  1,
			expectedReqIDs: []string{"req2"},
			wantErr:        false,
		},
		{
			name: "trim multiple conversations",
			setupEvents: func(t *testing.T, service *Service, key session.Key) []event.Event {
				ctx := context.Background()
				sess, err := service.CreateSession(ctx, key, nil)
				require.NoError(t, err)

				events := []event.Event{
					{ID: "e1", RequestID: "req1", Timestamp: time.Now().Add(-3 * time.Hour)},
					{ID: "e2", RequestID: "req2", Timestamp: time.Now().Add(-2 * time.Hour)},
					{ID: "e3", RequestID: "req3", Timestamp: time.Now().Add(-1 * time.Hour)},
					{ID: "e4", RequestID: "req4", Timestamp: time.Now()},
				}
				for i := range events {
					events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
					require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
				}
				return events
			},
			trimOptions:    []TrimConversationOption{WithCount(2)},
			expectedCount:  2,
			expectedReqIDs: []string{"req3", "req4"},
			wantErr:        false,
		},
		{
			name: "trim conversation with multiple events per request",
			setupEvents: func(t *testing.T, service *Service, key session.Key) []event.Event {
				ctx := context.Background()
				sess, err := service.CreateSession(ctx, key, nil)
				require.NoError(t, err)

				events := []event.Event{
					{ID: "e1", RequestID: "req1", Timestamp: time.Now().Add(-4 * time.Hour)},
					{ID: "e2", RequestID: "req1", Timestamp: time.Now().Add(-3 * time.Hour)},
					{ID: "e3", RequestID: "req2", Timestamp: time.Now().Add(-2 * time.Hour)},
					{ID: "e4", RequestID: "req2", Timestamp: time.Now().Add(-1 * time.Hour)},
					{ID: "e5", RequestID: "req2", Timestamp: time.Now()},
				}
				for i := range events {
					events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
					require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
				}
				return events
			},
			trimOptions:    []TrimConversationOption{WithCount(1)},
			expectedCount:  3,
			expectedReqIDs: []string{"req2", "req2", "req2"},
			wantErr:        false,
		},
		{
			name: "empty events returns nil",
			setupEvents: func(t *testing.T, service *Service, key session.Key) []event.Event {
				ctx := context.Background()
				_, err := service.CreateSession(ctx, key, nil)
				require.NoError(t, err)
				return nil
			},
			trimOptions:    nil,
			expectedCount:  0,
			expectedReqIDs: nil,
			wantErr:        false,
		},
		{
			name: "events without RequestID are skipped",
			setupEvents: func(t *testing.T, service *Service, key session.Key) []event.Event {
				ctx := context.Background()
				sess, err := service.CreateSession(ctx, key, nil)
				require.NoError(t, err)

				events := []event.Event{
					{ID: "e1", RequestID: "", Timestamp: time.Now().Add(-2 * time.Hour)},
					{ID: "e2", RequestID: "req1", Timestamp: time.Now().Add(-1 * time.Hour)},
					{ID: "e3", RequestID: "", Timestamp: time.Now()},
				}
				for i := range events {
					events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
					require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
				}
				return events
			},
			trimOptions:    nil,
			expectedCount:  1,
			expectedReqIDs: []string{"req1"},
			wantErr:        false,
		},
		{
			name: "zero conversation count defaults to 1",
			setupEvents: func(t *testing.T, service *Service, key session.Key) []event.Event {
				ctx := context.Background()
				sess, err := service.CreateSession(ctx, key, nil)
				require.NoError(t, err)

				events := []event.Event{
					{ID: "e1", RequestID: "req1", Timestamp: time.Now().Add(-1 * time.Hour)},
					{ID: "e2", RequestID: "req2", Timestamp: time.Now()},
				}
				for i := range events {
					events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
					require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
				}
				return events
			},
			trimOptions:    []TrimConversationOption{WithCount(0)},
			expectedCount:  1,
			expectedReqIDs: []string{"req2"},
			wantErr:        false,
		},
		{
			name: "negative conversation count defaults to 1",
			setupEvents: func(t *testing.T, service *Service, key session.Key) []event.Event {
				ctx := context.Background()
				sess, err := service.CreateSession(ctx, key, nil)
				require.NoError(t, err)

				events := []event.Event{
					{ID: "e1", RequestID: "req1", Timestamp: time.Now().Add(-1 * time.Hour)},
					{ID: "e2", RequestID: "req2", Timestamp: time.Now()},
				}
				for i := range events {
					events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
					require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
				}
				return events
			},
			trimOptions:    []TrimConversationOption{WithCount(-5)},
			expectedCount:  1,
			expectedReqIDs: []string{"req2"},
			wantErr:        false,
		},
		{
			name: "trim more than available conversations",
			setupEvents: func(t *testing.T, service *Service, key session.Key) []event.Event {
				ctx := context.Background()
				sess, err := service.CreateSession(ctx, key, nil)
				require.NoError(t, err)

				events := []event.Event{
					{ID: "e1", RequestID: "req1", Timestamp: time.Now()},
				}
				for i := range events {
					events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
					require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
				}
				return events
			},
			trimOptions:    []TrimConversationOption{WithCount(10)},
			expectedCount:  1,
			expectedReqIDs: []string{"req1"},
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(
				WithRedisClientURL(redisURL),
				WithSessionTTL(time.Hour),
			)
			require.NoError(t, err)
			defer service.Close()

			key := session.Key{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: fmt.Sprintf("sess_%s", tt.name),
			}

			tt.setupEvents(t, service, key)

			deleted, err := service.TrimConversations(context.Background(), key, tt.trimOptions...)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Len(t, deleted, tt.expectedCount)

			if tt.expectedReqIDs != nil {
				for i, evt := range deleted {
					assert.Equal(t, tt.expectedReqIDs[i], evt.RequestID, "event %d has wrong RequestID", i)
				}
			}
		})
	}
}

func TestService_TrimConversations_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	tests := []struct {
		name        string
		key         session.Key
		errContains string
	}{
		{
			name:        "empty app name",
			key:         session.Key{AppName: "", UserID: "user", SessionID: "sess"},
			errContains: "appName",
		},
		{
			name:        "empty user id",
			key:         session.Key{AppName: "app", UserID: "", SessionID: "sess"},
			errContains: "userID",
		},
		{
			name:        "empty session id",
			key:         session.Key{AppName: "app", UserID: "user", SessionID: ""},
			errContains: "sessionID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.TrimConversations(context.Background(), tt.key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestService_TrimConversations_ChronologicalOrder(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL), WithSessionTTL(time.Hour))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess_chrono"}

	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Create events with known timestamps.
	baseTime := time.Now()
	events := []event.Event{
		{ID: "e1", RequestID: "req1", Timestamp: baseTime.Add(-3 * time.Second)},
		{ID: "e2", RequestID: "req1", Timestamp: baseTime.Add(-2 * time.Second)},
		{ID: "e3", RequestID: "req1", Timestamp: baseTime.Add(-1 * time.Second)},
	}
	for i := range events {
		events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
		require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
	}

	deleted, err := service.TrimConversations(ctx, key)
	require.NoError(t, err)
	require.Len(t, deleted, 3)

	// Verify chronological order (oldest first).
	assert.Equal(t, "e1", deleted[0].ID)
	assert.Equal(t, "e2", deleted[1].ID)
	assert.Equal(t, "e3", deleted[2].ID)
}

func TestService_TrimConversations_RemainingEvents(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL), WithSessionTTL(time.Hour))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess_remain"}

	sess, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Create 3 conversations.
	events := []event.Event{
		{ID: "e1", RequestID: "req1", Timestamp: time.Now().Add(-3 * time.Hour)},
		{ID: "e2", RequestID: "req2", Timestamp: time.Now().Add(-2 * time.Hour)},
		{ID: "e3", RequestID: "req3", Timestamp: time.Now().Add(-1 * time.Hour)},
	}
	for i := range events {
		events[i].Response = &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}}}}
		require.NoError(t, service.AppendEvent(ctx, sess, &events[i]))
	}

	// Trim 1 conversation (the most recent one: req3).
	deleted, err := service.TrimConversations(ctx, key, WithCount(1))
	require.NoError(t, err)
	require.Len(t, deleted, 1)
	assert.Equal(t, "req3", deleted[0].RequestID)

	// Verify remaining events.
	sess2, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, sess2.Events, 2)
	assert.Equal(t, "e1", sess2.Events[0].ID)
	assert.Equal(t, "e2", sess2.Events[1].ID)
}
