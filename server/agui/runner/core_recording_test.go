//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestWrapCoreRunnerRecordsAGUITrackAndForwardsCoreEvents(t *testing.T) {
	assistant := newAssistantEvent("run-1", "hello")
	completion := newCompletionEvent("run-1")
	base := &recordingBaseRunner{events: []*event.Event{assistant, completion}}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(),
		"user",
		"thread",
		model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	got := collectCoreEvents(events)
	require.Len(t, got, 2)
	assert.Same(t, assistant, got[0])
	assert.Same(t, completion, got[1])

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "thread",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	trackEvents, err := sess.GetTrackEvents(session.Track("agui"))
	require.NoError(t, err)
	gotTypes := make([]aguievents.EventType, 0, len(trackEvents.Events))
	var userEvent *aguievents.CustomEvent
	for _, trackEvent := range trackEvents.Events {
		evt, err := aguievents.EventFromJSON(trackEvent.Payload)
		require.NoError(t, err)
		gotTypes = append(gotTypes, evt.Type())
		if custom, ok := evt.(*aguievents.CustomEvent); ok {
			userEvent = custom
		}
	}
	assert.Equal(t, []aguievents.EventType{
		aguievents.EventTypeCustom,
		aguievents.EventTypeRunStarted,
		aguievents.EventTypeTextMessageStart,
		aguievents.EventTypeTextMessageContent,
		aguievents.EventTypeTextMessageEnd,
		aguievents.EventTypeRunFinished,
	}, gotTypes)
	require.NotNil(t, userEvent)
	assert.Equal(t, multimodal.CustomEventNameUserMessage, userEvent.Name)
}

func TestWrapCoreRunnerUsesFirstEventRequestID(t *testing.T) {
	base := &recordingBaseRunner{events: []*event.Event{newCompletionEvent("request-id")}}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(), "user", "thread", model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	collectCoreEvents(events)

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "thread",
	})
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(session.Track("agui"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(trackEvents.Events), 3)
	startedEvent, err := aguievents.EventFromJSON(trackEvents.Events[1].Payload)
	require.NoError(t, err)
	started, ok := startedEvent.(*aguievents.RunStartedEvent)
	require.True(t, ok)
	assert.Equal(t, "request-id", started.RunID())
}

func TestWrapCoreRunnerRecordsEmptySuccessfulRun(t *testing.T) {
	base := &recordingBaseRunner{}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(), "user", "thread", model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	assert.Empty(t, collectCoreEvents(events))

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "thread",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	trackEvents, err := sess.GetTrackEvents(session.Track("agui"))
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 3)
	gotTypes := make([]aguievents.EventType, 0, len(trackEvents.Events))
	for _, trackEvent := range trackEvents.Events {
		evt, err := aguievents.EventFromJSON(trackEvent.Payload)
		require.NoError(t, err)
		gotTypes = append(gotTypes, evt.Type())
	}
	assert.Equal(t, []aguievents.EventType{
		aguievents.EventTypeCustom,
		aguievents.EventTypeRunStarted,
		aguievents.EventTypeRunFinished,
	}, gotTypes)
}

func TestWrapCoreRunnerTrackCanBeReadAsMessagesSnapshot(t *testing.T) {
	base := &recordingBaseRunner{events: []*event.Event{
		newAssistantEvent("run-1", "hello"),
		newCompletionEvent("run-1"),
	}}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(), "user", "thread", model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	collectCoreEvents(events)

	historyRunner := aguirunner.New(
		&recordingBaseRunner{},
		aguirunner.WithAppName("app"),
		aguirunner.WithSessionService(service),
	)
	snapshotter, ok := historyRunner.(aguirunner.MessagesSnapshotter)
	require.True(t, ok)
	history, err := snapshotter.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "history"},
	)
	require.NoError(t, err)
	var snapshot *aguievents.MessagesSnapshotEvent
	for evt := range history {
		if current, ok := evt.(*aguievents.MessagesSnapshotEvent); ok {
			snapshot = current
		}
	}
	require.NotNil(t, snapshot)
	require.Len(t, snapshot.Messages, 2)
	assert.Equal(t, aguitypes.RoleUser, snapshot.Messages[0].Role)
	userContent, ok := snapshot.Messages[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "hi", userContent)
	assert.Equal(t, aguitypes.RoleAssistant, snapshot.Messages[1].Role)
	assistantContent, ok := snapshot.Messages[1].ContentString()
	require.True(t, ok)
	assert.Equal(t, "hello", assistantContent)
}

