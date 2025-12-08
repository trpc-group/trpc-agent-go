//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestMessagesSnapshotRequiresAppName(t *testing.T) {
	svc := &testSessionService{}
	tracker, err := track.New(svc)
	require.NoError(t, err)
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		tracker:           tracker,
	}

	ch, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestMessagesSnapshotRequiresRunner(t *testing.T) {
	r := &runner{}
	ch, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.Nil(t, ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "runner is nil")
}

func TestMessagesSnapshotRequiresInput(t *testing.T) {
	r := &runner{
		runner: noopBaseRunner{},
	}
	ch, err := r.MessagesSnapshot(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run input cannot be nil")
}

func TestMessagesSnapshotRequiresSessionService(t *testing.T) {
	r := &runner{
		runner:         noopBaseRunner{},
		appName:        "demo",
		userIDResolver: NewOptions().UserIDResolver,
	}

	ch, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestMessagesSnapshotHappyPath(t *testing.T) {
	svc := &testSessionService{
		trackEvents: []session.TrackEvent{
			newTrackEvent(t, aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user"))),
			newTrackEvent(t, aguievents.NewTextMessageContentEvent("user-1", "hello")),
			newTrackEvent(t, aguievents.NewTextMessageEndEvent("user-1")),
			newTrackEvent(t, aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
			newTrackEvent(t, aguievents.NewTextMessageContentEvent("assistant-1", "thinking")),
			newTrackEvent(t, aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1"))),
			newTrackEvent(t, aguievents.NewToolCallArgsEvent("tool-call-1", "{\"a\":1}")),
			newTrackEvent(t, aguievents.NewToolCallEndEvent("tool-call-1")),
			newTrackEvent(t, aguievents.NewTextMessageEndEvent("assistant-1")),
			newTrackEvent(t, aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "42")),
		},
	}
	tracker, err := track.New(svc)
	require.NoError(t, err)
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		tracker:           tracker,
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	require.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 3)

	if _, ok := collected[0].(*aguievents.RunStartedEvent); !ok {
		t.Fatalf("expected RUN_STARTED")
	}

	snapshot, ok := collected[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	require.Len(t, snapshot.Messages, 3)
	assert.Equal(t, "user", snapshot.Messages[0].Role)
	assert.Equal(t, "hello", *snapshot.Messages[0].Content)
	assert.Equal(t, "assistant", snapshot.Messages[1].Role)
	require.Len(t, snapshot.Messages[1].ToolCalls, 1)
	assert.Equal(t, "calc", snapshot.Messages[1].ToolCalls[0].Function.Name)
	assert.Equal(t, "tool", snapshot.Messages[2].Role)
	assert.Equal(t, "tool-call-1", *snapshot.Messages[2].ToolCallID)

	if _, ok := collected[2].(*aguievents.RunFinishedEvent); !ok {
		t.Fatalf("expected RUN_FINISHED")
	}
}

func TestMessagesSnapshotEmptyTrack(t *testing.T) {
	svc := &testSessionService{}
	tracker, err := track.New(svc)
	require.NoError(t, err)
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		tracker:           tracker,
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	require.NoError(t, err)
	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 2)
	if _, ok := collected[1].(*aguievents.RunErrorEvent); !ok {
		t.Fatalf("expected RUN_ERROR")
	}
}

func TestMessagesSnapshotGetSessionError(t *testing.T) {
	svc := &testSessionService{getErr: errors.New("boom")}
	tracker, err := track.New(svc)
	require.NoError(t, err)
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		tracker:           tracker,
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	require.NoError(t, err)
	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 2)
	if _, ok := collected[1].(*aguievents.RunErrorEvent); !ok {
		t.Fatalf("expected RUN_ERROR")
	}
}

func TestMessagesSnapshotUserIDResolverError(t *testing.T) {
	svc := &testSessionService{trackEvents: []session.TrackEvent{newTrackEvent(t, aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")))}}
	tracker, err := track.New(svc)
	require.NoError(t, err)
	userIDResolver := func(context.Context, *adapter.RunAgentInput) (string, error) {
		return "", errors.New("boom")
	}
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    userIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		tracker:           tracker,
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	require.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "resolve user ID")
}

func TestMessagesSnapshotRunAgentInputHookError(t *testing.T) {
	svc := &testSessionService{}
	tracker, err := track.New(svc)
	require.NoError(t, err)
	r := &runner{
		runner:         noopBaseRunner{},
		appName:        "demo",
		tracker:        tracker,
		userIDResolver: NewOptions().UserIDResolver,
		runAgentInputHook: func(context.Context, *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
			return nil, errors.New("hook fail")
		},
	}

	ch, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.Nil(t, ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run input hook")
}

func TestMessagesSnapshotReduceError(t *testing.T) {
	svc := &testSessionService{
		trackEvents: []session.TrackEvent{
			newTrackEvent(t, aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user"))),
		},
	}
	tracker, err := track.New(svc)
	require.NoError(t, err)
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		tracker:           tracker,
	}

	stream, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	require.NoError(t, err)
	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 3)
	if _, ok := collected[0].(*aguievents.RunStartedEvent); !ok {
		t.Fatalf("expected RUN_STARTED")
	}
	snapshot, ok := collected[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	require.Len(t, snapshot.Messages, 1)
	errEvt, ok := collected[2].(*aguievents.RunErrorEvent)
	require.True(t, ok)
	assert.Contains(t, errEvt.Message, "reduce track events")
}

func TestMessagesSnapshotReduceErrorEmitsSnapshotThenError(t *testing.T) {
	svc := &testSessionService{
		trackEvents: []session.TrackEvent{
			newTrackEvent(t, aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user"))),
			newTrackEvent(t, aguievents.NewTextMessageContentEvent("user-1", "hello")),
			newTrackEvent(t, aguievents.NewTextMessageEndEvent("user-1")),
			newTrackEvent(t, aguievents.NewTextMessageContentEvent("user-1", "!")),
		},
	}
	tracker, err := track.New(svc)
	require.NoError(t, err)
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		tracker:           tracker,
	}

	stream, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	require.NoError(t, err)
	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 3)
	if _, ok := collected[0].(*aguievents.RunStartedEvent); !ok {
		t.Fatalf("expected RUN_STARTED")
	}
	snapshot, ok := collected[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	require.Len(t, snapshot.Messages, 1)
	if snapshot.Messages[0].Content == nil || *snapshot.Messages[0].Content != "hello" {
		t.Fatalf("unexpected snapshot content %v", snapshot.Messages[0].Content)
	}
	errEvt, ok := collected[2].(*aguievents.RunErrorEvent)
	require.True(t, ok)
	assert.Contains(t, errEvt.Message, "reduce track events")
}

func collectAGUIEvents(t *testing.T, ch <-chan aguievents.Event) []aguievents.Event {
	t.Helper()
	var events []aguievents.Event
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func newTrackEvent(t *testing.T, evt aguievents.Event) session.TrackEvent {
	t.Helper()
	payload, err := evt.ToJSON()
	require.NoError(t, err)
	return session.TrackEvent{
		Track:     track.TrackAGUI,
		Payload:   append([]byte(nil), payload...),
		Timestamp: time.Now(),
	}
}

type noopBaseRunner struct{}

func (noopBaseRunner) Run(ctx context.Context, userID, sessionID string, message model.Message,
	runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (noopBaseRunner) Close() error { return nil }

type testSessionService struct {
	trackEvents []session.TrackEvent
	getErr      error
	returnNil   bool
}

func (s *testSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap,
	opts ...session.Option) (*session.Session, error) {
	return nil, nil
}

func (s *testSessionService) GetSession(ctx context.Context, key session.Key,
	opts ...session.Option) (*session.Session, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.returnNil {
		return nil, nil
	}
	sess := &session.Session{AppName: key.AppName, UserID: key.UserID, ID: key.SessionID}
	if len(s.trackEvents) > 0 {
		sess.Tracks = map[session.Track]*session.TrackEvents{
			track.TrackAGUI: {
				Track:  track.TrackAGUI,
				Events: append([]session.TrackEvent(nil), s.trackEvents...),
			},
		}
	}
	return sess, nil
}

func (s *testSessionService) ListSessions(ctx context.Context, key session.UserKey,
	opts ...session.Option) ([]*session.Session, error) {
	return nil, nil
}

func (s *testSessionService) DeleteSession(ctx context.Context, key session.Key, opts ...session.Option) error {
	return nil
}

func (s *testSessionService) UpdateAppState(ctx context.Context, app string, state session.StateMap) error {
	return nil
}

func (s *testSessionService) DeleteAppState(ctx context.Context, app string, key string) error {
	return nil
}

func (s *testSessionService) ListAppStates(ctx context.Context, app string) (session.StateMap, error) {
	return nil, nil
}

func (s *testSessionService) UpdateUserState(ctx context.Context, key session.UserKey, state session.StateMap) error {
	return nil
}

func (s *testSessionService) ListUserStates(ctx context.Context, key session.UserKey) (session.StateMap, error) {
	return nil, nil
}

func (s *testSessionService) DeleteUserState(ctx context.Context, key session.UserKey, stateKey string) error {
	return nil
}

func (s *testSessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	return nil
}

func (s *testSessionService) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event,
	opts ...session.Option) error {
	return nil
}

func (s *testSessionService) AppendTrackEvent(ctx context.Context, sess *session.Session,
	evt *session.TrackEvent, opts ...session.Option) error {
	if evt != nil {
		s.trackEvents = append(s.trackEvents, *evt)
	}
	return nil
}

func (s *testSessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, summary string,
	force bool) error {
	return nil
}

func (s *testSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, summary string,
	force bool) error {
	return nil
}

func (s *testSessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	return "", false
}

func (s *testSessionService) Close() error { return nil }
