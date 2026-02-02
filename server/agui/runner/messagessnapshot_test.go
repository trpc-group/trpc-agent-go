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
	"sync"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
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

	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])

	snapshot, ok := collected[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	require.Len(t, snapshot.Messages, 3)
	assert.Equal(t, types.RoleUser, snapshot.Messages[0].Role)
	content, ok := snapshot.Messages[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "hello", content)
	assert.Equal(t, types.RoleAssistant, snapshot.Messages[1].Role)
	require.Len(t, snapshot.Messages[1].ToolCalls, 1)
	assert.Equal(t, "calc", snapshot.Messages[1].ToolCalls[0].Function.Name)
	assert.Equal(t, types.RoleTool, snapshot.Messages[2].Role)
	assert.Equal(t, "tool-call-1", snapshot.Messages[2].ToolCallID)

	require.IsType(t, (*aguievents.RunFinishedEvent)(nil), collected[2])
}

func TestMessagesSnapshotAllowsConcurrentRequests(t *testing.T) {
	unblock := make(chan struct{})
	trackEvents := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEvent(t, aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant"))),
			newTrackEvent(t, aguievents.NewTextMessageContentEvent("msg-1", "hello")),
			newTrackEvent(t, aguievents.NewTextMessageEndEvent("msg-1")),
		},
	}
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		tracker:           &blockingTracker{unblock: unblock, events: trackEvents},
	}

	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"}

	stream1, err := r.MessagesSnapshot(context.Background(), input)
	require.NoError(t, err)

	stream2, err := r.MessagesSnapshot(context.Background(), input)
	require.NoError(t, err)

	close(unblock)

	evts1 := collectAGUIEvents(t, stream1)
	evts2 := collectAGUIEvents(t, stream2)
	require.Len(t, evts1, 3)
	require.Len(t, evts2, 3)
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), evts1[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), evts1[1])
	require.IsType(t, (*aguievents.RunFinishedEvent)(nil), evts1[2])
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), evts2[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), evts2[1])
	require.IsType(t, (*aguievents.RunFinishedEvent)(nil), evts2[2])
}

func TestMessagesSnapshotAllowsConcurrentWithRunningRun(t *testing.T) {
	agentCh := make(chan *event.Event)
	underlying := &fakeRunner{
		run: func(context.Context, string, string, model.Message, ...agent.RunOption) (<-chan *event.Event, error) {
			return agentCh, nil
		},
	}
	r := New(underlying).(*runner)
	unblock := make(chan struct{})
	close(unblock)
	trackEvents := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEvent(t, aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant"))),
			newTrackEvent(t, aguievents.NewTextMessageContentEvent("msg-1", "hello")),
			newTrackEvent(t, aguievents.NewTextMessageEndEvent("msg-1")),
		},
	}
	r.appName = "demo"
	r.tracker = &blockingTracker{unblock: unblock, events: trackEvents}

	runInput := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}

	runStream, err := r.Run(context.Background(), runInput)
	require.NoError(t, err)
	select {
	case evt := <-runStream:
		require.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for RUN_STARTED")
	}

	snapshotStream, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
	})
	require.NoError(t, err)
	snapshotEvts := collectAGUIEvents(t, snapshotStream)
	require.Len(t, snapshotEvts, 3)
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), snapshotEvts[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), snapshotEvts[1])
	require.IsType(t, (*aguievents.RunFinishedEvent)(nil), snapshotEvts[2])

	close(agentCh)
	_ = collectEvents(t, runStream)
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
	require.IsType(t, (*aguievents.RunErrorEvent)(nil), collected[1])
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
	require.IsType(t, (*aguievents.RunErrorEvent)(nil), collected[1])
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
			newTrackEvent(t, aguievents.NewTextMessageContentEvent("user-1", "hello")),
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
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	snapshot, ok := collected[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	require.Empty(t, snapshot.Messages)
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
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	snapshot, ok := collected[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	require.Len(t, snapshot.Messages, 1)
	require.NotNil(t, snapshot.Messages[0].Content)
	content, ok := snapshot.Messages[0].ContentString()
	require.True(t, ok)
	require.Equal(t, "hello", content)
	errEvt, ok := collected[2].(*aguievents.RunErrorEvent)
	require.True(t, ok)
	assert.Contains(t, errEvt.Message, "reduce track events")
}

func TestMessagesSnapshotFollowUntilTerminalEvent(t *testing.T) {
	base := time.Now().Add(-time.Second)
	initial := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")), base),
			newTrackEventAt(t, aguievents.NewTextMessageContentEvent("msg-1", "hello"), base.Add(time.Millisecond)),
			newTrackEventAt(t, aguievents.NewTextMessageEndEvent("msg-1"), base.Add(2*time.Millisecond)),
		},
	}
	follow := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewCustomEvent("node.progress", aguievents.WithValue(map[string]any{"p": 1})),
				base.Add(3*time.Millisecond)),
			newTrackEventAt(t, aguievents.NewRunFinishedEvent("thread", "real-run"), base.Add(4*time.Millisecond)),
		},
	}
	r := &runner{
		runner:                            noopBaseRunner{},
		userIDResolver:                    NewOptions().UserIDResolver,
		runAgentInputHook:                 NewOptions().RunAgentInputHook,
		appName:                           "demo",
		tracker:                           &sequenceTracker{first: initial, second: follow},
		flushInterval:                     5 * time.Millisecond,
		timeout:                           100 * time.Millisecond,
		messagesSnapshotFollowEnabled:     true,
		messagesSnapshotFollowMaxDuration: 100 * time.Millisecond,
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "req-run"},
	)
	require.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 4)
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), collected[1])
	require.IsType(t, (*aguievents.CustomEvent)(nil), collected[2])
	finished, ok := collected[3].(*aguievents.RunFinishedEvent)
	require.True(t, ok)
	require.Equal(t, "req-run", finished.RunID())
}

