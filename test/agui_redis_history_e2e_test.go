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
	"net/http"
	"os"
	"strings"
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
	for _, scenario := range []string{
		"old-finished-before-snapshot", "older-finishes-last", "start-replaced", "startup-write-fails",
		"reversed-uuid-order", "legacy-startup-write-fails-with-active-run",
		"newer-legacy-run", "newer-legacy-run-finishes", "older-ordered-startup-lost-newer-legacy",
		"older-startup-lost-before-newer-start", "latest-finishes-without-startup",
		"older-startup-flush-delayed", "older-startup-delayed-newer-startup-lost",
	} {
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
	legacy := scenario == "legacy-startup-write-fails-with-active-run"
	olderStartupLost := scenario == "older-startup-lost-before-newer-start"
	leaveOlderRunning := olderStartupLost || scenario == "latest-finishes-without-startup" ||
		scenario == "newer-legacy-run-finishes"
	latestStartupLostWithDelay := scenario == "older-startup-delayed-newer-startup-lost"
	delayOlderStartup := scenario == "older-startup-flush-delayed" || latestStartupLostWithDelay
	olderOrderedStartupLost := scenario == "older-ordered-startup-lost-newer-legacy"
	previewBeforeOlderFinishes := latestStartupLostWithDelay || olderOrderedStartupLost
	oldRedisService := newService()
	var oldService session.Service = oldRedisService
	latestService := newService()
	var liveService session.Service = latestService
	switch scenario {
	case "older-startup-lost-before-newer-start":
		oldService = &historyPersistenceFixture{Service: oldRedisService, failFirstAppend: true}
	case "startup-write-fails", "latest-finishes-without-startup", "older-startup-delayed-newer-startup-lost":
		liveService = &historyPersistenceFixture{Service: latestService, failFirstAppend: true}
	case "reversed-uuid-order":
		// Persist the newer run's start second while giving it a smaller UUIDv7.
		oldService = &historyPersistenceFixture{
			Service: oldRedisService, runID: "00000000-0002-7000-8000-000000000000",
		}
		liveService = &historyPersistenceFixture{Service: latestService, runID: "00000000-0001-7000-8000-000000000000"}
	case "older-ordered-startup-lost-newer-legacy":
		oldService = &historyPersistenceFixture{Service: oldRedisService, failFirstAppend: true}
		liveService = &historyPersistenceFixture{Service: latestService, omitRunID: true}
	case "newer-legacy-run", "newer-legacy-run-finishes":
		// A legacy writer can start after a run with a stored order.
		liveService = &historyPersistenceFixture{Service: latestService, omitRunID: true}
	case "legacy-startup-write-fails-with-active-run":
		oldService = &historyPersistenceFixture{Service: oldRedisService, omitRunID: true}
		liveService = &historyPersistenceFixture{Service: latestService, omitRunID: true, failFirstAppend: true}
	}
	olderGate := newHistoryStartupGate("old")
	oldCallbacks := translator.NewCallbacks()
	if delayOlderStartup {
		oldCallbacks.RegisterAfterTranslate(olderGate.pause)
	}
	oldServer := newAGUIHTTPServerWithOptions(t, oldModel, oldService,
		agui.WithAGUIRunnerOptions(aguirunner.WithTranslateCallbacks(oldCallbacks)), agui.WithFlushInterval(5*time.Millisecond))
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

	const olderPayload = `{"threadId":"thread","runId":"old","messages":[{"id":"old-user","role":"user","content":"old"}],"forwardedProps":{"userId":"user"}}`
	const olderPreview = "older output before newer run starts"
	const latestPreview = "before snapshot"
	persistPreview := func(stream historyStreamModel, messageID, delta string) {
		t.Helper()
		response := historyDelta(delta)
		response.ID = messageID
		stream.responses <- response
		require.Eventually(t, func() bool {
			history, err := latestService.GetTrackEvents(context.Background(), key, "agui")
			if err != nil || len(history.Events) == 0 {
				return false
			}
			last, err := aguievents.EventFromJSON(history.Events[len(history.Events)-1].Payload)
			content, ok := last.(*aguievents.TextMessageContentEvent)
			return err == nil && ok && content.Delta == delta
		}, 3*time.Second, 5*time.Millisecond)
	}

	var oldRun *http.Response
	var resumeOlder func() *http.Response
	if delayOlderStartup {
		resumeOlder = olderGate.start(t, oldServer.Client(), oldServer.URL+"/agui", olderPayload)
	} else {
		oldRun = postJSON(t, oldServer.Client(), oldServer.URL+"/agui", olderPayload, 10*time.Second)
	}
	if leaveOlderRunning || legacy {
		defer func() {
			close(oldModel.responses)
			readSSEEvents(t, oldRun.Body)
		}()
	}
	if olderStartupLost {
		persistPreview(oldModel, "older-preview", olderPreview)
	}
	previousModel, previousRun, previousMessage := oldModel, oldRun, "old-message"
	if legacy {
		// Leave the oldest run active while the middle run finishes and the
		// latest run writes its first response after losing the startup batch.
		middleModel := historyStreamModel{responses: make(chan *model.Response, 1)}
		middleServer := newAGUIHTTPServerWithOptions(t, middleModel,
			&historyPersistenceFixture{Service: newService(), omitRunID: true}, agui.WithFlushInterval(5*time.Millisecond))
		middleRun := postJSON(t, middleServer.Client(), middleServer.URL+"/agui",
			`{"threadId":"thread","runId":"middle","messages":[{"id":"middle-user","role":"user","content":"middle"}],"forwardedProps":{"userId":"user"}}`,
			10*time.Second)
		previousModel, previousRun, previousMessage = middleModel, middleRun, "middle-message"
	}
	const latestPayload = `{"threadId":"thread","runId":"latest","messages":[{"id":"latest-user","role":"user","content":"latest"}],"forwardedProps":{"userId":"user"}}`
	latestRun := postJSON(t, latestServer.Client(), latestServer.URL+"/agui", latestPayload, 10*time.Second)
	if previewBeforeOlderFinishes {
		persistPreview(latestModel, "latest-message", latestPreview)
	}
	if delayOlderStartup {
		previousRun = resumeOlder()
	}

	finish := func(stream historyStreamModel, id string) {
		stream.responses <- &model.Response{
			ID: id, Object: model.ObjectTypeChatCompletion, Done: true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage(id)}},
		}
		close(stream.responses)
	}
	snapshotInput := `{"threadId":"thread","runId":"snapshot","messages":[],"forwardedProps":{"userId":"user"}}`
	latestCompleted := leaveOlderRunning || scenario == "older-finishes-last"
	if latestCompleted {
		finish(latestModel, "latest-message")
		events := readSSEEvents(t, latestRun.Body)
		require.IsType(t, &aguievents.RunFinishedEvent{}, events[len(events)-1])
	}
	if !leaveOlderRunning {
		finish(previousModel, previousMessage)
		previousEvents := readSSEEvents(t, previousRun.Body)
		require.IsType(t, &aguievents.RunFinishedEvent{}, previousEvents[len(previousEvents)-1])
	}
	if !latestCompleted && !previewBeforeOlderFinishes {
		persistPreview(latestModel, "latest-message", latestPreview)
	}

	var expectedTrackOrder []string
	switch scenario {
	case "older-finishes-last":
		expectedTrackOrder = []string{"start:old", "start:latest", "finish:latest", "finish:old"}
	case "older-startup-lost-before-newer-start":
		expectedTrackOrder = []string{olderPreview, "start:latest", "finish:latest"}
	case "latest-finishes-without-startup":
		expectedTrackOrder = []string{"start:old", "finish:latest"}
	case "older-startup-flush-delayed":
		expectedTrackOrder = []string{"start:latest", "start:old", "finish:old", latestPreview}
	case "older-startup-delayed-newer-startup-lost":
		expectedTrackOrder = []string{latestPreview, "start:old", "finish:old"}
	case "older-ordered-startup-lost-newer-legacy":
		expectedTrackOrder = []string{"start:latest", latestPreview, "finish:old"}
	case "newer-legacy-run-finishes":
		expectedTrackOrder = []string{"start:old", "start:latest", "finish:latest"}
	case "newer-legacy-run":
		expectedTrackOrder = []string{"start:old", "start:latest", "finish:old", latestPreview}
	}
	if expectedTrackOrder != nil {
		history, err := latestService.GetTrackEvents(context.Background(), key, "agui")
		require.NoError(t, err)
		var trackOrder []string
		for _, record := range history.Events {
			event, err := aguievents.EventFromJSON(record.Payload)
			require.NoError(t, err)
			switch event := event.(type) {
			case *aguievents.RunStartedEvent:
				trackOrder = append(trackOrder, "start:"+event.RunID())
			case *aguievents.RunFinishedEvent:
				if olderOrderedStartupLost && event.RunID() == "old" {
					require.Contains(t, string(record.Payload), `"trpcRunOrder"`)
				}
				trackOrder = append(trackOrder, "finish:"+event.RunID())
			case *aguievents.TextMessageContentEvent:
				if event.Delta == olderPreview || event.Delta == latestPreview {
					trackOrder = append(trackOrder, event.Delta)
				}
			}
		}
		require.Equal(t, expectedTrackOrder, trackOrder)
	}

	if latestCompleted {
		snapshot := postJSON(t, latestServer.Client(), latestServer.URL+"/history", snapshotInput, 5*time.Second)
		events := readSSEEvents(t, snapshot.Body)
		require.Len(t, events, 3)
		require.IsType(t, &aguievents.RunStartedEvent{}, events[0])
		require.IsType(t, &aguievents.MessagesSnapshotEvent{}, events[1])
		if failure, ok := events[2].(*aguievents.RunErrorEvent); ok {
			t.Fatalf("completed history returned RUN_ERROR: %s", failure.Message)
		}
		finished, ok := events[2].(*aguievents.RunFinishedEvent)
		require.True(t, ok)
		require.Equal(t, "snapshot", finished.RunID())
		return
	}

	if legacy {
		history, err := latestService.GetTrackEvents(context.Background(), key, "agui")
		require.NoError(t, err)
		var startedRuns, finishedRuns []string
		for _, event := range history.Events {
			require.NotContains(t, string(event.Payload), `"trpcRunId"`)
			require.NotContains(t, string(event.Payload), `"trpcRunOrder"`)
			stored, err := aguievents.EventFromJSON(event.Payload)
			require.NoError(t, err)
			switch stored.Type() {
			case aguievents.EventTypeRunStarted:
				startedRuns = append(startedRuns, stored.RunID())
			case aguievents.EventTypeRunFinished:
				finishedRuns = append(finishedRuns, stored.RunID())
			}
		}
		require.Equal(t, []string{"old", "middle"}, startedRuns)
		require.Equal(t, []string{"middle"}, finishedRuns)
	}

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

	observed := next()
	content, ok := observed.(*aguievents.TextMessageContentEvent)
	require.True(t, ok, "history ended before delivering the latest run's next content: %s", observed.Type())
	require.Equal(t, "after snapshot", content.Delta)
	require.IsType(t, &aguievents.TextMessageEndEvent{}, next())
	finished, ok := next().(*aguievents.RunFinishedEvent)
	require.True(t, ok)
	require.Equal(t, "snapshot", finished.RunID())
	runEvents := readSSEEvents(t, latestRun.Body)
	require.IsType(t, &aguievents.RunFinishedEvent{}, runEvents[len(runEvents)-1])
}

