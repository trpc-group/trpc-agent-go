//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestIngestSessionCapturesTimestampedMessagesAndCursor(t *testing.T) {
	var got captureRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: len(got.Messages)})
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestQueueSize(1),
		WithIngestJobTimeout(time.Second),
	)
	require.NoError(t, err, "NewService")

	ts1 := time.Date(2026, 5, 22, 8, 0, 0, 123*1e6, time.UTC)
	ts2 := ts1.Add(time.Second)
	sess := &session.Session{
		ID:      "sess-1",
		AppName: "app",
		UserID:  "user",
		State:   session.StateMap{},
		Events: []event.Event{
			{
				ID:        "u1",
				Timestamp: ts1,
				Response: &model.Response{Choices: []model.Choice{{
					Index:   0,
					Message: model.NewUserMessage("remember this"),
				}}},
			},
			{
				ID:        "a1",
				Timestamp: ts2,
				Response: &model.Response{Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage("stored"),
				}}},
			},
		},
	}
	require.NoError(t, svc.IngestSession(context.Background(), sess), "IngestSession")
	require.NoError(t, svc.Close(), "Close")

	assert.Equal(t, "app:user:sess-1", got.SessionKey)
	assert.Equal(t, "remember this", got.UserContent)
	assert.Equal(t, "stored", got.AssistantContent)
	require.Len(t, got.Messages, 2)
	assert.Greater(t, got.Messages[0].Timestamp, ts2.UnixMilli())
	assert.Equal(t, got.Messages[0].Timestamp+1, got.Messages[1].Timestamp)
	assert.Equal(t, ts2, readBestEffortLastCaptureAt(sess))
}

func TestIngestSessionMarksInFlightBeforeAsyncCaptureCompletes(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: 2})
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestQueueSize(1),
		WithIngestJobTimeout(time.Second),
	)
	require.NoError(t, err, "NewService")
	defer func() {
		close(release)
		assert.NoError(t, svc.Close())
	}()
	sess := captureReadySession()
	require.NoError(t, svc.IngestSession(context.Background(), sess), "IngestSession")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("capture request did not start")
	}
	assert.True(t, readBestEffortLastCaptureAt(sess).IsZero())
	require.NoError(t, svc.IngestSession(context.Background(), sess), "second IngestSession")
	assert.Equal(t, int32(1), requests.Load())
}

func TestIngestSessionRetriesAfterAsyncCaptureFailure(t *testing.T) {
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		switch requests.Add(1) {
		case 1:
			http.Error(w, "gateway unavailable", http.StatusBadGateway)
			close(firstDone)
		case 2:
			_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: 2})
			close(secondDone)
		default:
			t.Fatalf("unexpected extra capture request")
		}
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestQueueSize(1),
		WithIngestJobTimeout(time.Second),
	)
	require.NoError(t, err, "NewService")
	defer func() {
		assert.NoError(t, svc.Close())
	}()
	sess := captureReadySession()
	want := sess.Events[len(sess.Events)-1].Timestamp
	require.NoError(t, svc.IngestSession(context.Background(), sess), "IngestSession")
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatalf("first capture request did not complete")
	}
	waitForCondition(t, time.Second, func() bool {
		svc.cursorMu.Lock()
		defer svc.cursorMu.Unlock()
		_, ok := svc.inFlight[svc.sessionKey(sess)]
		return !ok
	})
	assert.True(t, readBestEffortLastCaptureAt(sess).IsZero())
	require.NoError(t, svc.IngestSession(context.Background(), sess), "retry IngestSession")
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatalf("retry capture request did not complete")
	}
	require.NoError(t, svc.Close(), "Close")
	assert.True(t, readBestEffortLastCaptureAt(sess).Equal(want))
}

func TestIngestSessionSerializesSameSessionCapturesAndTimestamps(t *testing.T) {
	releaseFirst := make(chan struct{})
	firstReqC := make(chan captureRequest, 1)
	secondReqC := make(chan captureRequest, 1)
	var released atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req captureRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch requests.Add(1) {
		case 1:
			firstReqC <- req
			<-releaseFirst
		case 2:
			secondReqC <- req
		default:
			t.Fatalf("unexpected extra capture request")
		}
		_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: len(req.Messages)})
	}))
	defer server.Close()
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(releaseFirst)
		}
	}()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestWorkers(2),
		WithIngestQueueSize(2),
		WithIngestJobTimeout(time.Second),
	)
	require.NoError(t, err, "NewService")
	defer func() {
		assert.NoError(t, svc.Close())
	}()
	sess := captureReadySession()
	require.NoError(t, svc.IngestSession(context.Background(), sess), "first IngestSession")
	firstReq := waitCaptureRequest(t, firstReqC, "first capture")

	events := sess.GetEvents()
	nextAt := events[len(events)-1].Timestamp.Add(time.Second)
	appendSessionPair(sess, nextAt, "u2", "second fact", "a2", "stored second")
	require.NoError(t, svc.IngestSession(context.Background(), sess), "second IngestSession")
	select {
	case req := <-secondReqC:
		t.Fatalf("second capture started before first completed: %#v", req)
	case <-time.After(50 * time.Millisecond):
	}
	if released.CompareAndSwap(false, true) {
		close(releaseFirst)
	}
	secondReq := waitCaptureRequest(t, secondReqC, "second capture")
	require.NotEmpty(t, firstReq.Messages)
	require.NotEmpty(t, secondReq.Messages)
	firstLast := firstReq.Messages[len(firstReq.Messages)-1].Timestamp
	assert.Greater(t, secondReq.Messages[0].Timestamp, firstLast)
	assert.Equal(t, "second fact", secondReq.UserContent)
	assert.Equal(t, "stored second", secondReq.AssistantContent)
}

