//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package track

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestTrackerAppendCreatesSession(t *testing.T) {
	svc := inmemory.NewSessionService()
	tracker, err := New(svc)
	require.NoError(t, err)
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "thread",
	}
	err = tracker.AppendEvent(context.Background(), key, aguievents.NewTextMessageStartEvent("msg", aguievents.WithRole("user")))
	require.NoError(t, err)
	sess, err := svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.Nil(t, sess)
	require.NoError(t, tracker.Flush(context.Background(), key))
	sess, err = svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.NotNil(t, trackEvents)
	require.Len(t, trackEvents.Events, 1)
}

func TestTrackerNewRequiresTrackService(t *testing.T) {
	tracker, err := New(&serviceWithoutTrack{})
	require.Error(t, err)
	require.Nil(t, tracker)
}

func TestTrackerAppendEventErrors(t *testing.T) {
	ctx := context.Background()
	validKey := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	t.Run("nil event", func(t *testing.T) {
		tracker, err := New(inmemory.NewSessionService())
		require.NoError(t, err)
		err = tracker.AppendEvent(ctx, validKey, nil)
		require.ErrorContains(t, err, "event is nil")
	})
	t.Run("invalid session key", func(t *testing.T) {
		tracker, err := New(inmemory.NewSessionService())
		require.NoError(t, err)
		err = tracker.AppendEvent(ctx, session.Key{}, aguievents.NewRunStartedEvent("thread", "run"))
		require.ErrorContains(t, err, "session key")
	})
	t.Run("marshal error", func(t *testing.T) {
		tracker, err := New(inmemory.NewSessionService())
		require.NoError(t, err)
		err = tracker.AppendEvent(ctx, validKey, &failingEvent{})
		require.ErrorContains(t, err, "marshal event")
	})
	t.Run("get session error", func(t *testing.T) {
		svc := newHookSessionService()
		svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return nil, errors.New("boom")
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		err = tracker.AppendEvent(ctx, validKey, aguievents.NewRunStartedEvent("thread", "run"))
		require.NoError(t, err)
		err = tracker.Flush(ctx, validKey)
		require.ErrorContains(t, err, "get session: boom")
	})
	t.Run("create session error", func(t *testing.T) {
		svc := newHookSessionService()
		svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return nil, nil
		}
		svc.createSessionFn = func(context.Context, session.Key, session.StateMap, ...session.Option) (*session.Session, error) {
			return nil, errors.New("fail")
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		err = tracker.AppendEvent(ctx, validKey, aguievents.NewRunStartedEvent("thread", "run"))
		require.NoError(t, err)
		err = tracker.Flush(ctx, validKey)
		require.ErrorContains(t, err, "create session: fail")
	})
	t.Run("append track event error", func(t *testing.T) {
		svc := newHookSessionService()
		appendErr := errors.New("append broke")
		svc.appendTrackFn = func(context.Context, *session.Session, *session.TrackEvent, ...session.Option) error {
			return appendErr
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		err = tracker.AppendEvent(ctx, validKey, aguievents.NewRunStartedEvent("thread", "run"))
		require.NoError(t, err)
		err = tracker.Flush(ctx, validKey)
		require.ErrorContains(t, err, "persist events: append track event")
		require.ErrorIs(t, err, appendErr)
	})
}

func TestTrackerAppendEventUsesCurrentTimestamp(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc)
	require.NoError(t, err)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	eventWithTs := aguievents.NewRunFinishedEvent("thread", "run")
	eventTimestamp := time.Now().Add(-time.Minute).UnixMilli()
	eventWithTs.SetTimestamp(eventTimestamp)

	require.NoError(t, tracker.AppendEvent(ctx, key, eventWithTs))
	before := time.Now()
	require.NoError(t, tracker.Flush(ctx, key))
	after := time.Now()

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.NotNil(t, trackEvents)
	require.Len(t, trackEvents.Events, 1)

	recorded := trackEvents.Events[0].Timestamp
	require.True(t, recorded.After(time.UnixMilli(eventTimestamp)))
	require.WithinDuration(t, after, recorded, time.Second)
	require.WithinDuration(t, before, recorded, time.Second*2)
}