// historyStartupGate pauses a live run before its startup batch is stored.
type historyStartupGate struct {
	runID   string
	reached chan struct{}
	release chan struct{}
}

func newHistoryStartupGate(runID string) *historyStartupGate {
	return &historyStartupGate{runID: runID, reached: make(chan struct{}), release: make(chan struct{}, 1)}
}

func (g *historyStartupGate) pause(_ context.Context, event aguievents.Event) (aguievents.Event, error) {
	if event.Type() == aguievents.EventTypeRunStarted && event.RunID() == g.runID {
		close(g.reached)
		<-g.release
	}
	return nil, nil
}

func (g *historyStartupGate) start(t *testing.T, client *http.Client, url, payload string) func() *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(func() { close(g.release) })
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(payload))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	type runResponse struct {
		response *http.Response
		err      error
	}
	responses := make(chan runResponse, 1)
	go func() {
		response, err := client.Do(request)
		responses <- runResponse{response, err}
	}()
	select {
	case <-g.reached:
	case result := <-responses:
		if result.response != nil {
			_ = result.response.Body.Close()
		}
		t.Fatalf("run %s returned before reaching startup callback: %v", g.runID, result.err)
	case <-ctx.Done():
		t.Fatalf("run %s did not reach startup callback", g.runID)
	}
	return func() *http.Response {
		t.Helper()
		g.release <- struct{}{}
		result := <-responses
		require.NoError(t, result.err)
		t.Cleanup(func() { _ = result.response.Body.Close() })
		require.Equal(t, http.StatusOK, result.response.StatusCode)
		return result.response
	}
}

