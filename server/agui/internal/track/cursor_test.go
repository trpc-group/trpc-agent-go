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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestGetEventsWithCursorPreservesReaderResult(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	denied := errors.New("read denied")
	for _, mode := range []string{"supported", "legacy reader", "nil result", "denied"} {
		t.Run(mode, func(t *testing.T) {
			svc := newHookSessionService()
			t.Cleanup(func() { require.NoError(t, svc.Close()) })
			calls := 0
			next := "after"
			expected := &session.TrackEvents{Track: TrackAGUI, Events: []session.TrackEvent{{Payload: []byte(`"run"`)}}}
			if mode == "supported" {
				expected.Cursor = &next
			}
			svc.getTrackEventsFn = func(_ context.Context, gotKey session.Key, track session.Track, opts ...session.Option) (*session.TrackEvents, error) {
				calls++
				require.Equal(t, key, gotKey)
				require.Equal(t, TrackAGUI, track)
				options := &session.Options{}
				for _, opt := range opts {
					opt(options)
				}
				require.NotNil(t, options.TrackCursor)
				require.Equal(t, "before", *options.TrackCursor)
				if mode == "denied" {
					return nil, denied
				}
				if mode == "nil result" {
					return nil, nil
				}
				return expected, nil
			}
			reader := &tracker{sessionService: svc}
			got, cursor, err := reader.GetEventsWithCursor(context.Background(), key, "before")
			require.Equal(t, 1, calls, "fallback must not issue a second read")
			switch mode {
			case "supported":
				require.NoError(t, err)
				require.Same(t, expected, got)
				require.Equal(t, next, cursor)
			case "denied":
				require.ErrorIs(t, err, denied)
				require.NotErrorIs(t, err, ErrCursorUnsupported)
			default:
				require.ErrorIs(t, err, ErrCursorUnsupported)
				require.Empty(t, cursor)
				require.Equal(t, TrackAGUI, got.Track)
				if mode == "legacy reader" {
					require.Same(t, expected, got)
				}
			}
		})
	}
}

func TestGetEventsWithCursorLegacySessionRead(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	denied := errors.New("read denied")
	for _, fail := range []bool{false, true} {
		svc := newTrackOnlyHookService()
		t.Cleanup(func() { require.NoError(t, svc.store.Close()) })
		calls := 0
		expected := &session.TrackEvents{Track: TrackAGUI, Events: []session.TrackEvent{{Payload: []byte(`"run"`)}}}
		svc.getSessionFn = func(_ context.Context, _ session.Key, opts ...session.Option) (*session.Session, error) {
			calls++
			require.Empty(t, opts, "legacy readers must not receive cursor options")
			if fail {
				return nil, denied
			}
			s := session.NewSession(key.AppName, key.UserID, key.SessionID)
			s.Tracks = map[session.Track]*session.TrackEvents{TrackAGUI: expected}
			return s, nil
		}
		reader := &tracker{sessionService: svc}
		got, cursor, err := reader.GetEventsWithCursor(context.Background(), key, "before")
		require.Equal(t, 1, calls)
		require.Empty(t, cursor)
		if fail {
			require.ErrorIs(t, err, denied)
			require.NotErrorIs(t, err, ErrCursorUnsupported)
		} else {
			require.ErrorIs(t, err, ErrCursorUnsupported)
			require.Equal(t, expected, got)
		}
	}
}