func TestTrackerReuseEnsuredSession(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()

	var getCalls, createCalls int
	svc.getSessionFn = func(ctx context.Context, key session.Key, opts ...session.Option) (*session.Session, error) {
		getCalls++
		return nil, nil
	}
	svc.createSessionFn = func(ctx context.Context, key session.Key, state session.StateMap, opts ...session.Option) (*session.Session, error) {
		createCalls++
		return svc.SessionService.CreateSession(ctx, key, state, opts...)
	}

	tracker, err := New(svc)
	require.NoError(t, err)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewRunFinishedEvent("thread", "run")))
	require.NoError(t, tracker.Flush(ctx, key))

	require.Equal(t, 1, getCalls)
	require.Equal(t, 1, createCalls)

	stored, err := svc.SessionService.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := stored.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 2)
	require.Equal(t, "run", trackEvents.Events[0].RequestID)
	require.Equal(t, "run", trackEvents.Events[1].RequestID)
}

func TestTrackerUsesEffectiveRunnerRequestID(t *testing.T) {
	ctx := ContextWithRequestID(context.Background(), "resolved-request")
	svc := inmemory.NewSessionService()
	tracker, err := New(svc)
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

	require.NoError(t, tracker.AppendEvent(
		ctx,
		key,
		aguievents.NewRunStartedEvent("thread", "protocol-run"),
	))
	require.NoError(t, tracker.Flush(context.Background(), key))
	stored, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := stored.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 1)
	require.Equal(t, "resolved-request", trackEvents.Events[0].RequestID)
}

func TestTrackerGetEventsForwardsOptions(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	afterTime := time.Now().Add(-time.Hour)

	var got *session.Options
	var gotKey session.Key
	var gotTrack session.Track
	svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
		return nil, errors.New("get session should not be called")
	}
	svc.getTrackEventsFn = func(ctx context.Context, key session.Key, track session.Track, opts ...session.Option) (*session.TrackEvents, error) {
		opt := &session.Options{}
		for _, o := range opts {
			o(opt)
		}
		got = opt
		gotKey = key
		gotTrack = track
		return &session.TrackEvents{Track: track}, nil
	}

	tracker, err := New(svc)
	require.NoError(t, err)

	_, err = tracker.GetEvents(ctx, key, session.WithEventTime(afterTime))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.True(t, got.EventTime.Equal(afterTime))
	require.Equal(t, key, gotKey)
	require.Equal(t, TrackAGUI, gotTrack)
}

func TestTrackerGetEventsFallbackForwardsOptions(t *testing.T) {
	ctx := context.Background()
	svc := newTrackOnlyHookService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	afterTime := time.Now().Add(-time.Hour)
	var got *session.Options
	svc.getSessionFn = func(ctx context.Context, key session.Key, opts ...session.Option) (*session.Session, error) {
		opt := &session.Options{}
		for _, o := range opts {
			o(opt)
		}
		got = opt
		sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
		require.NoError(t, sess.AppendTrackEvent(&session.TrackEvent{Track: TrackAGUI, Timestamp: time.Now()}))
		return sess, nil
	}
	tracker, err := New(svc)
	require.NoError(t, err)
	_, err = tracker.GetEvents(ctx, key, session.WithEventTime(afterTime))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.True(t, got.EventTime.Equal(afterTime))
}

func TestTrackerAppendEventAggregateError(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc, WithAggregatorFactory(func(context.Context, ...aggregator.Option) aggregator.Aggregator {
		return &errorAggregator{}
	}))
	require.NoError(t, err)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	err = tracker.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run"))
	require.ErrorContains(t, err, "aggregate event: agg boom")
}

func TestTrackerSessionUnavailableWhenCreateReturnsNil(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	var getCalls, createCalls int
	svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
		getCalls++
		return nil, nil
	}
	svc.createSessionFn = func(context.Context, session.Key, session.StateMap, ...session.Option) (*session.Session, error) {
		createCalls++
		return nil, nil
	}
	svc.appendTrackFn = func(context.Context, *session.Session, *session.TrackEvent, ...session.Option) error {
		return errors.New("should not be called")
	}

	tracker, err := New(svc)
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

	err = tracker.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run"))
	require.NoError(t, err)
	err = tracker.Flush(ctx, key)
	require.ErrorContains(t, err, "session unavailable")
	require.Equal(t, 1, getCalls)
	require.Equal(t, 1, createCalls)
}

