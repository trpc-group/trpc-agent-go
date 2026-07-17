//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summarytrigger

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRequestStartContext(t *testing.T) {
	startedAt := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	want := RequestStart{RequestID: "req-2", StartedAt: startedAt}

	ctx := ContextWithRequestStart(nil, want)
	got, ok := RequestStartFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, want, got)

	_, ok = RequestStartFromContext(context.Background())
	assert.False(t, ok)
	_, ok = RequestStartFromContext(nil)
	assert.False(t, ok)
}

func TestObserveRequestGap(t *testing.T) {
	t0 := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	start := RequestStart{RequestID: "req-2", StartedAt: t0.Add(30 * time.Second)}

	t.Run("uses previous event before immutable request start", func(t *testing.T) {
		sess := &session.Session{Events: []event.Event{
			{RequestID: "req-1", Timestamp: t0},
			{RequestID: "req-2", Timestamp: t0.Add(31 * time.Second)},
			{RequestID: "req-3", Timestamp: t0.Add(40 * time.Second)},
		}}

		got := ObserveRequestGap(sess, start, "")
		require.True(t, got.Available)
		assert.Equal(t, start.RequestID, got.RequestID)
		assert.Equal(t, start.StartedAt, got.CurrentRequestStartedAt)
		assert.Equal(t, t0, got.PreviousRequestEndedAt)
		assert.Equal(t, 30*time.Second, got.Elapsed)
	})

	t.Run("keeps previous event with empty request id", func(t *testing.T) {
		sess := &session.Session{Events: []event.Event{{Timestamp: t0}}}
		got := ObserveRequestGap(sess, start, "")
		require.True(t, got.Available)
		assert.Equal(t, 30*time.Second, got.Elapsed)
	})

	t.Run("uses branch descendants but not ancestors", func(t *testing.T) {
		const scope = "app/branch"
		sess := &session.Session{Events: []event.Event{
			{
				RequestID: "req-1",
				Timestamp: t0.Add(20 * time.Second),
				FilterKey: "app",
				Version:   event.CurrentVersion,
			},
			{
				RequestID: "req-1",
				Timestamp: t0.Add(5 * time.Second),
				FilterKey: scope + "/tool",
				Version:   event.CurrentVersion,
			},
		}}

		got := ObserveRequestGap(sess, start, scope)
		require.True(t, got.Available)
		assert.Equal(t, 25*time.Second, got.Elapsed)
		assert.Equal(t, scope, got.FilterKey)
	})

	t.Run("uses legacy branch when event version is old", func(t *testing.T) {
		const scope = "app/branch"
		sess := &session.Session{Events: []event.Event{{
			RequestID: "req-1",
			Timestamp: t0.Add(8 * time.Second),
			Branch:    scope,
		}}}

		got := ObserveRequestGap(sess, start, scope)
		require.True(t, got.Available)
		assert.Equal(t, 22*time.Second, got.Elapsed)
	})

	t.Run("request id reuse is unavailable", func(t *testing.T) {
		sess := &session.Session{Events: []event.Event{
			{RequestID: "req-1", Timestamp: t0},
			{RequestID: "req-2", Timestamp: t0.Add(10 * time.Second)},
		}}

		got := ObserveRequestGap(sess, start, "")
		assert.False(t, got.Available)
		assert.Zero(t, got.Elapsed)
	})

	t.Run("missing request id is unavailable", func(t *testing.T) {
		got := ObserveRequestGap(
			&session.Session{Events: []event.Event{{Timestamp: t0}}},
			RequestStart{StartedAt: start.StartedAt},
			"",
		)
		assert.False(t, got.Available)
	})

	t.Run("missing previous event is unavailable", func(t *testing.T) {
		got := ObserveRequestGap(&session.Session{}, start, "")
		assert.False(t, got.Available)
	})
}

func TestObservationSessionState(t *testing.T) {
	want := RequestGapObservation{
		RequestID:               "req-2",
		FilterKey:               "app",
		CurrentRequestStartedAt: time.Date(2026, 7, 15, 10, 0, 30, 0, time.UTC),
		PreviousRequestEndedAt:  time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
		Elapsed:                 30 * time.Second,
		Available:               true,
	}
	sess := &session.Session{}
	SetObservation(sess, want)

	got, ok := ObservationFromSession(sess)
	require.True(t, ok)
	assert.Equal(t, want, got)

	sess.SetState(requestGapObservationStateKey, []byte("not-json"))
	got, ok = ObservationFromSession(sess)
	require.True(t, ok)
	assert.False(t, got.Available)

	_, ok = ObservationFromSession(&session.Session{})
	assert.False(t, ok)
	_, ok = ObservationFromSession(nil)
	assert.False(t, ok)
}
