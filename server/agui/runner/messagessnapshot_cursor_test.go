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
	"sync/atomic"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestMessagesSnapshotCursorAcrossPolls(t *testing.T) {
	for _, rebuild := range []bool{false, true} {
		t.Run(map[bool]string{false: "append", true: "recreate"}[rebuild], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			svc := newFollowObservingSessionService()
			defer svc.Close()
			key := session.Key{AppName: "demo", UserID: "user", SessionID: "thread"}
			sess, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			stamp := time.Now().Add(-time.Second)
			seed := []session.TrackEvent{newUserMessageTrackEventAt(t, "user", "hi", stamp.Add(-time.Millisecond)),
				newTrackEventAt(t, aguievents.NewRunStartedEvent("thread", "owner"), stamp)}
			for i := range seed {
				require.NoError(t, svc.AppendTrackEvent(ctx, sess, &seed[i]))
			}
			follower := New(noopBaseRunner{}, WithAppName("demo"), WithSessionService(svc), WithFlushInterval(time.Millisecond), WithMessagesSnapshotFollowEnabled(true), WithMessagesSnapshotFollowMaxDuration(2*time.Second))
			stream, err := follower.(MessagesSnapshotter).MessagesSnapshot(ctx, &adapter.RunAgentInput{ThreadID: "thread", RunID: "follower"})
			require.NoError(t, err)
			require.IsType(t, (*aguievents.RunStartedEvent)(nil), nextAGUIEvent(t, stream))
			require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), nextAGUIEvent(t, stream))
			if rebuild {
				require.NoError(t, svc.DeleteSession(ctx, key))
				_, err = svc.CreateSession(ctx, key, nil)
				require.NoError(t, err)
			} else {
				late := newTrackEventAt(t, aguievents.NewCustomEvent("late"), stamp)
				require.NoError(t, svc.AppendTrackEvent(ctx, sess, &late))
				require.NoError(t, svc.AppendTrackEvent(ctx, sess, &late))
				finish := newTrackEventAt(t, aguievents.NewRunFinishedEvent("thread", "owner"), stamp)
				require.NoError(t, svc.AppendTrackEvent(ctx, sess, &finish))
			}
			rest := collectAGUIEvents(t, stream)
			if rebuild {
				require.Len(t, rest, 1)
				failure, ok := rest[0].(*aguievents.RunErrorEvent)
				require.True(t, ok)
				require.Contains(t, failure.Message, session.ErrTrackCursorInvalid.Error())
			} else {
				require.Len(t, rest, 3)
				for _, evt := range rest[:2] {
					custom, ok := evt.(*aguievents.CustomEvent)
					require.True(t, ok)
					require.Equal(t, "late", custom.Name)
				}
				require.IsType(t, (*aguievents.RunFinishedEvent)(nil), rest[2])
			}
			svc.mu.Lock()
			calls := svc.getTrackCalls
			svc.mu.Unlock()
			require.GreaterOrEqual(t, calls, 2, "cursor reads must still go through the wrapper")
		})
	}
}

type cursorPolicyWrapper struct {
	*inmemory.SessionService
	mode  string
	calls atomic.Int32
}

func (s *cursorPolicyWrapper) GetTrackEvents(ctx context.Context, key session.Key, name session.Track, opts ...session.Option) (*session.TrackEvents, error) {
	call := s.calls.Add(1)
	if s.mode == "deny" {
		return nil, errors.New("track cursors are not supported")
	}
	if s.mode == "legacy" || (s.mode == "drop" && call > 1) {
		return s.SessionService.GetTrackEvents(ctx, key, name)
	}
	return s.SessionService.GetTrackEvents(ctx, key, name, opts...)
}

func TestMessagesSnapshotCursorWrapperPolicies(t *testing.T) {
	for _, mode := range []string{"deny", "legacy", "drop"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := &cursorPolicyWrapper{SessionService: inmemory.NewSessionService(), mode: mode}
			defer s.Close()
			key := session.Key{AppName: "demo", UserID: "user", SessionID: "thread"}
			sess, err := s.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			stamp := time.Now()
			user := newUserMessageTrackEventAt(t, "user", "hi", stamp)
			require.NoError(t, s.AppendTrackEvent(ctx, sess, &user))
			if mode == "legacy" {
				finish := newTrackEventAt(t, aguievents.NewRunFinishedEvent("thread", "owner"), stamp.Add(time.Second))
				require.NoError(t, s.AppendTrackEvent(ctx, sess, &finish))
			}
			r := New(noopBaseRunner{}, WithAppName("demo"), WithSessionService(s), WithFlushInterval(time.Millisecond), WithMessagesSnapshotFollowEnabled(true), WithMessagesSnapshotFollowMaxDuration(time.Second))
			stream, err := r.(MessagesSnapshotter).MessagesSnapshot(ctx, &adapter.RunAgentInput{ThreadID: "thread", RunID: "follower"})
			require.NoError(t, err)
			result := collectAGUIEvents(t, stream)
			if mode == "legacy" {
				require.Len(t, result, 3)
				require.IsType(t, (*aguievents.RunFinishedEvent)(nil), result[2])
				require.EqualValues(t, 1, s.calls.Load())
			} else {
				require.IsType(t, (*aguievents.RunErrorEvent)(nil), result[len(result)-1])
				if mode == "deny" {
					require.Len(t, result, 2)
					require.EqualValues(t, 1, s.calls.Load())
				} else {
					require.Len(t, result, 3)
					require.EqualValues(t, 2, s.calls.Load())
				}
			}
		})
	}
}