func TestTrackerEnsureSessionUsesExisting(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	sess := session.NewSession("app", "user", "thread")
	var createCalls int
	svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
		return sess, nil
	}
	svc.createSessionFn = func(context.Context, session.Key, session.StateMap, ...session.Option) (*session.Session, error) {
		createCalls++
		return nil, errors.New("should not create")
	}
	svc.appendTrackFn = func(context.Context, *session.Session, *session.TrackEvent, ...session.Option) error {
		return nil
	}

	trk, err := New(svc, WithAggregationOption(aggregator.WithEnabled(false)))
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

	require.NoError(t, trk.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run")))
	require.NoError(t, trk.Flush(ctx, key))
	require.Equal(t, 0, createCalls)

	internal := trk.(*tracker)
	state := internal.getSessionState(ctx, key)
	require.Equal(t, sess, state.session)
}

func TestTrackerGetEventsErrors(t *testing.T) {
	ctx := context.Background()
	validKey := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

	tracker, err := New(inmemory.NewSessionService())
	require.NoError(t, err)

	_, err = tracker.GetEvents(ctx, session.Key{})
	require.ErrorContains(t, err, "session key")

	t.Run("get session error", func(t *testing.T) {
		svc := newTrackOnlyHookService()
		svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return nil, errors.New("nope")
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		_, err = tracker.GetEvents(ctx, validKey)
		require.ErrorContains(t, err, "get session: nope")
	})

	t.Run("session not found", func(t *testing.T) {
		svc := newTrackOnlyHookService()
		svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return nil, nil
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		_, err = tracker.GetEvents(ctx, validKey)
		require.ErrorContains(t, err, "session not found")
	})

	t.Run("track events error", func(t *testing.T) {
		svc := newTrackOnlyHookService()
		svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return &session.Session{
				AppName: validKey.AppName,
				UserID:  validKey.UserID,
				ID:      validKey.SessionID,
			}, nil
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		_, err = tracker.GetEvents(ctx, validKey)
		require.ErrorContains(t, err, "tracks is empty")
	})
	t.Run("reader error", func(t *testing.T) {
		svc := newHookSessionService()
		svc.getTrackEventsFn = func(context.Context, session.Key, session.Track, ...session.Option) (*session.TrackEvents, error) {
			return nil, errors.New("reader failed")
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		_, err = tracker.GetEvents(ctx, validKey)
		require.ErrorContains(t, err, "get track events: reader failed")
	})
}

func TestTrackerGetEventsSuccess(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc)
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

	first := aguievents.NewTextMessageStartEvent("msg", aguievents.WithRole("user"))
	require.NoError(t, tracker.AppendEvent(ctx, key, first))
	require.NoError(t, tracker.Flush(ctx, key))

	events, err := tracker.GetEvents(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, events)
	require.Len(t, events.Events, 1)
	parsed, err := aguievents.EventFromJSON(events.Events[0].Payload)
	require.NoError(t, err)
	start, ok := parsed.(*aguievents.TextMessageStartEvent)
	require.True(t, ok)
	require.Equal(t, first.MessageID, start.MessageID)
}

func TestTrackerSnapshotsAppendOutputBeforeCallerMutation(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("aggregation_enabled_%t", enabled), func(t *testing.T) {
			ctx := context.Background()
			svc := inmemory.NewSessionService()
			tracker, err := New(svc, WithAggregationOption(aggregator.WithEnabled(enabled)))
			require.NoError(t, err)
			key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

			values := []any{"first", map[string]any{"nested": "original"}}
			event := aguievents.NewCustomEvent("original", aguievents.WithValue(values))
			require.NoError(t, tracker.AppendEvent(ctx, key, event))
			event.Name = "mutated"
			values[0] = "changed"
			values[1].(map[string]any)["nested"] = "changed"

			require.NoError(t, tracker.Flush(ctx, key))
			trackEvents, err := tracker.GetEvents(ctx, key)
			require.NoError(t, err)
			require.Len(t, trackEvents.Events, 1)
			parsed, err := aguievents.EventFromJSON(trackEvents.Events[0].Payload)
			require.NoError(t, err)
			custom, ok := parsed.(*aguievents.CustomEvent)
			require.True(t, ok)
			require.Equal(t, "original", custom.Name)
			require.Equal(t, []any{"first", map[string]any{"nested": "original"}}, custom.Value)
		})
	}
}

