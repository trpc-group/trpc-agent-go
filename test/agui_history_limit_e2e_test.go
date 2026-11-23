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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	redisclient "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session"
	redissession "trpc.group/trpc-go/trpc-agent-go/session/redis"
	redisstorage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
)

// Set TRPC_TEST_REDIS_URL to run the same regression against a real Redis server.
// Each run owns a unique key prefix and deletes only its own session.
func TestAGUIRedisSnapshotMaxTrackEvents(t *testing.T) {
	store, hook := newSnapshotRedisStore(t)
	ctx := context.Background()
	key := session.Key{AppName: testAppName, UserID: "user", SessionID: "long-thread"}
	sess, err := store.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.DeleteSession(ctx, key)) }()

	const turns = 200
	const eventsPerTurn = 7
	base := time.Now().Add(-time.Hour)
	for turn := 0; turn < turns; turn++ {
		userID, assistantID := fmt.Sprintf("user-%d", turn), fmt.Sprintf("assistant-%d", turn)
		events := []aguievents.Event{
			aguievents.NewTextMessageStartEvent(userID, aguievents.WithRole("user")),
			aguievents.NewTextMessageContentEvent(userID, "question"),
			aguievents.NewTextMessageEndEvent(userID),
			aguievents.NewTextMessageStartEvent(assistantID, aguievents.WithRole("assistant")),
			aguievents.NewTextMessageContentEvent(assistantID, strings.Repeat("answer ", 150)),
			aguievents.NewTextMessageEndEvent(assistantID),
			aguievents.NewRunFinishedEvent(key.SessionID, fmt.Sprintf("run-%d", turn)),
		}
		for i, evt := range events {
			payload, err := json.Marshal(evt)
			require.NoError(t, err)
			seq := turn*eventsPerTurn + i
			require.NoError(t, store.AppendTrackEvent(ctx, sess, &session.TrackEvent{
				Track:     "agui",
				Timestamp: base.Add(time.Duration(seq) * time.Millisecond), Payload: payload,
			}))
		}
	}

	for _, tc := range []struct {
		name         string
		limit        int
		wantEvents   int
		wantMessages int
	}{
		{"default", 0, turns * eventsPerTurn, turns * 2},
		{"exact_boundary", 7, 7, 2},
		{"orphan_prefix", 8, 8, 2},
		{"no_user_boundary", 1, 1, 0},
		{"multiple_payload_batches", 700, 700, 200},
		{"larger_than_history", 2000, turns * eventsPerTurn, turns * 2},
	} {
		// Own all servers in the parent test so their shared store stays open.
		server := newAGUIHTTPServerWithOptions(t, &QueueModel{}, store,
			agui.WithMessagesSnapshotMaxTrackEvents(tc.limit))
		t.Run(tc.name, func(t *testing.T) {
			hook.reset()
			res := postJSON(t, server.Client(), server.URL+"/history",
				`{"threadId":"long-thread","runId":"history"}`, 30*time.Second)
			require.Equal(t, http.StatusOK, res.StatusCode)
			events := readSSEEvents(t, res.Body)
			require.Len(t, events, 3)
			require.NoError(t, aguievents.ValidateSequence(events))
			snapshot, ok := events[1].(*aguievents.MessagesSnapshotEvent)
			require.True(t, ok)
			require.Len(t, snapshot.Messages, tc.wantMessages)
			firstTurn := turns - tc.wantMessages/2
			for i := 0; i < tc.wantMessages/2; i++ {
				require.Equal(t, fmt.Sprintf("user-%d", firstTurn+i), snapshot.Messages[2*i].ID)
				require.Equal(t, fmt.Sprintf("assistant-%d", firstTurn+i), snapshot.Messages[2*i+1].ID)
			}
			if tc.wantMessages > 0 {
				last := snapshot.Messages[len(snapshot.Messages)-2:]
				content, ok := last[1].ContentString()
				require.True(t, ok)
				require.Equal(t, strings.Repeat("answer ", 150), content)
			}
			// Inspect the Lua result, not just the final snapshot: slicing after
			// an unbounded Redis read would still risk the reported OOM.
			require.Equal(t, []snapshotRead{{limit: tc.limit, count: tc.wantEvents}}, hook.reads())
		})
	}
}