func TestTrackEndsWithTerminalRunEvent(t *testing.T) {
	base := time.Now().Add(-time.Second)
	nonTerminal := newTrackEventAt(t, aguievents.NewCustomEvent("node.progress", aguievents.WithValue(map[string]any{"p": 1})), base)
	finished := newTrackEventAt(t, aguievents.NewRunFinishedEvent("thread", "run"), base.Add(time.Millisecond))
	runErr := newTrackEventAt(t, aguievents.NewRunErrorEvent("boom"), base.Add(2*time.Millisecond))

	require.False(t, trackEndsWithTerminalRunEvent(nil))
	require.False(t, trackEndsWithTerminalRunEvent([]session.TrackEvent{}))
	require.False(t, trackEndsWithTerminalRunEvent([]session.TrackEvent{{Track: track.TrackAGUI, Timestamp: base}}))
	require.False(t, trackEndsWithTerminalRunEvent([]session.TrackEvent{{Track: track.TrackAGUI, Payload: []byte("{"), Timestamp: base}}))
	require.False(t, trackEndsWithTerminalRunEvent([]session.TrackEvent{finished, nonTerminal}))
	require.True(t, trackEndsWithTerminalRunEvent([]session.TrackEvent{nonTerminal, finished}))
	require.True(t, trackEndsWithTerminalRunEvent([]session.TrackEvent{nonTerminal, runErr}))
}

func TestMessagesSnapshotFollowSkipsWhenInitialAlreadyTerminal(t *testing.T) {
	base := time.Now().Add(-time.Second)
	initial := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")), base),
			newTrackEventAt(t, aguievents.NewTextMessageContentEvent("msg-1", "hello"), base.Add(time.Millisecond)),
			newTrackEventAt(t, aguievents.NewTextMessageEndEvent("msg-1"), base.Add(2*time.Millisecond)),
			newTrackEventAt(t, aguievents.NewRunFinishedEvent("thread", "real-run"), base.Add(3*time.Millisecond)),
		},
	}
	follow := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewCustomEvent("node.progress", aguievents.WithValue(map[string]any{"p": 1})),
				base.Add(4*time.Millisecond)),
		},
	}
	tr := &sequenceTracker{first: initial, second: follow}
	r := &runner{
		runner:                            noopBaseRunner{},
		userIDResolver:                    NewOptions().UserIDResolver,
		runAgentInputHook:                 NewOptions().RunAgentInputHook,
		appName:                           "demo",
		tracker:                           tr,
		flushInterval:                     time.Millisecond,
		timeout:                           50 * time.Millisecond,
		messagesSnapshotFollowEnabled:     true,
		messagesSnapshotFollowMaxDuration: 50 * time.Millisecond,
	}

	stream, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "req-run"})
	require.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 3)
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), collected[1])
	require.IsType(t, (*aguievents.RunFinishedEvent)(nil), collected[2])

	tr.mu.Lock()
	calls := tr.calls
	tr.mu.Unlock()
	require.Equal(t, 1, calls)
}

