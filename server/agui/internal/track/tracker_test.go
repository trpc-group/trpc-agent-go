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
		require.ErrorContains(t, err, "create session: fail")
	})
	t.Run("append track event error", func(t *testing.T) {
		svc := newHookSessionService()
		svc.appendTrackFn = func(context.Context, *session.Session, *session.TrackEvent, ...session.Option) error {
			return errors.New("append broke")
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		err = tracker.AppendEvent(ctx, validKey, aguievents.NewRunStartedEvent("thread", "run"))
		require.ErrorContains(t, err, "persist events: append track event")
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

	before := time.Now()
	require.NoError(t, tracker.AppendEvent(ctx, key, eventWithTs))
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

	require.Equal(t, 1, getCalls)
	require.Equal(t, 1, createCalls)

	stored, err := svc.SessionService.GetSession(ctx, key)
	require.NoError(t, err)
	trackEvents, err := stored.GetTrackEvents(TrackAGUI)
	require.NoError(t, err)
	require.Len(t, trackEvents.Events, 2)
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
		svc := newHookSessionService()
		svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return nil, errors.New("nope")
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		_, err = tracker.GetEvents(ctx, validKey)
		require.ErrorContains(t, err, "get session: nope")
	})

	t.Run("session not found", func(t *testing.T) {
		svc := newHookSessionService()
		svc.getSessionFn = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return nil, nil
		}
		tracker, err := New(svc)
		require.NoError(t, err)
		_, err = tracker.GetEvents(ctx, validKey)
		require.ErrorContains(t, err, "session not found")
	})

	t.Run("track events error", func(t *testing.T) {
		svc := newHookSessionService()
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
}

func TestTrackerGetEventsSuccess(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := New(svc)
	require.NoError(t, err)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

	first := aguievents.NewTextMessageStartEvent("msg", aguievents.WithRole("user"))
	require.NoError(t, tracker.AppendEvent(ctx, key, first))

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

func TestTrackerFlushPeriodically(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := inmemory.NewSessionService()
	agg := &stubAggregator{flushCh: make(chan struct{}, 2)}
	tracker, err := New(svc,
		WithAggregatorFactory(func(ctx context.Context, opt ...aggregator.Option) aggregator.Aggregator {
			return agg
		}),
		WithFlushInterval(10*time.Millisecond),
	)
	require.NoError(t, err)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	require.NoError(t, tracker.AppendEvent(ctx, key, aguievents.NewTextMessageContentEvent("msg", "hi")))

	select {
	case <-agg.flushCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected periodic flush")
	}
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
	getSessionFn    func(context.Context, session.Key, ...session.Option) (*session.Session, error)
	createSessionFn func(context.Context, session.Key, session.StateMap, ...session.Option) (*session.Session, error)
	appendTrackFn   func(context.Context, *session.Session, *session.TrackEvent, ...session.Option) error
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
