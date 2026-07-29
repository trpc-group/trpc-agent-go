//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
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

func TestAppendTrackEventSerializesEventIndexWithinService(t *testing.T) {
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

func TestAppendTrackEventRetainsConcurrentCrossServiceWrites(t *testing.T) {
	state := SessionState{
		ID:        "session",
		State:     make(session.StateMap),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stateJSON, err := json.Marshal(state)
	require.NoError(t, err)

	var mu sync.Mutex
	countCalls := 0
	countBarrier := make(chan struct{})
	var indexes []uint64
	var eventIDs []string
	var payloads []string
	client := &mockClient{}
	client.queryFunc = func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
		if !strings.Contains(query, "SELECT count()") {
			return newMockRows([][]any{{string(stateJSON)}}), nil
		}

		mu.Lock()
		countCalls++
		if countCalls == 2 {
			close(countBarrier)
		}
		mu.Unlock()
		<-countBarrier
		return newMockRows([][]any{{uint64(0)}}), nil
	}
	client.execFunc = func(_ context.Context, query string, args ...any) error {
		if !strings.Contains(query, "INSERT INTO session_track_events") ||
			!strings.Contains(query, "VALUES") {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		indexes = append(indexes, args[4].(uint64))
		eventIDs = append(eventIDs, args[5].(string))
		payloads = append(payloads, args[6].(string))
		return nil
	}

	newService := func() *Service {
		return &Service{
			chClient:                client,
			tableSessionStates:      "session_states",
			tableSessionTrackEvents: "session_track_events",
		}
	}
	services := []*Service{newService(), newService()}
	sessions := []*session.Session{
		session.NewSession("app", "user", "session"),
		session.NewSession("app", "user", "session"),
	}

	errs := make(chan error, len(services))
	for i := range services {
		go func(i int) {
			errs <- services[i].AppendTrackEvent(
				context.Background(),
				sessions[i],
				&session.TrackEvent{
					Track:   "model",
					Payload: json.RawMessage(fmt.Sprintf(`{"service":%d}`, i)),
				},
			)
		}(i)
	}
	for range services {
		require.NoError(t, <-errs)
	}

	require.Equal(t, []uint64{0, 0}, indexes)
	require.Len(t, eventIDs, 2)
	require.NotEqual(t, eventIDs[0], eventIDs[1])
	slices.Sort(payloads)
	require.Contains(t, payloads[0], `"service":0`)
	require.Contains(t, payloads[1], `"service":1`)
}

func TestLockTrackAppendHonorsContextCancellation(t *testing.T) {
	service := &Service{}
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	unlock, err := service.lockTrackAppend(context.Background(), key)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.lockTrackAppend(ctx, key)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, service.trackLocks, 1)

	unlock()
	require.Empty(t, service.trackLocks)
}

func TestAppendTrackEventFailureDoesNotMutateSession(t *testing.T) {
	tests := []struct {
		name             string
		event            *session.TrackEvent
		failQuery        bool
		failInsert       bool
		failStatePersist bool
		wantCompensation bool
	}{
		{
			name: "marshal",
			event: &session.TrackEvent{
				Track:   "model",
				Payload: json.RawMessage(`{`),
			},
		},
		{
			name:      "count query",
			event:     &session.TrackEvent{Track: "model"},
			failQuery: true,
		},
		{
			name:       "event insert",
			event:      &session.TrackEvent{Track: "model"},
			failInsert: true,
		},
		{
			name:             "state insert",
			event:            &session.TrackEvent{Track: "model"},
			failStatePersist: true,
			wantCompensation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := SessionState{
				ID:        "session",
				State:     make(session.StateMap),
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			stateJSON, err := json.Marshal(state)
			require.NoError(t, err)

			var compensation bool
			var insertedEventID string
			var compensatedEventID string
			client := &mockClient{}
			client.queryFunc = func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
				if strings.Contains(query, "SELECT count()") {
					if tt.failQuery {
						return nil, assert.AnError
					}
					return newMockRows([][]any{{uint64(0)}}), nil
				}
				return newMockRows([][]any{{string(stateJSON)}}), nil
			}
			client.execFunc = func(_ context.Context, query string, args ...any) error {
				switch {
				case strings.Contains(query, "INSERT INTO session_track_events") &&
					strings.Contains(query, "VALUES"):
					if tt.failInsert {
						return assert.AnError
					}
					insertedEventID = args[5].(string)
				case strings.Contains(query, "INSERT INTO session_states"):
					if tt.failStatePersist {
						return assert.AnError
					}
				case strings.Contains(query, "INSERT INTO session_track_events") &&
					strings.Contains(query, "SELECT"):
					compensation = true
					compensatedEventID = args[6].(string)
				}
				return nil
			}
			service := &Service{
				chClient:                client,
				tableSessionStates:      "session_states",
				tableSessionTrackEvents: "session_track_events",
			}
			sess := session.NewSession("app", "user", "session")

			err = service.AppendTrackEvent(context.Background(), sess, tt.event)
			require.Error(t, err)
			_, trackErr := sess.GetTrackEvents("model")
			require.ErrorIs(t, trackErr, session.ErrTracksEmpty)
			require.Nil(t, sess.SnapshotTracksState())
			require.Equal(t, tt.wantCompensation, compensation)
			if tt.wantCompensation {
				require.NotEmpty(t, insertedEventID)
				require.Equal(t, insertedEventID, compensatedEventID)
				require.ErrorIs(t, err, assert.AnError)
			}
		})
	}
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