func TestMessagesSnapshotFollowEmitsRunErrorOnTerminalErrorEvent(t *testing.T) {
	base := time.Now().Add(-time.Second)
	initial := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")), base),
			newTrackEventAt(t, aguievents.NewTextMessageContentEvent("msg-1", "hello"), base.Add(time.Millisecond)),
			newTrackEventAt(t, aguievents.NewTextMessageEndEvent("msg-1"), base.Add(2*time.Millisecond)),
		},
	}
	follow := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewCustomEvent("node.progress", aguievents.WithValue(map[string]any{"p": 1})),
				base.Add(3*time.Millisecond)),
			newTrackEventAt(t, aguievents.NewRunErrorEvent("boom"), base.Add(4*time.Millisecond)),
		},
	}
	r := &runner{
		runner:                            noopBaseRunner{},
		userIDResolver:                    NewOptions().UserIDResolver,
		runAgentInputHook:                 NewOptions().RunAgentInputHook,
		appName:                           "demo",
		tracker:                           &sequenceTracker{first: initial, second: follow},
		flushInterval:                     time.Millisecond,
		timeout:                           100 * time.Millisecond,
		messagesSnapshotFollowEnabled:     true,
		messagesSnapshotFollowMaxDuration: 100 * time.Millisecond,
	}

	stream, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "req-run"})
	require.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 4)
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), collected[1])
	require.IsType(t, (*aguievents.CustomEvent)(nil), collected[2])
	errEvt, ok := collected[3].(*aguievents.RunErrorEvent)
	require.True(t, ok)
	require.Equal(t, "req-run", errEvt.RunID())
	require.Equal(t, "boom", errEvt.Message)
}

func TestMessagesSnapshotFollowEmitsRunErrorOnFollowGetEventsError(t *testing.T) {
	base := time.Now().Add(-time.Second)
	initial := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")), base),
			newTrackEventAt(t, aguievents.NewTextMessageContentEvent("msg-1", "hello"), base.Add(time.Millisecond)),
			newTrackEventAt(t, aguievents.NewTextMessageEndEvent("msg-1"), base.Add(2*time.Millisecond)),
		},
	}
	r := &runner{
		runner:                            noopBaseRunner{},
		userIDResolver:                    NewOptions().UserIDResolver,
		runAgentInputHook:                 NewOptions().RunAgentInputHook,
		appName:                           "demo",
		tracker:                           &errorAfterFirstTracker{first: initial, err: errors.New("boom")},
		flushInterval:                     time.Millisecond,
		timeout:                           100 * time.Millisecond,
		messagesSnapshotFollowEnabled:     true,
		messagesSnapshotFollowMaxDuration: 100 * time.Millisecond,
	}

	stream, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "req-run"})
	require.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 3)
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), collected[1])
	errEvt, ok := collected[2].(*aguievents.RunErrorEvent)
	require.True(t, ok)
	require.Equal(t, "req-run", errEvt.RunID())
	require.Contains(t, errEvt.Message, "follow track events: boom")
}

