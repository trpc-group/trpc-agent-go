//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrackCursorAppendAndReplay(t *testing.T) {
	ctx := context.Background()
	s := NewSessionService()
	defer s.Close()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "cursor"}
	sess, err := s.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	first, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.NotNil(t, first.Cursor)
	timestamp := time.Now()
	for i := 0; i < 3; i++ {
		event := &session.TrackEvent{Track: "track", Payload: json.RawMessage(`{"same":true}`), Timestamp: timestamp}
		if i == 2 {
			event.Timestamp = timestamp.Add(-time.Hour)
		}
		require.NoError(t, s.AppendTrackEvent(ctx, sess, event))
	}
	read, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*first.Cursor))
	require.NoError(t, err)
	require.Len(t, read.Events, 3)
	require.True(t, read.Events[2].Timestamp.Before(read.Events[0].Timestamp))
	read.Events[0].Payload[0] = 'x'
	replayed, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*first.Cursor))
	require.NoError(t, err)
	require.True(t, json.Valid(replayed.Events[0].Payload))
	require.Equal(t, *read.Cursor, *replayed.Cursor)
	empty, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*read.Cursor))
	require.NoError(t, err)
	require.Empty(t, empty.Events)
	encoded, err := json.Marshal(replayed)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), *read.Cursor)
	legacy, err := s.GetTrackEvents(ctx, key, "track")
	require.NoError(t, err)
	require.Nil(t, legacy.Cursor)
}

func TestTrackCursorBindingAndLifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewSessionService()
	defer s.Close()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "cursor"}
	missing, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	sess, err := s.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, s.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Timestamp: time.Now()}))
	read, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*missing.Cursor))
	require.NoError(t, err)
	for _, other := range []session.Key{{AppName: "other", UserID: "user", SessionID: "cursor"},
		{AppName: "app", UserID: "other", SessionID: "cursor"}, {AppName: "app", UserID: "user", SessionID: "other"}} {
		_, err := s.GetTrackEvents(ctx, other, "track", session.WithTrackCursor(*read.Cursor))
		require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	}
	_, err = s.GetTrackEvents(ctx, key, "other", session.WithTrackCursor(*read.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	_, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor("e30"))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	_, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*read.Cursor), session.WithEventNum(1))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	require.NoError(t, s.DeleteSession(ctx, key))
	_, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*read.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	_, err = s.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	_, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*read.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	fresh, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	app, _ := s.getAppSessions(key.AppName)
	app.mu.Lock()
	app.sessions[key.UserID][key.SessionID].expiredAt = time.Now().Add(-time.Second)
	app.mu.Unlock()
	_, err = s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(*fresh.Cursor))
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
}

func TestTrackCursorReadHooks(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "real"}
	alias := key
	alias.SessionID = "alias"
	var calls int
	s := NewSessionService(WithGetSessionHook(func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		calls++
		require.NotNil(t, c.Options.TrackCursor)
		c.Key = key
		snapshot, err := next()
		if snapshot != nil && snapshot.Tracks["track"] != nil {
			for i := range snapshot.Tracks["track"].Events {
				snapshot.Tracks["track"].Events[i].Payload = json.RawMessage(`"redacted"`)
			}
		}
		return snapshot, err
	}))
	defer s.Close()
	sess, err := s.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, s.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "track", Payload: json.RawMessage(`"secret"`)}))
	read, err := s.GetTrackEvents(ctx, alias, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, json.RawMessage(`"redacted"`), read.Events[0].Payload)
	read, err = s.GetTrackEvents(ctx, alias, "track", session.WithTrackCursor(*read.Cursor))
	require.NoError(t, err)
	require.Empty(t, read.Events)
	require.Equal(t, 2, calls)
	denied := errors.New("denied")
	for _, mode := range []string{"deny", "nil", "synthetic", "twice"} {
		t.Run(mode, func(t *testing.T) {
			service := NewSessionService(WithGetSessionHook(func(_ *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				switch mode {
				case "deny":
					return nil, denied
				case "nil":
					return nil, nil
				case "synthetic":
					return session.NewSession("app", "user", "real"), nil
				default:
					_, _ = next()
					return next()
				}
			}))
			defer service.Close()
			result, err := service.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
			switch mode {
			case "deny":
				require.ErrorIs(t, err, denied)
			case "nil":
				require.NoError(t, err)
				require.Empty(t, result.Events)
			default:
				require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
			}
		})
	}
}

func TestTrackCursorConcurrentAppend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := NewSessionService()
	defer s.Close()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "concurrent"}
	sess, err := s.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	start, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(""))
	require.NoError(t, err)
	var writers sync.WaitGroup
	errors := make(chan error, 2)
	timestamp := time.Now()
	for i := 0; i < 2; i++ {
		copy := sess.Clone()
		writers.Add(1)
		go func() {
			defer writers.Done()
			for j := 0; j < 50; j++ {
				if err := s.AppendTrackEvent(ctx, copy, &session.TrackEvent{Track: "track", Timestamp: timestamp, Payload: json.RawMessage(`"same"`)}); err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	cursor, count := *start.Cursor, 0
	for count < 100 {
		require.NoError(t, ctx.Err())
		next, err := s.GetTrackEvents(ctx, key, "track", session.WithTrackCursor(cursor))
		require.NoError(t, err)
		count += len(next.Events)
		cursor = *next.Cursor
		runtime.Gosched()
	}
	writers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, 100, count)
}
