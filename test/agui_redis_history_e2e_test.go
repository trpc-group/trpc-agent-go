//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session"
	redisservice "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// TestAGUIRedisHistoryCompletesWithTiedEvents reproduces #2702 with an
// already-finished Hashidx run and a tail that crosses the ID digit boundary.
func TestAGUIRedisHistoryCompletesWithTiedEvents(t *testing.T) {
	url := os.Getenv("TRPC_TEST_REDIS_URL")
	if url == "" {
		url = "redis://" + miniredis.RunT(t).Addr()
	}

	svc, err := redisservice.NewService(
		redisservice.WithRedisClientURL(url),
		redisservice.WithCompatMode(redisservice.CompatModeNone),
		redisservice.WithEnableAsyncPersist(false),
		redisservice.WithKeyPrefix("history-e2e-"+rand.Text()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	server := newAGUIHTTPServerWithOptions(t, &QueueModel{}, svc,
		agui.WithMessagesSnapshotFollowEnabled(true),
		agui.WithFlushInterval(10*time.Millisecond),
		agui.WithMessagesSnapshotFollowMaxDuration(200*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key := session.Key{AppName: testAppName, UserID: "user", SessionID: "thread"}
	sess, err := svc.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.DeleteSession(context.Background(), key)) })

	start := time.Unix(1700000000, 0)
	tied := start.Add(time.Second)
	appendEvent := func(ts time.Time, evt aguievents.Event) {
		t.Helper()
		raw, err := evt.ToJSON()
		require.NoError(t, err)
		require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
			Track: "agui", Timestamp: ts, Payload: json.RawMessage(raw),
		}))
	}
	appendEvent(start, aguievents.NewRunStartedEvent("thread", "original"))
	appendEvent(start, aguievents.NewTextMessageStartEvent("user-message", aguievents.WithRole("user")))
	appendEvent(start, aguievents.NewTextMessageContentEvent("user-message", "hello"))
	appendEvent(start, aguievents.NewTextMessageEndEvent("user-message"))
	for i := 0; i < 5; i++ {
		appendEvent(tied, aguievents.NewCustomEvent("progress"))
	}
	appendEvent(tied, aguievents.NewRunFinishedEvent("thread", "original"))
	appendEvent(tied, aguievents.NewCustomEvent("trailing-activity"))

	payload := `{"threadId":"thread","runId":"snapshot","messages":[],"forwardedProps":{"userId":"user"}}`
	res := postJSON(t, server.Client(), server.URL+"/history", payload, 10*time.Second)
	events := readSSEEvents(t, res.Body)
	require.Len(t, events, 3)
	require.IsType(t, &aguievents.RunStartedEvent{}, events[0])
	require.IsType(t, &aguievents.MessagesSnapshotEvent{}, events[1])
	if failure, ok := events[2].(*aguievents.RunErrorEvent); ok {
		t.Fatalf("completed history returned RUN_ERROR: %s", failure.Message)
	}
	finished, ok := events[2].(*aguievents.RunFinishedEvent)
	require.True(t, ok, "expected RUN_FINISHED, got %T", events[2])
	require.Equal(t, "snapshot", finished.RunID())
}
