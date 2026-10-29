//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package trackcursor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestReadPolicyPreservesContinuation(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "alias"}
	stored := session.NewSession("app", "user", "real")
	stored.Tracks = map[session.Track]*session.TrackEvents{
		"track": {Track: "track", Events: []session.TrackEvent{{Payload: json.RawMessage(`"private"`)}}},
	}
	calls, reads := 0, 0
	h := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		calls++
		c.Key.SessionID = "real"
		s, err := next()
		if err == nil {
			s.Tracks["track"].Events[0].Payload = json.RawMessage(`"redacted"`)
		}
		return s, err
	}
	events, cursor, err := Read(context.Background(), key, "track", "before", []session.GetSessionHook{h},
		func(c *session.GetSessionContext) (*session.Session, string, error) {
			reads++
			require.Equal(t, "real", c.Key.SessionID)
			require.Equal(t, "before", *c.Options.TrackCursor)
			return stored, "after", nil
		})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, reads)
	require.Equal(t, "after", cursor)
	require.Equal(t, json.RawMessage(`"redacted"`), events.Events[0].Payload)
}

func TestReadRejectsUnpositionedPolicies(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	denied := errors.New("denied")
	cases := []struct {
		name  string
		hook  session.GetSessionHook
		want  error
		reads int
	}{
		{"deny", func(_ *session.GetSessionContext, _ func() (*session.Session, error)) (*session.Session, error) {
			return nil, denied
		}, denied, 0},
		{"synthetic snapshot", func(_ *session.GetSessionContext, _ func() (*session.Session, error)) (*session.Session, error) {
			return session.NewSession("app", "user", "session"), nil
		}, session.ErrTrackCursorInvalid, 0},
		{"swallowed second read", func(_ *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
			first, err := next()
			_, _ = next()
			return first, err
		}, session.ErrTrackCursorInvalid, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			_, cursor, err := Read(context.Background(), key, "track", "before", []session.GetSessionHook{tc.hook},
				func(*session.GetSessionContext) (*session.Session, string, error) {
					reads++
					return session.NewSession("app", "user", "session"), "after", nil
				})
			require.ErrorIs(t, err, tc.want)
			require.Empty(t, cursor, "a failed policy must not advance the caller")
			require.Equal(t, tc.reads, reads)
		})
	}
}

func TestReadValidatesPolicyChangesBeforeStorage(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	for name, change := range map[string]func(*session.GetSessionContext){
		"missing options":       func(c *session.GetSessionContext) { c.Options = nil },
		"missing cursor":        func(c *session.GetSessionContext) { c.Options = &session.Options{} },
		"changed cursor":        func(c *session.GetSessionContext) { *c.Options.TrackCursor = "other" },
		"missing context":       func(c *session.GetSessionContext) { c.Context = nil },
		"invalid rewritten key": func(c *session.GetSessionContext) { c.Key = session.Key{} },
		"canceled context": func(c *session.GetSessionContext) {
			ctx, cancel := context.WithCancel(c.Context)
			cancel()
			c.Context = ctx
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				change(c)
				return next()
			}
			_, cursor, err := Read(context.Background(), key, "track", "before", []session.GetSessionHook{h},
				func(*session.GetSessionContext) (*session.Session, string, error) {
					t.Fatal("invalid policy reached storage")
					return nil, "", nil
				})
			require.Error(t, err)
			require.Empty(t, cursor)
		})
	}
}

func TestReadEmptyResultsAndErrors(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	loadError := errors.New("storage unavailable")
	for _, mode := range []string{"no session", "no tracks", "other track", "storage error", "policy hides session"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			var hooks []session.GetSessionHook
			if mode == "policy hides session" {
				hooks = []session.GetSessionHook{func(*session.GetSessionContext, func() (*session.Session, error)) (*session.Session, error) {
					return nil, nil
				}}
			}
			events, cursor, err := Read(context.Background(), key, "track", "before", hooks,
				func(*session.GetSessionContext) (*session.Session, string, error) {
					reads++
					if mode == "storage error" {
						return nil, "after", loadError
					}
					if mode == "no session" {
						return nil, "after", nil
					}
					s := session.NewSession("app", "user", "session")
					if mode == "other track" {
						s.Tracks = map[session.Track]*session.TrackEvents{"other": {Track: "other"}}
					}
					return s, "after", nil
				})
			if mode == "storage error" {
				require.ErrorIs(t, err, loadError)
				require.Empty(t, cursor)
				return
			}
			require.NoError(t, err)
			require.Equal(t, session.Track("track"), events.Track)
			require.Empty(t, events.Events)
			if mode == "policy hides session" {
				require.Zero(t, reads)
				require.Equal(t, "before", cursor)
			} else {
				require.Equal(t, 1, reads)
				require.Equal(t, "after", cursor)
			}
		})
	}
	load := func(*session.GetSessionContext) (*session.Session, string, error) {
		t.Fatal("invalid request reached storage")
		return nil, "", nil
	}
	_, _, err := Read(context.Background(), session.Key{}, "track", "", nil, load)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = Read(ctx, key, "track", "", nil, load)
	require.ErrorIs(t, err, context.Canceled)
}
