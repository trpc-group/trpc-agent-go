//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
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
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestMessagesSnapshotMaxTrackEvents(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "persisted_reader"
		if fallback {
			name = "session_fallback"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				limit   int
				wantIDs []string
			}{
				{"default", 0, []string{"old-user", "old-assistant", "new-user", "new-assistant"}},
				{"exact_boundary", 4, []string{"new-user", "new-assistant"}},
				{"orphan_prefix", 5, []string{"new-user", "new-assistant"}},
				{"no_user_boundary", 3, nil},
				{"one_event", 1, nil},
				{"larger_than_history", 100, []string{"old-user", "old-assistant", "new-user", "new-assistant"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					store := inmemory.NewSessionService()
					t.Cleanup(func() { _ = store.Close() })
					key := session.Key{AppName: "demo", UserID: "user", SessionID: "thread"}
					sess, err := store.CreateSession(context.Background(), key, nil)
					require.NoError(t, err)
					for _, id := range []string{"old", "new"} {
						events := []session.TrackEvent{
							newUserMessageTrackEvent(t, id+"-user", id+" question"),
							newTrackEvent(t, aguievents.NewTextMessageStartEvent(id+"-assistant", aguievents.WithRole("assistant"))),
							newTrackEvent(t, aguievents.NewTextMessageContentEvent(id+"-assistant", id+" answer")),
							newTrackEvent(t, aguievents.NewTextMessageEndEvent(id+"-assistant")),
						}
						for i := range events {
							require.NoError(t, store.AppendTrackEvent(context.Background(), sess, &events[i]))
						}
					}
					var calls []session.Options
					reader := &snapshotLimitReader{SessionService: store}
					reader.read = func(ctx context.Context, gotKey session.Key, gotTrack session.Track, opts ...session.Option) (*session.TrackEvents, error) {
						var got session.Options
						for _, opt := range opts {
							opt(&got)
						}
						calls = append(calls, got)
						return store.GetTrackEvents(ctx, gotKey, gotTrack, opts...)
					}
					var svc session.Service = reader
					if fallback {
						svc = &snapshotLimitFallback{Service: store, TrackService: store, reader: reader}
					}
					r := New(noopBaseRunner{}, WithAppName(key.AppName), WithSessionService(svc),
						WithUserIDResolver(func(context.Context, *adapter.RunAgentInput) (string, error) { return key.UserID, nil }),
						WithMessagesSnapshotMaxTrackEvents(tc.limit))
					stream, err := r.(MessagesSnapshotter).MessagesSnapshot(context.Background(),
						&adapter.RunAgentInput{ThreadID: key.SessionID, RunID: "history"})
					require.NoError(t, err)
					out := collectAGUIEvents(t, stream)
					require.Len(t, out, 3)
					snapshot, ok := out[1].(*aguievents.MessagesSnapshotEvent)
					require.True(t, ok)
					var ids []string
					for _, msg := range snapshot.Messages {
						ids = append(ids, msg.ID)
					}
					require.Equal(t, tc.wantIDs, ids)
					require.IsType(t, &aguievents.RunFinishedEvent{}, out[2])
					require.Len(t, calls, 1, "must not retry an unbounded read")
					require.Equal(t, tc.limit, calls[0].EventNum)
					require.True(t, calls[0].EventTime.IsZero())
				})
			}
		})
	}
}

func TestMessagesSnapshotMaxTrackEventsReadError(t *testing.T) {
	store := inmemory.NewSessionService()
	t.Cleanup(func() { _ = store.Close() })
	calls := 0
	reader := &snapshotLimitReader{SessionService: store, read: func(context.Context, session.Key, session.Track, ...session.Option) (*session.TrackEvents, error) {
		calls++
		return nil, errors.New("storage unavailable")
	}}
	r := New(noopBaseRunner{}, WithAppName("demo"), WithSessionService(reader), WithMessagesSnapshotMaxTrackEvents(4))
	stream, err := r.(MessagesSnapshotter).MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "history"})
	require.NoError(t, err)
	out := collectAGUIEvents(t, stream)
	require.Len(t, out, 2)
	runErr, ok := out[1].(*aguievents.RunErrorEvent)
	require.True(t, ok)
	require.Contains(t, runErr.Message, "storage unavailable")
	require.Equal(t, 1, calls)
}