func TestTrackerSupportsCustomAggregatorMergingMultipleCustomEvents(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc, WithAggregatorFactory(
		func(context.Context, ...aggregator.Option) aggregator.Aggregator {
			return &customEventMergingAggregator{}
		},
	))
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("first")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("second")))
	require.NoError(t, tracker.Flush(ctx, key))

	trackEvents, err := tracker.GetEvents(ctx, key)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 1)
	parsed, err := aguievents.EventFromJSON(trackEvents.Events[0].Payload)
	require.NoError(t, err)
	custom, ok := parsed.(*aguievents.CustomEvent)
	require.True(t, ok)
	require.Equal(t, "merged", custom.Name)
	require.Equal(t, []any{"first", "second"}, custom.Value)
}

func TestTrackerSnapshotsCustomAggregatorOutputs(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	extraValue := map[string]any{"state": "original"}
	extra := aguievents.NewCustomEvent("extra", aguievents.WithValue(extraValue))
	tracker, err := New(svc, WithAggregatorFactory(
		func(context.Context, ...aggregator.Option) aggregator.Aggregator {
			return &multiOutputAggregator{extra: extra}
		},
	))
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	inputValue := map[string]any{"state": "original"}
	input := aguievents.NewCustomEvent("input", aguievents.WithValue(inputValue))

	require.NoError(t, tracker.AppendEvent(ctx, key, input))
	input.Name = "mutated-input"
	inputValue["state"] = "changed"
	extra.Name = "mutated-extra"
	extraValue["state"] = "changed"
	require.NoError(t, tracker.Flush(ctx, key))

	trackEvents, err := tracker.GetEvents(ctx, key)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 2)
	for i, wantName := range []string{"input", "extra"} {
		parsed, parseErr := aguievents.EventFromJSON(trackEvents.Events[i].Payload)
		require.NoError(t, parseErr)
		custom, ok := parsed.(*aguievents.CustomEvent)
		require.True(t, ok)
		require.Equal(t, wantName, custom.Name)
		require.Equal(t, map[string]any{"state": "original"}, custom.Value)
	}
}

func TestTrackerAggregatesTextContent(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc)
	require.NoError(t, err)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageStartEvent("msg",
		aguievents.WithRole("assistant"))))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageContentEvent("msg", "hello")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageContentEvent("msg", "world")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageEndEvent("msg")))
	require.NoError(t, tracker.Flush(ctx, key))

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 3)

	parsed, err := aguievents.EventFromJSON(trackEvents.Events[1].Payload)
	require.NoError(t, err)
	content, ok := parsed.(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, "helloworld", content.Delta)
}

func TestTrackerAggregatesToolCallArgs(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc)
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewToolCallStartEvent("call-1", "create_document",
		aguievents.WithParentMessageID("msg"))))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewToolCallArgsEvent("call-1", `{"content":"12`)))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewToolCallArgsEvent("call-1", `34"}`)))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewToolCallEndEvent("call-1")))
	require.NoError(t, tracker.Flush(ctx, key))
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 3)
	parsed, err := aguievents.EventFromJSON(trackEvents.Events[1].Payload)
	require.NoError(t, err)
	args, ok := parsed.(*aguievents.ToolCallArgsEvent)
	require.True(t, ok)
	require.Equal(t, "call-1", args.ToolCallID)
	require.Equal(t, `{"content":"1234"}`, args.Delta)
}

func TestTrackerAggregationDisabled(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc, WithAggregationOption(aggregator.WithEnabled(false)))
	require.NoError(t, err)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageStartEvent("msg",
		aguievents.WithRole("assistant"))))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageContentEvent("msg", "hello")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageContentEvent("msg", "world")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageEndEvent("msg")))
	require.NoError(t, tracker.Flush(ctx, key))

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 4)

	firstPayload, err := aguievents.EventFromJSON(trackEvents.Events[1].Payload)
	require.NoError(t, err)
	firstContent, ok := firstPayload.(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, "hello", firstContent.Delta)

	secondPayload, err := aguievents.EventFromJSON(trackEvents.Events[2].Payload)
	require.NoError(t, err)
	secondContent, ok := secondPayload.(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, "world", secondContent.Delta)
}

