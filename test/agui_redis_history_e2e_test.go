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
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/alicebob/miniredis/v2"
	r3sse "github.com/r3labs/sse/v2"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
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

func TestAGUIRedisHistoryFollowsLatestOverlappingRun(t *testing.T) {
	for _, scenario := range []string{"old-finished-before-snapshot", "older-finishes-last", "start-replaced", "startup-write-fails"} {
		t.Run(scenario, func(t *testing.T) { testAGUIRedisHistoryFollowsLatestRun(t, scenario) })
	}
}

func testAGUIRedisHistoryFollowsLatestRun(t *testing.T, scenario string) {
	t.Helper()
	url := os.Getenv("TRPC_TEST_REDIS_URL")
	if url == "" {
		url = "redis://" + miniredis.RunT(t).Addr()
	}
	prefix := "history-e2e-" + rand.Text()
	newService := func() *redisservice.Service {
		t.Helper()
		svc, err := redisservice.NewService(
			redisservice.WithRedisClientURL(url),
			redisservice.WithCompatMode(redisservice.CompatModeNone),
			redisservice.WithEnableAsyncPersist(false),
			redisservice.WithKeyPrefix(prefix),
		)
		require.NoError(t, err)
		return svc
	}
	oldModel := historyStreamModel{responses: make(chan *model.Response, 1)}
	latestModel := historyStreamModel{responses: make(chan *model.Response, 1)}
	oldServer := newAGUIHTTPServerWithOptions(t, oldModel, newService(),
		agui.WithFlushInterval(5*time.Millisecond))
	latestService := newService()
	var liveService session.Service = latestService
	if scenario == "startup-write-fails" {
		liveService = &failFirstHistoryAppend{Service: latestService}
	}
	callbacks := translator.NewCallbacks().RegisterAfterTranslate(func(_ context.Context, e aguievents.Event) (aguievents.Event, error) {
		if scenario == "start-replaced" && e.Type() == aguievents.EventTypeRunStarted && e.RunID() == "latest" {
			return aguievents.NewCustomEvent("start-replaced"), nil
		}
		return nil, nil
	})
	latestServer := newAGUIHTTPServerWithOptions(t, latestModel, liveService,
		agui.WithAGUIRunnerOptions(aguirunner.WithTranslateCallbacks(callbacks)),
		agui.WithFlushInterval(5*time.Millisecond),
		agui.WithMessagesSnapshotFollowEnabled(true),
		agui.WithMessagesSnapshotFollowMaxDuration(3*time.Second),
	)
	key := session.Key{AppName: testAppName, UserID: "user", SessionID: "thread"}
	t.Cleanup(func() { require.NoError(t, latestService.DeleteSession(context.Background(), key)) })

	oldRun := postJSON(t, oldServer.Client(), oldServer.URL+"/agui",
		`{"threadId":"thread","runId":"old","messages":[{"id":"old-user","role":"user","content":"old"}],"forwardedProps":{"userId":"user"}}`,
		10*time.Second)
	latestRun := postJSON(t, latestServer.Client(), latestServer.URL+"/agui",
		`{"threadId":"thread","runId":"latest","messages":[{"id":"latest-user","role":"user","content":"latest"}],"forwardedProps":{"userId":"user"}}`,
		10*time.Second)

	finish := func(stream historyStreamModel, id string) {
		stream.responses <- &model.Response{
			ID: id, Object: model.ObjectTypeChatCompletion, Done: true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage(id)}},
		}
		close(stream.responses)
	}
	snapshotInput := `{"threadId":"thread","runId":"snapshot","messages":[],"forwardedProps":{"userId":"user"}}`
	if scenario == "older-finishes-last" {
		finish(latestModel, "latest-message")
		events := readSSEEvents(t, latestRun.Body)
		require.IsType(t, &aguievents.RunFinishedEvent{}, events[len(events)-1])
	}
	finish(oldModel, "old-message")
	oldEvents := readSSEEvents(t, oldRun.Body)
	require.IsType(t, &aguievents.RunFinishedEvent{}, oldEvents[len(oldEvents)-1])

	if scenario == "older-finishes-last" {
		history, err := latestService.GetTrackEvents(context.Background(), key, "agui")
		require.NoError(t, err)
		var finishedRuns []string
		for _, event := range history.Events {
			stored, err := aguievents.EventFromJSON(event.Payload)
			require.NoError(t, err)
			if terminal, ok := stored.(*aguievents.RunFinishedEvent); ok {
				finishedRuns = append(finishedRuns, terminal.RunID())
			}
		}
		require.Equal(t, []string{"latest", "old"}, finishedRuns)

		snapshot := postJSON(t, latestServer.Client(), latestServer.URL+"/history", snapshotInput, 5*time.Second)
		events := readSSEEvents(t, snapshot.Body)
		require.Len(t, events, 3)
		require.IsType(t, &aguievents.MessagesSnapshotEvent{}, events[1])
		if failure, ok := events[2].(*aguievents.RunErrorEvent); ok {
			t.Fatalf("completed history returned RUN_ERROR: %s", failure.Message)
		}
		finished, ok := events[2].(*aguievents.RunFinishedEvent)
		require.True(t, ok)
		require.Equal(t, "snapshot", finished.RunID())
		return
	}

	latestModel.responses <- historyDelta("before snapshot")
	require.Eventually(t, func() bool {
		history, err := latestService.GetTrackEvents(context.Background(), key, "agui")
		if err != nil || len(history.Events) == 0 {
			return false
		}
		last, err := aguievents.EventFromJSON(history.Events[len(history.Events)-1].Payload)
		content, ok := last.(*aguievents.TextMessageContentEvent)
		return err == nil && ok && content.Delta == "before snapshot"
	}, 3*time.Second, 5*time.Millisecond)

	snapshot := postJSON(t, latestServer.Client(), latestServer.URL+"/history", snapshotInput, 10*time.Second)
	reader := r3sse.NewEventStreamReader(snapshot.Body, 1024*1024)
	next := func() aguievents.Event {
		t.Helper()
		raw, err := reader.ReadEvent()
		require.NoError(t, err)
		event, err := aguievents.EventFromJSON(sseData(raw))
		require.NoError(t, err)
		return event
	}
	require.IsType(t, &aguievents.RunStartedEvent{}, next())
	require.IsType(t, &aguievents.MessagesSnapshotEvent{}, next())
	latestModel.responses <- historyDelta("after snapshot")
	close(latestModel.responses)

	content, ok := next().(*aguievents.TextMessageContentEvent)
	require.True(t, ok, "history ended before delivering the latest run's next content")
	require.Equal(t, "after snapshot", content.Delta)
	require.IsType(t, &aguievents.TextMessageEndEvent{}, next())
	finished, ok := next().(*aguievents.RunFinishedEvent)
	require.True(t, ok)
	require.Equal(t, "snapshot", finished.RunID())
	runEvents := readSSEEvents(t, latestRun.Body)
	require.IsType(t, &aguievents.RunFinishedEvent{}, runEvents[len(runEvents)-1])
}