// This smoke test makes real model calls only when explicitly enabled. Configure
// OPENAI_BASE_URL and OPENAI_API_KEY as usual, and set TRPC_TEST_AGUI_MODEL to the
// model name. TRPC_TEST_REDIS_URL optionally selects a real Redis server.
func TestAGUIRedisSnapshotMaxTrackEventsLiveModel(t *testing.T) {
	modelName := os.Getenv("TRPC_TEST_AGUI_MODEL")
	if modelName == "" {
		t.Skip("set TRPC_TEST_AGUI_MODEL to enable live model smoke testing")
	}
	store, hook := newSnapshotRedisStore(t)
	ctx := context.Background()
	key := session.Key{AppName: testAppName, UserID: "user", SessionID: "live-thread"}
	defer func() { require.NoError(t, store.DeleteSession(ctx, key)) }()
	server := newAGUIHTTPServerWithOptions(t, openai.New(modelName), store)
	var counts []int
	for turn := 0; turn < 2; turn++ {
		payload := fmt.Sprintf(`{"threadId":"live-thread","runId":"run-%d","messages":[{"id":"user-%d","role":"user","content":"Reply with just OK."}]}`, turn, turn)
		res := postJSON(t, server.Client(), server.URL+"/agui", payload, 120*time.Second)
		require.Equal(t, http.StatusOK, res.StatusCode)
		events := readSSEEvents(t, res.Body)
		require.NotEmpty(t, events)
		require.NoError(t, aguievents.ValidateSequence(events))
		require.IsType(t, &aguievents.RunFinishedEvent{}, events[len(events)-1])
		persisted, err := store.GetTrackEvents(ctx, key, "agui")
		require.NoError(t, err)
		counts = append(counts, len(persisted.Events))
	}
	// Derive a window containing exactly the second run, independently of the
	// provider's chunking and the history aggregator's flush schedule.
	limit := counts[1] - counts[0]
	require.Positive(t, counts[0])
	require.Positive(t, limit)
	history := newAGUIHTTPServerWithOptions(t, &QueueModel{}, store,
		agui.WithMessagesSnapshotMaxTrackEvents(limit))
	hook.reset()
	res := postJSON(t, history.Client(), history.URL+"/history",
		`{"threadId":"live-thread","runId":"history"}`, 30*time.Second)
	require.Equal(t, http.StatusOK, res.StatusCode)
	events := readSSEEvents(t, res.Body)
	require.Len(t, events, 3)
	require.NoError(t, aguievents.ValidateSequence(events))
	snapshot, ok := events[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	var userIDs []string
	var assistantText string
	for _, message := range snapshot.Messages {
		if message.Role == "user" {
			userIDs = append(userIDs, message.ID)
		}
		if message.Role == "assistant" {
			content, _ := message.ContentString()
			assistantText += content
		}
	}
	require.Equal(t, []string{"user-1"}, userIDs)
	require.NotEmpty(t, assistantText)
	require.Equal(t, []snapshotRead{{limit: limit, count: limit}}, hook.reads())
	t.Logf("live model smoke: stored %d track events, read %d, restored %d messages", counts[1], limit, len(snapshot.Messages))
}

func newSnapshotRedisStore(t *testing.T) (*redissession.Service, *snapshotReadHook) {
	t.Helper()
	redisURL := os.Getenv("TRPC_TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://" + miniredis.RunT(t).Addr()
	}
	hook := &snapshotReadHook{}
	builder := redisstorage.GetClientBuilder()
	redisstorage.SetClientBuilder(func(opts ...redisstorage.ClientBuilderOpt) (redisclient.UniversalClient, error) {
		client, err := builder(opts...)
		if err == nil {
			client.AddHook(hook)
		}
		return client, err
	})
	t.Cleanup(func() { redisstorage.SetClientBuilder(builder) })
	store, err := redissession.NewService(
		redissession.WithRedisClientURL(redisURL),
		redissession.WithCompatMode(redissession.CompatModeNone),
		redissession.WithKeyPrefix("agui-history-limit-"+uuid.NewString()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store, hook
}

type snapshotRead struct {
	limit int
	count int
}

type snapshotReadHook struct {
	mu      sync.Mutex
	results []snapshotRead
}

func (h *snapshotReadHook) DialHook(next redisclient.DialHook) redisclient.DialHook {
	return next
}

func (h *snapshotReadHook) ProcessPipelineHook(next redisclient.ProcessPipelineHook) redisclient.ProcessPipelineHook {
	return next
}

func (h *snapshotReadHook) ProcessHook(next redisclient.ProcessHook) redisclient.ProcessHook {
	return func(ctx context.Context, cmd redisclient.Cmder) error {
		err := next(ctx, cmd)
		args := cmd.Args()
		if err != nil || (cmd.Name() != "eval" && cmd.Name() != "evalsha") || len(args) != 8 ||
			!strings.Contains(fmt.Sprint(args[3]), ":trkdata:") {
			return err
		}
		result, ok := cmd.(*redisclient.Cmd)
		if !ok {
			return err
		}
		values, ok := result.Val().([]any)
		if !ok {
			return err
		}
		limit, ok := args[7].(int)
		if ok {
			h.mu.Lock()
			h.results = append(h.results, snapshotRead{limit: limit, count: len(values)})
			h.mu.Unlock()
		}
		return err
	}
}

func (h *snapshotReadHook) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.results = nil
}

func (h *snapshotReadHook) reads() []snapshotRead {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]snapshotRead(nil), h.results...)
}
