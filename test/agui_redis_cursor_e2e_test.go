//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/google/uuid"
	r3sse "github.com/r3labs/sse/v2"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session"
	redisservice "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// TestAGUIRedisCursorHistory exercises the public HTTP/SSE history handler against
// a standalone Redis server. Each case uses its own prefix and deletes its session.
func TestAGUIRedisCursorHistory(t *testing.T) {
	url := os.Getenv("TRPC_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set TRPC_TEST_REDIS_URL to run against a standalone Redis server")
	}
	for _, mode := range []redisservice.CompatMode{redisservice.CompatModeNone, redisservice.CompatModeTransition} {
		for _, async := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode=%d/async=%t", mode, async), func(t *testing.T) {
				svc, err := redisservice.NewService(redisservice.WithRedisClientURL(url), redisservice.WithCompatMode(mode), redisservice.WithEnableAsyncPersist(async), redisservice.WithKeyPrefix("cursor-e2e-"+uuid.NewString()))
				require.NoError(t, err)
				server := newAGUIHTTPServerWithOptions(t, &QueueModel{}, svc,
					agui.WithMessagesSnapshotFollowEnabled(true), agui.WithFlushInterval(5*time.Millisecond), agui.WithMessagesSnapshotFollowMaxDuration(5*time.Second))
				ctx := context.Background()
				key := session.Key{AppName: testAppName, UserID: "user", SessionID: "thread"}
				sess, err := svc.CreateSession(ctx, key, session.StateMap{})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, svc.DeleteSession(ctx, key)) })
				timestamp := time.Unix(1700000000, 0)
				appendEvent := func(evt aguievents.Event) {
					raw, err := evt.ToJSON()
					require.NoError(t, err)
					require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "agui", Timestamp: timestamp, Payload: json.RawMessage(raw)}))
				}
				appendEvent(aguievents.NewRunStartedEvent("thread", "original"))
				appendEvent(aguievents.NewTextMessageStartEvent("user-message", aguievents.WithRole("user")))
				appendEvent(aguievents.NewTextMessageContentEvent("user-message", "hello"))
				appendEvent(aguievents.NewTextMessageEndEvent("user-message"))
				require.Eventually(t, func() bool {
					events, err := svc.GetTrackEvents(ctx, key, "agui")
					return err == nil && len(events.Events) == 4
				}, 3*time.Second, time.Millisecond)
				payload := `{"threadId":"thread","runId":"snapshot","messages":[],"forwardedProps":{"userId":"user"}}`
				res := postJSON(t, server.Client(), server.URL+"/history", payload, 10*time.Second)
				reader := r3sse.NewEventStreamReader(res.Body, 1024*1024)
				read := func() aguievents.Event {
					raw, err := reader.ReadEvent()
					require.NoError(t, err)
					data := bytes.TrimSpace(sseData(raw))
					evt, err := aguievents.EventFromJSON(data)
					require.NoError(t, err)
					return evt
				}
				require.IsType(t, &aguievents.RunStartedEvent{}, read())
				require.IsType(t, &aguievents.MessagesSnapshotEvent{}, read())
				// Appending only after observing the snapshot makes this a deterministic
				// cross-poll tie, rather than a timing-dependent batch test.
				for batch := 0; batch < 2; batch++ {
					for i := 0; i < 7; i++ {
						appendEvent(aguievents.NewCustomEvent(fmt.Sprintf("batch-%d-%d", batch, i)))
					}
					for i := 0; i < 7; i++ {
						evt, ok := read().(*aguievents.CustomEvent)
						require.True(t, ok)
						require.Equal(t, fmt.Sprintf("batch-%d-%d", batch, i), evt.Name)
					}
				}
				appendEvent(aguievents.NewRunFinishedEvent("thread", "original"))
				finished, ok := read().(*aguievents.RunFinishedEvent)
				require.True(t, ok)
				require.Equal(t, "snapshot", finished.RunID())
				_, err = reader.ReadEvent()
				require.ErrorIs(t, err, io.EOF)
				// A completed history must terminate immediately on a fresh snapshot too.
				again := postJSON(t, server.Client(), server.URL+"/history", payload, 10*time.Second)
				events := readSSEEvents(t, again.Body)
				require.Len(t, events, 3)
				require.IsType(t, &aguievents.RunFinishedEvent{}, events[2])
				// A cursor must never follow a replacement session with the same ID.
				require.NoError(t, svc.DeleteSession(ctx, key))
				sess, err = svc.CreateSession(ctx, key, session.StateMap{})
				require.NoError(t, err)
				appendEvent(aguievents.NewRunStartedEvent("thread", "replacement"))
				appendEvent(aguievents.NewTextMessageStartEvent("new-user", aguievents.WithRole("user")))
				appendEvent(aguievents.NewTextMessageContentEvent("new-user", "hello again"))
				appendEvent(aguievents.NewTextMessageEndEvent("new-user"))
				require.Eventually(t, func() bool {
					stored, err := svc.GetTrackEvents(ctx, key, "agui")
					return err == nil && len(stored.Events) == 4
				}, 3*time.Second, time.Millisecond)
				replacement := postJSON(t, server.Client(), server.URL+"/history", payload, 10*time.Second)
				reader = r3sse.NewEventStreamReader(replacement.Body, 1024*1024)
				require.IsType(t, &aguievents.RunStartedEvent{}, read())
				require.IsType(t, &aguievents.MessagesSnapshotEvent{}, read())
				require.NoError(t, svc.DeleteSession(ctx, key))
				_, err = svc.CreateSession(ctx, key, session.StateMap{})
				require.NoError(t, err)
				failure, ok := read().(*aguievents.RunErrorEvent)
				require.True(t, ok)
				require.Contains(t, failure.Message, "cursor")

			})
		}
	}
}