func TestEndSessionWaitsForPendingCapture(t *testing.T) {
	releaseCapture := make(chan struct{})
	captureStarted := make(chan struct{}, 1)
	endCalled := make(chan struct{}, 1)
	var released atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathCapture:
			requests.Add(1)
			select {
			case captureStarted <- struct{}{}:
			default:
			}
			<-releaseCapture
			_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: 2})
		case pathEndSession:
			if got := requests.Load(); got != 1 {
				t.Fatalf("end called before capture request: captures=%d", got)
			}
			select {
			case endCalled <- struct{}{}:
			default:
			}
			_ = json.NewEncoder(w).Encode(endSessionResponse{Flushed: true})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestWorkers(2),
		WithIngestQueueSize(2),
		WithIngestJobTimeout(time.Second),
	)
	require.NoError(t, err, "NewService")
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(releaseCapture)
		}
		assert.NoError(t, svc.Close())
	}()

	sess := captureReadySession()
	require.NoError(t, svc.IngestSession(context.Background(), sess), "IngestSession")
	select {
	case <-captureStarted:
	case <-time.After(time.Second):
		t.Fatalf("capture request did not start")
	}
	endDone := make(chan error, 1)
	go func() {
		endDone <- svc.EndSession(context.Background(), sess)
	}()
	select {
	case <-endCalled:
		t.Fatalf("end session reached gateway before capture completed")
	case <-time.After(50 * time.Millisecond):
	}
	if released.CompareAndSwap(false, true) {
		close(releaseCapture)
	}
	select {
	case err := <-endDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatalf("EndSession did not complete")
	}
	select {
	case <-endCalled:
	default:
		t.Fatalf("end session gateway call was not observed")
	}
}

func TestServiceCheckpointSkipsReloadedSessionEvents(t *testing.T) {
	requests := make(chan captureRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req captureRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests <- req
		_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: len(req.Messages)})
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestQueueSize(2),
		WithIngestJobTimeout(time.Second),
	)
	require.NoError(t, err, "NewService")
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	sess := captureReadySession()
	require.NoError(t, svc.IngestSession(context.Background(), sess), "first IngestSession")
	_ = waitCaptureRequest(t, requests, "first capture")
	reloaded := &session.Session{
		ID:      sess.ID,
		AppName: sess.AppName,
		UserID:  sess.UserID,
		Events:  sess.GetEvents(),
		State:   session.StateMap{},
	}
	require.NoError(t, svc.IngestSession(context.Background(), reloaded), "reloaded IngestSession")
	select {
	case req := <-requests:
		t.Fatalf("reloaded session resent captured events: %#v", req)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEndSessionRetainsCaptureCheckpoint(t *testing.T) {
	requests := make(chan captureRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathCapture:
			var req captureRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requests <- req
			_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: len(req.Messages)})
		case pathEndSession:
			_ = json.NewEncoder(w).Encode(endSessionResponse{Flushed: true})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestQueueSize(2),
		WithIngestJobTimeout(time.Second),
	)
	require.NoError(t, err, "NewService")
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	sess := captureReadySession()
	require.NoError(t, svc.IngestSession(context.Background(), sess), "IngestSession")
	_ = waitCaptureRequest(t, requests, "initial capture")

	sessionKey := svc.sessionKey(sess)
	waitForCondition(t, time.Second, func() bool {
		svc.cursorMu.Lock()
		defer svc.cursorMu.Unlock()
		_, ok := svc.lastCapture[sessionKey]
		return ok
	})

	require.NoError(t, svc.EndSession(context.Background(), sess), "EndSession")

	svc.cursorMu.Lock()
	_, retained := svc.lastCapture[sessionKey]
	svc.cursorMu.Unlock()
	assert.True(t, retained, "capture checkpoint should be retained after EndSession")

	// A session that keeps running (here modeled as a reload without the
	// in-state cursor) must not resend already captured transcript, because the
	// service-level checkpoint survives EndSession.
	reloaded := &session.Session{
		ID:      sess.ID,
		AppName: sess.AppName,
		UserID:  sess.UserID,
		Events:  sess.GetEvents(),
		State:   session.StateMap{},
	}
	require.NoError(t, svc.IngestSession(context.Background(), reloaded), "reloaded IngestSession")
	select {
	case req := <-requests:
		t.Fatalf("session resent captured events after EndSession: %#v", req)
	case <-time.After(50 * time.Millisecond):
	}
}