func TestTrackerFlushPersistsPendingAggregation(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc)
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageStartEvent("msg",
		aguievents.WithRole("assistant"))))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageContentEvent("msg", "hi ")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageContentEvent("msg", "there")))
	require.NoError(t, tracker.Flush(ctx, key))
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 2)
	parsed, err := aguievents.EventFromJSON(trackEvents.Events[1].Payload)
	require.NoError(t, err)
	content, ok := parsed.(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, "hi there", content.Delta)
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageContentEvent("msg", " again")))
	require.NoError(t, tracker.Close(ctx, key))
	sess, err = svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err = sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 3)
	parsed, err = aguievents.EventFromJSON(trackEvents.Events[2].Payload)
	require.NoError(t, err)
	content, ok = parsed.(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, " again", content.Delta)
	err = tracker.Flush(ctx, key)
	require.ErrorContains(t, err, "session state not found")
}

func TestTrackerFlushFailureDropsRemainingBatchBeforeNewEvents(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	var appendCalls int
	failSecond := true
	svc.appendTrackFn = func(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
		appendCalls++
		if failSecond && appendCalls == 2 {
			return errors.New("append broke")
		}
		return svc.SessionService.AppendTrackEvent(ctx, sess, evt, opts...)
	}
	tracker, err := New(svc, WithAggregationOption(aggregator.WithEnabled(false)))
	require.NoError(t, err)
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("middle")))
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewRunFinishedEvent("thread", "run")))
	err = tracker.Flush(ctx, key)
	require.ErrorContains(t, err, "append broke")
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("after")))
	failSecond = false
	require.NoError(t, tracker.Flush(ctx, key))
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 2)
	requireTrackEventType(t, trackEvents.Events[0], aguievents.EventTypeRunStarted)
	requireTrackEventType(t, trackEvents.Events[1], aguievents.EventTypeCustom)
}

func TestTrackerRepeatedFlushFailuresDoNotRetainFailedBatches(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	fail := true
	svc.appendTrackFn = func(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
		if fail {
			return errors.New("append broke")
		}
		return svc.SessionService.AppendTrackEvent(ctx, sess, evt, opts...)
	}
	tracker, err := New(svc, WithAggregationOption(aggregator.WithEnabled(false)))
	require.NoError(t, err)
	for range 100 {
		require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("failed")))
		err = tracker.Flush(ctx, key)
		require.ErrorContains(t, err, "append broke")
	}
	fail = false
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("after")))
	require.NoError(t, tracker.Flush(ctx, key))
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 1)
	parsed, err := aguievents.EventFromJSON(trackEvents.Events[0].Payload)
	require.NoError(t, err)
	custom, ok := parsed.(*aguievents.CustomEvent)
	require.True(t, ok)
	require.Equal(t, "after", custom.Name)
}

func TestTrackerFlushDoesNotRetryAmbiguouslyCommittedEvent(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	first := true
	svc.appendTrackFn = func(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
		if err := svc.SessionService.AppendTrackEvent(ctx, sess, evt, opts...); err != nil {
			return err
		}
		if first {
			first = false
			return errors.New("acknowledgement lost")
		}
		return nil
	}
	tracker, err := New(svc, WithAggregationOption(aggregator.WithEnabled(false)))
	require.NoError(t, err)
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("original")))
	err = tracker.Flush(ctx, key)
	require.ErrorContains(t, err, "acknowledgement lost")
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("after")))
	require.NoError(t, tracker.Flush(ctx, key))
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 2)
	for i, want := range []string{"original", "after"} {
		parsed, parseErr := aguievents.EventFromJSON(trackEvents.Events[i].Payload)
		require.NoError(t, parseErr)
		custom, ok := parsed.(*aguievents.CustomEvent)
		require.True(t, ok)
		require.Equal(t, want, custom.Name)
	}
}