type failFirstHistoryAppend struct {
	*redisservice.Service
	failed atomic.Bool
}

func (s *failFirstHistoryAppend) AppendTrackEvent(ctx context.Context, sess *session.Session, event *session.TrackEvent, opts ...session.Option) error {
	if s.failed.CompareAndSwap(false, true) {
		return errors.New("temporary startup track write failure")
	}
	return s.Service.AppendTrackEvent(ctx, sess, event, opts...)
}

type historyStreamModel struct {
	responses chan *model.Response
}

func (m historyStreamModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	return m.responses, nil
}

func (historyStreamModel) Info() model.Info { return model.Info{Name: "history-stream"} }

func historyDelta(content string) *model.Response {
	return &model.Response{
		ID: "latest-message", Object: model.ObjectTypeChatCompletionChunk, IsPartial: true,
		Choices: []model.Choice{{Delta: model.Message{Role: model.RoleAssistant, Content: content}}},
	}
}

func TestAGUIHistoryCompletesAfterAnonymousCallbackTerminal(t *testing.T) {
	m := &QueueModel{}
	m.Push(Call{Responses: []*model.Response{{
		ID: "answer", Object: model.ObjectTypeChatCompletion, Done: true,
		Choices: []model.Choice{{Message: model.NewAssistantMessage("answer")}},
	}}})
	svc := inmemory.NewSessionService()
	callback := translator.NewCallbacks().RegisterAfterTranslate(func(_ context.Context, e aguievents.Event) (aguievents.Event, error) {
		if _, ok := e.(*aguievents.RunFinishedEvent); ok && e.RunID() == "original" {
			return aguievents.NewRunErrorEvent("callback override"), nil
		}
		return nil, nil
	})
	srv := newAGUIHTTPServerWithOptions(t, m, svc,
		agui.WithAGUIRunnerOptions(aguirunner.WithTranslateCallbacks(callback)),
		agui.WithFlushInterval(10*time.Millisecond),
		agui.WithMessagesSnapshotFollowEnabled(true),
		agui.WithMessagesSnapshotFollowMaxDuration(250*time.Millisecond),
	)
	live := readSSEEvents(t, postJSON(t, srv.Client(), srv.URL+"/agui",
		`{"threadId":"thread","runId":"original","messages":[{"id":"u","role":"user","content":"hello"}],"forwardedProps":{"userId":"user"}}`,
		5*time.Second).Body)
	terminal, ok := live[len(live)-1].(*aguievents.RunErrorEvent)
	require.True(t, ok)
	require.Empty(t, terminal.RunID())
	history, err := svc.GetTrackEvents(context.Background(), session.Key{
		AppName: testAppName, UserID: "user", SessionID: "thread",
	}, "agui")
	require.NoError(t, err)
	stored, err := aguievents.EventFromJSON(history.Events[len(history.Events)-1].Payload)
	require.NoError(t, err)
	require.IsType(t, &aguievents.RunErrorEvent{}, stored)
	require.Empty(t, stored.RunID())
	out := readSSEEvents(t, postJSON(t, srv.Client(), srv.URL+"/history",
		`{"threadId":"thread","runId":"snapshot","messages":[],"forwardedProps":{"userId":"user"}}`,
		5*time.Second).Body)
	last := out[len(out)-1]
	if failure, ok := last.(*aguievents.RunErrorEvent); ok {
		t.Fatalf("completed history returned RUN_ERROR: %s", failure.Message)
	}
	require.IsType(t, &aguievents.RunFinishedEvent{}, last)
}