func TestMessagesSnapshotFollowEmitsTimeoutWhenNoTerminalEvent(t *testing.T) {
	base := time.Now().Add(-time.Second)
	initial := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")), base),
			newTrackEventAt(t, aguievents.NewTextMessageContentEvent("msg-1", "hello"), base.Add(time.Millisecond)),
			newTrackEventAt(t, aguievents.NewTextMessageEndEvent("msg-1"), base.Add(2*time.Millisecond)),
		},
	}
	empty := &session.TrackEvents{Track: track.TrackAGUI}
	r := &runner{
		runner:                            noopBaseRunner{},
		userIDResolver:                    NewOptions().UserIDResolver,
		runAgentInputHook:                 NewOptions().RunAgentInputHook,
		appName:                           "demo",
		tracker:                           &sequenceTracker{first: initial, second: empty},
		flushInterval:                     time.Millisecond,
		timeout:                           0,
		messagesSnapshotFollowEnabled:     true,
		messagesSnapshotFollowMaxDuration: 10 * time.Millisecond,
	}

	stream, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "req-run"})
	require.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 3)
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), collected[1])
	errEvt, ok := collected[2].(*aguievents.RunErrorEvent)
	require.True(t, ok)
	require.Equal(t, "req-run", errEvt.RunID())
	require.Equal(t, "messages snapshot follow timeout", errEvt.Message)
}

func TestMessagesSnapshotFollowSkipsInvalidAndNonIncrementalTrackEvents(t *testing.T) {
	base := time.Now().Add(-time.Second)
	initial := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEventAt(t, aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")), base),
			newTrackEventAt(t, aguievents.NewTextMessageContentEvent("msg-1", "hello"), base.Add(time.Millisecond)),
			newTrackEventAt(t, aguievents.NewTextMessageEndEvent("msg-1"), base.Add(2*time.Millisecond)),
		},
	}
	follow := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			// Same timestamp as the initial cursor should be ignored.
			{Track: track.TrackAGUI, Payload: []byte(`{"type":"CUSTOM","name":"ignored"}`), Timestamp: base.Add(2 * time.Millisecond)},
			// Invalid JSON should be skipped.
			{Track: track.TrackAGUI, Payload: []byte("{"), Timestamp: base.Add(3 * time.Millisecond)},
			// Empty payload should be skipped.
			{Track: track.TrackAGUI, Payload: nil, Timestamp: base.Add(4 * time.Millisecond)},
			newTrackEventAt(t, aguievents.NewRunFinishedEvent("thread", "real-run"), base.Add(5*time.Millisecond)),
		},
	}
	r := &runner{
		runner:                            noopBaseRunner{},
		userIDResolver:                    NewOptions().UserIDResolver,
		runAgentInputHook:                 NewOptions().RunAgentInputHook,
		appName:                           "demo",
		tracker:                           &sequenceTracker{first: initial, second: follow},
		flushInterval:                     time.Millisecond,
		timeout:                           100 * time.Millisecond,
		messagesSnapshotFollowEnabled:     true,
		messagesSnapshotFollowMaxDuration: 100 * time.Millisecond,
	}

	stream, err := r.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "req-run"})
	require.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	require.Len(t, collected, 3)
	require.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), collected[1])
	finished, ok := collected[2].(*aguievents.RunFinishedEvent)
	require.True(t, ok)
	require.Equal(t, "req-run", finished.RunID())
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