func TestMessagesSnapshotMaxTrackEventsNegative(t *testing.T) {
	store := inmemory.NewSessionService()
	t.Cleanup(func() { _ = store.Close() })
	r := New(noopBaseRunner{}, WithSessionService(store), WithMessagesSnapshotMaxTrackEvents(-1))
	stream, err := r.(MessagesSnapshotter).MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread"})
	require.Nil(t, stream)
	require.EqualError(t, err, "messages snapshot max track events must be non-negative")
}

func TestMessagesSnapshotMaxTrackEventsFollow(t *testing.T) {
	for _, safe := range []bool{true, false} {
		name := "safe"
		if !safe {
			name = "no_user_boundary"
		}
		t.Run(name, func(t *testing.T) {
			base := time.Now().Add(-time.Second)
			initial := newUserMessageTrackEventAt(t, "user", "hello", base)
			if !safe {
				initial = newTrackEventAt(t, aguievents.NewTextMessageContentEvent("orphan", "tail"), base)
			}
			followEvents := []session.TrackEvent{
				newTrackEventAt(t, aguievents.NewTextMessageStartEvent("reply", aguievents.WithRole("assistant")), base.Add(time.Millisecond)),
				newTrackEventAt(t, aguievents.NewTextMessageContentEvent("reply", "hello"), base.Add(2*time.Millisecond)),
				newTrackEventAt(t, aguievents.NewTextMessageEndEvent("reply"), base.Add(3*time.Millisecond)),
				newTrackEventAt(t, aguievents.NewRunFinishedEvent("thread", "run"), base.Add(4*time.Millisecond)),
			}
			store := inmemory.NewSessionService()
			t.Cleanup(func() { _ = store.Close() })
			var calls []session.Options
			reader := &snapshotLimitReader{SessionService: store, read: func(_ context.Context, _ session.Key, tr session.Track, opts ...session.Option) (*session.TrackEvents, error) {
				var got session.Options
				for _, opt := range opts {
					opt(&got)
				}
				calls = append(calls, got)
				if len(calls) == 1 {
					return &session.TrackEvents{Track: tr, Events: []session.TrackEvent{initial}}, nil
				}
				return &session.TrackEvents{Track: tr, Events: followEvents}, nil
			}}
			r := New(noopBaseRunner{}, WithAppName("demo"), WithSessionService(reader),
				WithMessagesSnapshotMaxTrackEvents(1), WithMessagesSnapshotFollowEnabled(true),
				WithFlushInterval(time.Millisecond), WithMessagesSnapshotFollowMaxDuration(time.Second))
			stream, err := r.(MessagesSnapshotter).MessagesSnapshot(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "history"})
			require.NoError(t, err)
			out := collectAGUIEvents(t, stream)
			require.Equal(t, 1, calls[0].EventNum)
			if safe {
				require.Len(t, calls, 2)
				require.Zero(t, calls[1].EventNum, "follow must not discard a backlog larger than the initial cap")
				require.True(t, base.Equal(calls[1].EventTime))
				require.Len(t, out, 6)
				require.IsType(t, &aguievents.TextMessageStartEvent{}, out[2])
				require.IsType(t, &aguievents.TextMessageContentEvent{}, out[3])
				require.IsType(t, &aguievents.TextMessageEndEvent{}, out[4])
			} else {
				require.Len(t, calls, 1)
				require.Len(t, out, 3)
				require.Empty(t, out[1].(*aguievents.MessagesSnapshotEvent).Messages)
			}
			require.IsType(t, &aguievents.RunFinishedEvent{}, out[len(out)-1])
		})
	}
}

type snapshotLimitReader struct {
	*inmemory.SessionService
	read func(context.Context, session.Key, session.Track, ...session.Option) (*session.TrackEvents, error)
}

func (s *snapshotLimitReader) GetTrackEvents(ctx context.Context, key session.Key, tr session.Track, opts ...session.Option) (*session.TrackEvents, error) {
	return s.read(ctx, key, tr, opts...)
}

type snapshotLimitFallback struct {
	session.Service
	session.TrackService
	reader *snapshotLimitReader
}

func (s *snapshotLimitFallback) GetSession(ctx context.Context, key session.Key, opts ...session.Option) (*session.Session, error) {
	events, err := s.reader.GetTrackEvents(ctx, key, track.TrackAGUI, opts...)
	if err != nil {
		return nil, err
	}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sess.Tracks = map[session.Track]*session.TrackEvents{track.TrackAGUI: events}
	return sess, nil
}