type historyPersistenceFixture struct {
	*redisservice.Service
	runID           string
	omitRunID       bool
	failFirstAppend bool
	failed          atomic.Bool
}

var _ session.StateInitializationService = (*historyPersistenceFixture)(nil)

func (s *historyPersistenceFixture) LoadOrInitializeSessionState(
	ctx context.Context,
	key session.Key,
	stateKey string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
	projections ...session.StateInitializationProjection,
) ([]byte, bool, error) {
	if s.omitRunID {
		return nil, false, errors.New("legacy writer does not allocate history run order")
	}
	return s.Service.LoadOrInitializeSessionState(ctx, key, stateKey, validate, initialize, projections...)
}

func (s *historyPersistenceFixture) AppendTrackEvent(ctx context.Context, sess *session.Session, event *session.TrackEvent, opts ...session.Option) error {
	if s.failFirstAppend && s.failed.CompareAndSwap(false, true) {
		return errors.New("temporary startup track write failure")
	}
	if s.runID != "" || s.omitRunID {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return err
		}
		if s.omitRunID {
			delete(payload, "trpcRunId")
			delete(payload, "trpcRunOrder")
		} else {
			runID, _ := json.Marshal(s.runID)
			payload["trpcRunId"] = runID
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		rewritten := *event
		rewritten.Payload = raw
		event = &rewritten
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