func newTrackEventAt(t *testing.T, evt aguievents.Event, ts time.Time) session.TrackEvent {
	t.Helper()
	payload, err := evt.ToJSON()
	require.NoError(t, err)
	return session.TrackEvent{
		Track:     track.TrackAGUI,
		Payload:   append([]byte(nil), payload...),
		Timestamp: ts,
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

type sequenceTracker struct {
	mu     sync.Mutex
	calls  int
	first  *session.TrackEvents
	second *session.TrackEvents
}

func (s *sequenceTracker) AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error {
	return nil
}

func (s *sequenceTracker) GetEvents(ctx context.Context, key session.Key, opts ...session.Option) (*session.TrackEvents, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return s.first, nil
	}
	return s.second, nil
}

func (s *sequenceTracker) Flush(ctx context.Context, key session.Key) error {
	return nil
}

type blockingTracker struct {
	unblock <-chan struct{}
	events  *session.TrackEvents
}

func (b *blockingTracker) AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error {
	return nil
}

func (b *blockingTracker) GetEvents(ctx context.Context, key session.Key, opts ...session.Option) (*session.TrackEvents, error) {
	<-b.unblock
	return b.events, nil
}

func (b *blockingTracker) Flush(ctx context.Context, key session.Key) error {
	return nil
}

type errorAfterFirstTracker struct {
	mu    sync.Mutex
	calls int
	first *session.TrackEvents
	err   error
}

func (t *errorAfterFirstTracker) AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error {
	return nil
}

func (t *errorAfterFirstTracker) GetEvents(ctx context.Context, key session.Key, opts ...session.Option) (*session.TrackEvents, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	if t.calls == 1 {
		return t.first, nil
	}
	return nil, t.err
}

func (t *errorAfterFirstTracker) Flush(ctx context.Context, key session.Key) error {
	return nil
}

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

func TestMessagesSnapshotStopsWhenContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := &runner{}
	sessionKey := session.Key{AppName: "demo", UserID: "user", SessionID: "thread"}
	input := &runInput{key: sessionKey, threadID: "thread", runID: "run"}
	events := make(chan aguievents.Event)

	r.messagesSnapshot(ctx, input, events)

	_, ok := <-events
	assert.False(t, ok)
}

func TestMessagesSnapshotStopsWhenCanceledWhileLoadingHistory(t *testing.T) {
	unblock := make(chan struct{})
	trackEvents := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEvent(t, aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user"))),
			newTrackEvent(t, aguievents.NewTextMessageEndEvent("user-1")),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{
		appName: "demo",
		tracker: &blockingTracker{unblock: unblock, events: trackEvents},
	}
	sessionKey := session.Key{AppName: "demo", UserID: "user", SessionID: "thread"}
	input := &runInput{key: sessionKey, threadID: "thread", runID: "run"}
	events := make(chan aguievents.Event)
	first := make(chan aguievents.Event, 1)
	done := make(chan struct{})

	go func() {
		r.messagesSnapshot(ctx, input, events)
		close(done)
	}()

	go func() {
		first <- <-events
		cancel()
		close(unblock)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for messages snapshot to exit")
	}

	evt := <-first
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)

	_, ok := <-events
	assert.False(t, ok)
}

func TestMessagesSnapshotDropsRunFinishedWhenChannelFullAndContextDone(t *testing.T) {
	unblock := make(chan struct{})
	close(unblock)
	trackEvents := &session.TrackEvents{
		Track: track.TrackAGUI,
		Events: []session.TrackEvent{
			newTrackEvent(t, aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user"))),
			newTrackEvent(t, aguievents.NewTextMessageEndEvent("user-1")),
		},
	}
	snapshotSeen := make(chan struct{})
	releaseFinish := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{
		appName: "demo",
		tracker: &blockingTracker{unblock: unblock, events: trackEvents},
		translateCallbacks: translator.NewCallbacks().RegisterAfterTranslate(
			func(ctx context.Context, evt aguievents.Event) (aguievents.Event, error) {
				switch evt.(type) {
				case *aguievents.MessagesSnapshotEvent:
					close(snapshotSeen)
				case *aguievents.RunFinishedEvent:
					<-releaseFinish
				}
				return evt, nil
			},
		),
	}
	sessionKey := session.Key{AppName: "demo", UserID: "user", SessionID: "thread"}
	input := &runInput{key: sessionKey, threadID: "thread", runID: "run"}
	events := make(chan aguievents.Event, 2)
	done := make(chan struct{})

	go func() {
		r.messagesSnapshot(ctx, input, events)
		close(done)
	}()

	select {
	case <-snapshotSeen:
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for snapshot event")
	}

	assert.Eventually(t, func() bool {
		return len(events) == 2
	}, time.Second, 10*time.Millisecond)

	cancel()
	close(releaseFinish)

	select {
	case <-done:
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for messages snapshot to exit")
	}

	collected := collectAGUIEvents(t, events)
	require.Len(t, collected, 2)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), collected[0])
	assert.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), collected[1])
}