func TestTrackerCloseFailureReleasesStateBeforeNewEvents(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	var appendCalls int
	failSecond := true
	svc.appendTrackFn = func(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
		appendCalls++
		if failSecond && appendCalls == 2 {
			return errors.New("append broke")
		}
		return svc.SessionService.AppendTrackEvent(ctx, sess, evt, opts...)
	}
	tr, err := New(svc, WithAggregationOption(aggregator.WithEnabled(false)))
	require.NoError(t, err)
	require.NoError(t, tr.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run")))
	require.NoError(t, tr.AppendEvent(ctx, key, aguievents.NewCustomEvent("middle")))
	require.NoError(t, tr.AppendEvent(ctx, key, aguievents.NewRunFinishedEvent("thread", "run")))
	err = tr.Close(ctx, key)
	require.ErrorContains(t, err, "append broke")
	impl, ok := tr.(*tracker)
	require.True(t, ok)
	require.Nil(t, impl.getExistingSessionState(key))
	require.NoError(t, tr.AppendEvent(ctx, key, aguievents.NewCustomEvent("after")))
	failSecond = false
	require.NoError(t, tr.Close(ctx, key))
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 2)
	requireTrackEventType(t, trackEvents.Events[0], aguievents.EventTypeRunStarted)
	requireTrackEventType(t, trackEvents.Events[1], aguievents.EventTypeCustom)
	err = tr.Flush(ctx, key)
	require.ErrorContains(t, err, "session state not found")
}

func TestTrackerAppendEventDuringCloseReturnsErrorAndDoesNotLoseLaterEvents(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	svc.appendTrackFn = func(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
		startOnce.Do(func() {
			close(started)
		})
		<-release
		return svc.SessionService.AppendTrackEvent(ctx, sess, evt, opts...)
	}
	tracker, err := New(svc, WithAggregationOption(aggregator.WithEnabled(false)))
	require.NoError(t, err)
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run")))
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- tracker.Close(ctx, key)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for close persistence")
	}
	err = tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("during"))
	require.ErrorContains(t, err, "session state is closing")
	close(release)
	require.NoError(t, <-closeDone)
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("after")))
	require.NoError(t, tracker.Flush(ctx, key))
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 2)
	requireTrackEventType(t, trackEvents.Events[0], aguievents.EventTypeRunStarted)
	requireTrackEventType(t, trackEvents.Events[1], aguievents.EventTypeCustom)
}

func TestTrackerAppendEventDoesNotWaitForInFlightFlushPersistence(t *testing.T) {
	ctx := context.Background()
	svc := newHookSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	svc.appendTrackFn = func(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
		startOnce.Do(func() {
			close(started)
		})
		<-release
		return svc.SessionService.AppendTrackEvent(ctx, sess, evt, opts...)
	}
	tracker, err := New(svc, WithAggregationOption(aggregator.WithEnabled(false)))
	require.NoError(t, err)
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run")))
	flushDone := make(chan error, 1)
	go func() {
		flushDone <- tracker.Flush(ctx, key)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for flush persistence")
	}
	appendDone := make(chan error, 1)
	go func() {
		appendDone <- tracker.AppendEvent(ctx, key, aguievents.NewCustomEvent("after"))
	}()
	select {
	case err := <-appendDone:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		require.FailNow(t, "append waited for flush persistence")
	}
	close(release)
	require.NoError(t, <-flushDone)
}

func TestTrackerFlushReturnsAggregatorError(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	agg := &stubAggregator{flushErr: errors.New("flush fail")}
	tracker, err := New(svc,
		WithAggregatorFactory(
			func(ctx context.Context, opt ...aggregator.Option) aggregator.Aggregator {
				return agg
			},
		),
	)
	require.NoError(t, err)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "run")))

	err = tracker.Flush(ctx, key)
	require.ErrorContains(t, err, "aggregator flush: flush fail")
}

func TestTrackerFlushAndCloseMissingState(t *testing.T) {
	ctx := context.Background()
	tracker, err := New(inmemory.NewSessionService())
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	err = tracker.Flush(ctx, key)
	require.ErrorContains(t, err, "session state not found")
	require.NoError(t, tracker.Close(ctx, key))
}