func TestWrapCoreRunnerAppendsMultipleRunsToOneSession(t *testing.T) {
	base := &recordingBaseRunner{}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	base.events = []*event.Event{
		newAssistantEvent("run-1", "first"),
		newCompletionEvent("run-1"),
	}
	events, err := recorded.Run(
		context.Background(), "user", "thread", model.NewUserMessage("one"),
	)
	require.NoError(t, err)
	collectCoreEvents(events)
	base.events = []*event.Event{
		newAssistantEvent("run-2", "second"),
		newCompletionEvent("run-2"),
	}
	events, err = recorded.Run(
		context.Background(), "user", "thread", model.NewUserMessage("two"),
	)
	require.NoError(t, err)
	collectCoreEvents(events)

	historyRunner := aguirunner.New(
		&recordingBaseRunner{},
		aguirunner.WithAppName("app"),
		aguirunner.WithSessionService(service),
	)
	snapshotter := historyRunner.(aguirunner.MessagesSnapshotter)
	history, err := snapshotter.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "history"},
	)
	require.NoError(t, err)
	var snapshot *aguievents.MessagesSnapshotEvent
	for evt := range history {
		if current, ok := evt.(*aguievents.MessagesSnapshotEvent); ok {
			snapshot = current
		}
	}
	require.NotNil(t, snapshot)
	require.Len(t, snapshot.Messages, 4)
	assertSnapshotContent(t, snapshot.Messages[0], aguitypes.RoleUser, "one")
	assertSnapshotContent(t, snapshot.Messages[1], aguitypes.RoleAssistant, "first")
	assertSnapshotContent(t, snapshot.Messages[2], aguitypes.RoleUser, "two")
	assertSnapshotContent(t, snapshot.Messages[3], aguitypes.RoleAssistant, "second")
}

func TestWrapCoreRunnerRunErrorIsUnchanged(t *testing.T) {
	wantErr := errors.New("run failed")
	base := &recordingBaseRunner{runErr: wantErr}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(), "user", "thread", model.NewUserMessage("hi"),
	)
	assert.ErrorIs(t, err, wantErr)
	assert.Nil(t, events)

	sess, getErr := service.GetSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "thread",
	})
	require.NoError(t, getErr)
	assert.Nil(t, sess)
}

func TestWrapCoreRunnerRecordingFailureDoesNotChangeCoreEvents(t *testing.T) {
	completion := newCompletionEvent("run-1")
	base := &recordingBaseRunner{events: []*event.Event{completion}}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(),
		"user",
		"thread",
		model.NewAssistantMessage("invalid recording input"),
	)
	require.NoError(t, err)
	got := collectCoreEvents(events)
	require.Len(t, got, 1)
	assert.Same(t, completion, got[0])

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "thread",
	})
	require.NoError(t, err)
	assert.Nil(t, sess)
}

func TestWrapCoreRunnerEmptyStreamRecordingFailureDoesNotChangeCoreEvents(t *testing.T) {
	base := &recordingBaseRunner{}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(),
		"user",
		"thread",
		model.NewAssistantMessage("invalid recording input"),
	)
	require.NoError(t, err)
	assert.Empty(t, collectCoreEvents(events))

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "thread",
	})
	require.NoError(t, err)
	assert.Nil(t, sess)
}

func TestWrapCoreRunnerInvalidSessionKeyDoesNotChangeCoreEvents(t *testing.T) {
	completion := newCompletionEvent("run-1")
	base := &recordingBaseRunner{events: []*event.Event{completion}}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(),
		"",
		"thread",
		model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	got := collectCoreEvents(events)
	require.Len(t, got, 1)
	assert.Same(t, completion, got[0])
}

func TestWrapCoreRunnerEmptyUserMessageDoesNotChangeCoreEvents(t *testing.T) {
	completion := newCompletionEvent("run-1")
	base := &recordingBaseRunner{events: []*event.Event{completion}}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(),
		"user",
		"thread",
		model.Message{Role: model.RoleUser},
	)
	require.NoError(t, err)
	got := collectCoreEvents(events)
	require.Len(t, got, 1)
	assert.Same(t, completion, got[0])
}

func TestWrapCoreRunnerRecordsRolelessUserMessage(t *testing.T) {
	base := &recordingBaseRunner{}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(),
		"user",
		"thread",
		model.Message{Content: "hi"},
	)
	require.NoError(t, err)
	assert.Empty(t, collectCoreEvents(events))

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "thread",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	trackEvents, err := sess.GetTrackEvents(session.Track("agui"))
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 3)
}

func TestWrapCoreRunnerPersistenceFailureDoesNotChangeCoreEvents(t *testing.T) {
	completion := newCompletionEvent("run-1")
	base := &recordingBaseRunner{events: []*event.Event{completion}}
	underlying := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, underlying.Close()) })
	service := &failingTrackService{
		Service: underlying,
		err:     errors.New("append failed"),
	}
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(),
		"user",
		"thread",
		model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	got := collectCoreEvents(events)
	require.Len(t, got, 1)
	assert.Same(t, completion, got[0])
	assert.Positive(t, service.appendCalls)
}

