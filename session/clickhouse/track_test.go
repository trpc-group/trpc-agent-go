//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestAppendTrackEventSerializesEventIndex(t *testing.T) {
	const appendCount = 24

	var mu sync.Mutex
	var persistedCount uint64
	var indexes []uint64
	state := SessionState{
		ID:        "session",
		State:     make(session.StateMap),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stateJSON, err := json.Marshal(state)
	require.NoError(t, err)

	client := &mockClient{}
	service := &Service{
		chClient:                client,
		tableSessionStates:      "session_states",
		tableSessionTrackEvents: "session_track_events",
	}
	client.queryFunc = func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(query, "SELECT count()") {
			return newMockRows([][]any{{persistedCount}}), nil
		}
		return newMockRows([][]any{{string(stateJSON)}}), nil
	}
	client.execFunc = func(_ context.Context, query string, args ...any) error {
		if !strings.Contains(query, "INSERT INTO "+service.tableSessionTrackEvents) {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		index := args[4].(uint64)
		indexes = append(indexes, index)
		persistedCount++
		return nil
	}

	sess := session.NewSession("app", "user", "session")
	errs := make(chan error, appendCount)
	var wg sync.WaitGroup
	for i := 0; i < appendCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := json.RawMessage(fmt.Sprintf(`{"index":%d}`, i))
			errs <- service.AppendTrackEvent(context.Background(), sess, &session.TrackEvent{
				Track:   "model",
				Payload: payload,
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	slices.Sort(indexes)
	require.Len(t, indexes, appendCount)
	for i, index := range indexes {
		require.Equal(t, uint64(i), index)
	}
	require.Empty(t, service.trackLocks)
}

func TestGetTrackEventsList(t *testing.T) {
	now := time.Now()
	eventOne, err := json.Marshal(session.TrackEvent{
		Track:   "ignored",
		Payload: json.RawMessage(`{"step":1}`),
	})
	require.NoError(t, err)
	eventTwo, err := json.Marshal(session.TrackEvent{
		Track:   "ignored",
		Payload: json.RawMessage(`{"step":2}`),
	})
	require.NoError(t, err)

	client := &mockClient{
		queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return newMockRows([][]any{
				{"app", "user", "one", "model", string(eventOne)},
				{"app", "user", "two", "tool", string(eventTwo)},
			}), nil
		},
	}
	service := &Service{
		chClient:                client,
		tableSessionTrackEvents: "session_track_events",
	}
	tracks, err := service.getTrackEventsList(
		context.Background(),
		[]session.Key{
			{AppName: "app", UserID: "user", SessionID: "one"},
			{AppName: "app", UserID: "user", SessionID: "two"},
		},
		[]time.Time{now, now},
	)
	require.NoError(t, err)
	require.Len(t, tracks, 2)
	require.Equal(t, session.Track("model"), tracks[0]["model"].Events[0].Track)
	require.JSONEq(t, `{"step":1}`, string(tracks[0]["model"].Events[0].Payload))
	require.Equal(t, session.Track("tool"), tracks[1]["tool"].Events[0].Track)
	require.JSONEq(t, `{"step":2}`, string(tracks[1]["tool"].Events[0].Payload))
}

func TestGetTrackEventsReturnsRowsError(t *testing.T) {
	client := &mockClient{
		queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return &mockRows{current: -1, err: assert.AnError}, nil
		},
	}
	service := &Service{
		chClient:                client,
		tableSessionTrackEvents: "session_track_events",
	}
	_, err := service.getTrackEvents(
		context.Background(),
		session.Key{AppName: "app", UserID: "user", SessionID: "session"},
		time.Now(),
	)
	require.ErrorIs(t, err, assert.AnError)
}