func TestTrackerCloseInvalidKey(t *testing.T) {
	tracker, err := New(inmemory.NewSessionService())
	require.NoError(t, err)
	err = tracker.Close(context.Background(), session.Key{})
	require.ErrorContains(t, err, "session key")
}

func requireTrackEventType(t *testing.T, trackEvent session.TrackEvent, eventType aguievents.EventType) {
	t.Helper()
	evt, err := aguievents.EventFromJSON(trackEvent.Payload)
	require.NoError(t, err)
	require.Equal(t, eventType, evt.Type())
}

type serviceWithoutTrack struct{}

func (serviceWithoutTrack) CreateSession(ctx context.Context, key session.Key, state session.StateMap, opts ...session.Option) (*session.Session, error) {
	return nil, nil
}

func (serviceWithoutTrack) GetSession(ctx context.Context, key session.Key, opts ...session.Option) (*session.Session, error) {
	return nil, nil
}

func (serviceWithoutTrack) ListSessions(ctx context.Context, key session.UserKey, opts ...session.Option) ([]*session.Session, error) {
	return nil, nil
}

func (serviceWithoutTrack) DeleteSession(ctx context.Context, key session.Key, opts ...session.Option) error {
	return nil
}

func (serviceWithoutTrack) UpdateAppState(ctx context.Context, app string, state session.StateMap) error {
	return nil
}

func (serviceWithoutTrack) DeleteAppState(ctx context.Context, app string, key string) error {
	return nil
}

func (serviceWithoutTrack) ListAppStates(ctx context.Context, app string) (session.StateMap, error) {
	return nil, nil
}

func (serviceWithoutTrack) UpdateUserState(ctx context.Context, key session.UserKey, state session.StateMap) error {
	return nil
}

func (serviceWithoutTrack) ListUserStates(ctx context.Context, key session.UserKey) (session.StateMap, error) {
	return nil, nil
}

func (serviceWithoutTrack) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	return nil
}

func (serviceWithoutTrack) DeleteUserState(ctx context.Context, key session.UserKey, stateKey string) error {
	return nil
}

func (serviceWithoutTrack) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event, opts ...session.Option) error {
	return nil
}

func (serviceWithoutTrack) CreateSessionSummary(ctx context.Context, sess *session.Session, summary string, force bool) error {
	return nil
}

func (serviceWithoutTrack) EnqueueSummaryJob(ctx context.Context, sess *session.Session, summary string, force bool) error {
	return nil
}

func (serviceWithoutTrack) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	return "", false
}

func (serviceWithoutTrack) Close() error {
	return nil
}

type stubAggregator struct {
	mu       sync.Mutex
	flushErr error
	flushCh  chan struct{}
}

type customEventMergingAggregator struct {
	names []string
}

type multiOutputAggregator struct {
	extra aguievents.Event
}

func (a *multiOutputAggregator) Append(_ context.Context, event aguievents.Event) ([]aguievents.Event, error) {
	return []aguievents.Event{event, a.extra}, nil
}

func (*multiOutputAggregator) Flush(context.Context) ([]aguievents.Event, error) {
	return nil, nil
}

func (a *customEventMergingAggregator) Append(_ context.Context, event aguievents.Event) ([]aguievents.Event, error) {
	custom, ok := event.(*aguievents.CustomEvent)
	if !ok {
		return []aguievents.Event{event}, nil
	}
	a.names = append(a.names, custom.Name)
	return nil, nil
}

func (a *customEventMergingAggregator) Flush(context.Context) ([]aguievents.Event, error) {
	if len(a.names) == 0 {
		return nil, nil
	}
	names := append([]string(nil), a.names...)
	a.names = nil
	return []aguievents.Event{aguievents.NewCustomEvent("merged", aguievents.WithValue(names))}, nil
}

func (s *stubAggregator) Append(ctx context.Context, event aguievents.Event) ([]aguievents.Event, error) {
	return []aguievents.Event{event}, nil
}

func (s *stubAggregator) Flush(ctx context.Context) ([]aguievents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flushCh != nil {
		select {
		case s.flushCh <- struct{}{}:
		default:
		}
	}
	if s.flushErr != nil {
		return nil, s.flushErr
	}
	return nil, nil
}