func TestWrapCoreRunnerTranslationFailureDoesNotChangeCoreEvents(t *testing.T) {
	assistant := newAssistantEvent("run-1", "hello")
	invalid := &event.Event{}
	base := &recordingBaseRunner{events: []*event.Event{assistant, invalid}}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	events, err := recorded.Run(
		context.Background(),
		"user",
		"thread",
		model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	got := collectCoreEvents(events)
	require.Len(t, got, 2)
	assert.Same(t, assistant, got[0])
	assert.Same(t, invalid, got[1])
}

func TestWrapCoreRunnerCancellationUnblocksUnreadOutputAndFlushesTrack(t *testing.T) {
	base := &recordingBaseRunner{events: []*event.Event{
		newAssistantEvent("run-1", "hello"),
	}}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := recorded.Run(
		ctx, "user", "thread", model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	cancel()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	require.Eventually(t, func() bool {
		sess, err := service.GetSession(context.Background(), key)
		return err == nil && sess != nil
	}, time.Second, 10*time.Millisecond)

	channelClosed := false
	for !channelClosed {
		select {
		case _, ok := <-events:
			if !ok {
				channelClosed = true
			}
		case <-time.After(time.Second):
			t.Fatal("wrapped event channel did not close after cancellation")
		}
	}
	sess, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(session.Track("agui"))
	require.NoError(t, err)
	require.NotEmpty(t, trackEvents.Events)
	terminal, err := aguievents.EventFromJSON(
		trackEvents.Events[len(trackEvents.Events)-1].Payload,
	)
	require.NoError(t, err)
	assert.IsType(t, (*aguievents.RunErrorEvent)(nil), terminal)
}

func TestWrapCoreRunnerCloseDelegates(t *testing.T) {
	base := &recordingBaseRunner{}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	recorded, err := aguirunner.WrapCoreRunner(base, "app", service)
	require.NoError(t, err)

	require.NoError(t, recorded.Close())
	assert.Equal(t, 1, base.closeCalls)
}

func TestWrapCoreRunnerValidatesConfiguration(t *testing.T) {
	base := &recordingBaseRunner{}
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	_, err := aguirunner.WrapCoreRunner(nil, "app", service)
	assert.ErrorContains(t, err, "runner is nil")
	_, err = aguirunner.WrapCoreRunner(base, "", service)
	assert.ErrorContains(t, err, "app name is empty")
	_, err = aguirunner.WrapCoreRunner(base, "app", nil)
	assert.ErrorContains(t, err, "session service is nil")
	_, err = aguirunner.WrapCoreRunner(
		base,
		"app",
		serviceWithoutTrack{Service: service},
	)
	assert.ErrorContains(t, err, "does not implement track service")
}

type recordingBaseRunner struct {
	events     []*event.Event
	runErr     error
	closeCalls int
}

func (r *recordingBaseRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	if r.runErr != nil {
		return nil, r.runErr
	}
	events := make(chan *event.Event, len(r.events))
	for _, evt := range r.events {
		events <- evt
	}
	close(events)
	return events, nil
}

func (r *recordingBaseRunner) Close() error {
	r.closeCalls++
	return nil
}

type serviceWithoutTrack struct {
	session.Service
}

type failingTrackService struct {
	session.Service
	err         error
	appendCalls int
}

func (s *failingTrackService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	evt *session.TrackEvent,
	opts ...session.Option,
) error {
	s.appendCalls++
	return s.err
}

func newAssistantEvent(requestID, content string) *event.Event {
	evt := event.NewResponseEvent("invocation", "agent", &model.Response{
		ID:     "assistant-message",
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	})
	evt.RequestID = requestID
	return evt
}

func newCompletionEvent(requestID string) *event.Event {
	evt := event.NewResponseEvent("invocation", "app", &model.Response{
		ID:     "runner-completion",
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	})
	evt.RequestID = requestID
	return evt
}

func collectCoreEvents(events <-chan *event.Event) []*event.Event {
	var collected []*event.Event
	for evt := range events {
		collected = append(collected, evt)
	}
	return collected
}

func assertSnapshotContent(
	t *testing.T,
	message aguitypes.Message,
	role aguitypes.Role,
	content string,
) {
	t.Helper()
	assert.Equal(t, role, message.Role)
	got, ok := message.ContentString()
	require.True(t, ok)
	assert.Equal(t, content, got)
}

var _ trunner.Runner = (*recordingBaseRunner)(nil)