type hookSessionService struct {
	*inmemory.SessionService
	getSessionFn     func(context.Context, session.Key, ...session.Option) (*session.Session, error)
	createSessionFn  func(context.Context, session.Key, session.StateMap, ...session.Option) (*session.Session, error)
	appendTrackFn    func(context.Context, *session.Session, *session.TrackEvent, ...session.Option) error
	getTrackEventsFn func(context.Context, session.Key, session.Track, ...session.Option) (*session.TrackEvents, error)
}

func newHookSessionService() *hookSessionService {
	return &hookSessionService{SessionService: inmemory.NewSessionService()}
}

func (s *hookSessionService) GetSession(ctx context.Context, key session.Key, opts ...session.Option) (*session.Session, error) {
	if s.getSessionFn != nil {
		return s.getSessionFn(ctx, key, opts...)
	}
	return s.SessionService.GetSession(ctx, key, opts...)
}

func (s *hookSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, opts ...session.Option) (*session.Session, error) {
	if s.createSessionFn != nil {
		return s.createSessionFn(ctx, key, state, opts...)
	}
	return s.SessionService.CreateSession(ctx, key, state, opts...)
}

func (s *hookSessionService) AppendTrackEvent(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
	if s.appendTrackFn != nil {
		return s.appendTrackFn(ctx, sess, evt, opts...)
	}
	return s.SessionService.AppendTrackEvent(ctx, sess, evt, opts...)
}

func (s *hookSessionService) GetTrackEvents(ctx context.Context, key session.Key, track session.Track, opts ...session.Option) (*session.TrackEvents, error) {
	if s.getTrackEventsFn != nil {
		return s.getTrackEventsFn(ctx, key, track, opts...)
	}
	return s.SessionService.GetTrackEvents(ctx, key, track, opts...)
}

type trackOnlyHookService struct {
	serviceWithoutTrack
	store           *inmemory.SessionService
	getSessionFn    func(context.Context, session.Key, ...session.Option) (*session.Session, error)
	createSessionFn func(context.Context, session.Key, session.StateMap, ...session.Option) (*session.Session, error)
	appendTrackFn   func(context.Context, *session.Session, *session.TrackEvent, ...session.Option) error
}

func newTrackOnlyHookService() *trackOnlyHookService {
	return &trackOnlyHookService{store: inmemory.NewSessionService()}
}

func (s *trackOnlyHookService) GetSession(ctx context.Context, key session.Key, opts ...session.Option) (*session.Session, error) {
	if s.getSessionFn != nil {
		return s.getSessionFn(ctx, key, opts...)
	}
	return s.store.GetSession(ctx, key, opts...)
}

func (s *trackOnlyHookService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, opts ...session.Option) (*session.Session, error) {
	if s.createSessionFn != nil {
		return s.createSessionFn(ctx, key, state, opts...)
	}
	return s.store.CreateSession(ctx, key, state, opts...)
}

func (s *trackOnlyHookService) AppendTrackEvent(ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option) error {
	if s.appendTrackFn != nil {
		return s.appendTrackFn(ctx, sess, evt, opts...)
	}
	return s.store.AppendTrackEvent(ctx, sess, evt, opts...)
}

type failingEvent struct {
	base aguievents.BaseEvent
}

func (f *failingEvent) Type() aguievents.EventType {
	return aguievents.EventType("failing")
}

func (f *failingEvent) Timestamp() *int64 {
	return f.base.Timestamp()
}

func (f *failingEvent) SetTimestamp(ts int64) {
	f.base.SetTimestamp(ts)
}

func (f *failingEvent) ThreadID() string { return "" }

func (f *failingEvent) RunID() string { return "" }

func (f *failingEvent) Validate() error { return nil }

func (f *failingEvent) ToJSON() ([]byte, error) {
	return nil, errors.New("fail to marshal")
}

func (f *failingEvent) GetBaseEvent() *aguievents.BaseEvent {
	return &f.base
}

type errorAggregator struct{}

func (e *errorAggregator) Append(context.Context, aguievents.Event) ([]aguievents.Event, error) {
	return nil, errors.New("agg boom")
}

func (e *errorAggregator) Flush(context.Context) ([]aguievents.Event, error) {
	return nil, nil
}
