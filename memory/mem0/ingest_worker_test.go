//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func newWorkerWithServer(t *testing.T, handler http.Handler, opts serviceOpts) (*ingestWorker, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	if opts.host == "" {
		opts.host = srv.URL
	}
	if opts.apiKey == "" {
		opts.apiKey = "k"
	}
	c, err := newClient(opts)
	if err != nil {
		srv.Close()
		t.Fatalf("newClient: %v", err)
	}
	w := newIngestWorker(c, opts)
	t.Cleanup(func() {
		w.Stop()
		srv.Close()
	})
	return w, srv
}

// httpServerURL spins up an httptest server and returns its URL. Cleanup is
// registered on t.
func httpServerURL(t *testing.T, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestHashUserKey_Deterministic(t *testing.T) {
	k := memory.UserKey{AppName: "app", UserID: "user"}
	assert.Equal(t, hashUserKey(k), hashUserKey(k), "hash should be deterministic")
	other := memory.UserKey{AppName: "app", UserID: "user2"}
	assert.NotEqual(t, hashUserKey(k), hashUserKey(other), "different keys should hash differently")
}

func TestNewIngestWorker_AppliesDefaults(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.NotFoundHandler(), serviceOpts{})
	assert.Len(t, w.jobChans, defaultAsyncMemoryNum)
	assert.Equal(t, defaultMemoryQueueSize, cap(w.jobChans[0]))
}

func TestNewIngestWorker_RespectsCustomSizes(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.NotFoundHandler(), serviceOpts{
		asyncMemoryNum:  3,
		memoryQueueSize: 7,
	})
	assert.Len(t, w.jobChans, 3)
	assert.Equal(t, 7, cap(w.jobChans[0]))
}

func TestIngestWorker_StopIsIdempotent(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.NotFoundHandler(), serviceOpts{})
	w.Stop()
	assert.NotPanics(t, w.Stop, "second Stop must not panic")
}

func TestIngestWorker_TryEnqueue_NilJobReturnsTrue(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.NotFoundHandler(), serviceOpts{})
	assert.True(t, w.tryEnqueue(context.Background(), nil))
}

func TestIngestWorker_TryEnqueue_CanceledContext(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.NotFoundHandler(), serviceOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok := w.tryEnqueue(ctx, &ingestJob{UserKey: memory.UserKey{AppName: "a", UserID: "u"}})
	assert.False(t, ok, "canceled context should not enqueue")
}

func TestIngestWorker_TryEnqueue_AfterStopReturnsFalse(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.NotFoundHandler(), serviceOpts{})
	w.Stop()
	ok := w.tryEnqueue(context.Background(), &ingestJob{UserKey: memory.UserKey{AppName: "a", UserID: "u"}})
	assert.False(t, ok, "stopped worker should not enqueue")
}

func TestIngestWorker_TryEnqueue_QueueFullReturnsFalse(t *testing.T) {
	// Block the worker by making every request hang; fill the queue.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	opts := serviceOpts{
		apiKey:           "k",
		host:             srv.URL,
		asyncMemoryNum:   1,
		memoryQueueSize:  1,
		memoryJobTimeout: 200 * time.Millisecond,
	}
	c, _ := newClient(opts)
	w := newIngestWorker(c, opts)

	// Ensure we unblock before tearing down the worker; otherwise Stop()
	// would wait forever on the hanging HTTP handler.
	t.Cleanup(func() {
		close(block)
		w.Stop()
		srv.Close()
	})

	job := &ingestJob{
		UserKey:  memory.UserKey{AppName: "a", UserID: "u"},
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	// First one is picked up by the worker (blocked in handler).
	w.tryEnqueue(context.Background(), job)
	time.Sleep(50 * time.Millisecond)
	// Fill the queue.
	w.tryEnqueue(context.Background(), job)
	// The next one must fail because the buffer is full.
	assert.False(t, w.tryEnqueue(context.Background(), job),
		"enqueue should fail when queue is full")
}

func TestIngestWorker_Ingest_EmptyMessagesIsNoOp(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called")
	}), serviceOpts{})
	err := w.ingest(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"},
		&session.Session{},
		[]model.Message{{Role: model.RoleUser, Content: "   "}}, // whitespace → filtered out
		session.IngestOptions{},
	)
	assert.NoError(t, err)
}

func TestIngestWorker_Ingest_CreatesAndTerminalStatus(t *testing.T) {
	var createCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/memories/" {
			atomic.AddInt32(&createCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"x","status":"SUCCEEDED"}]`))
			return
		}
		http.NotFound(w, r)
	})
	w, _ := newWorkerWithServer(t, handler, serviceOpts{memoryJobTimeout: time.Second})
	err := w.ingest(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"},
		nil,
		[]model.Message{{Role: model.RoleUser, Content: "hi"}},
		session.IngestOptions{
			Metadata: map[string]any{"k": "v"},
			AgentID:  "agent",
			RunID:    "run",
		},
	)
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&createCalls))
}

func TestIngestWorker_AwaitIngestEvent_PollsUntilSuccess(t *testing.T) {
	var calls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/event/") {
			http.NotFound(w, r)
			return
		}
		n := atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		if n < 3 {
			_, _ = w.Write([]byte(`{"id":"x","status":"PENDING"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x","status":"SUCCEEDED"}`))
	})
	c, err := newClient(serviceOpts{apiKey: "k", host: httpServerURL(t, handler)})
	require.NoError(t, err)
	w := &ingestWorker{c: c}
	// The polling interval is a package-level 2s constant; tests must tolerate
	// a few cycles.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := w.awaitIngestEvent(ctx, "x")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "SUCCEEDED", res.Status)
}

func TestIngestWorker_AwaitIngestEvent_FailedStatus(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","status":"FAILED"}`))
	})
	c, _ := newClient(serviceOpts{apiKey: "k", host: httpServerURL(t, handler)})
	w := &ingestWorker{c: c}
	_, err := w.awaitIngestEvent(context.Background(), "x")
	assert.Error(t, err)
}

func TestIngestWorker_AwaitIngestEvent_UnknownStatusWithResults(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","status":"UNKNOWN","results":[{"id":"y"}]}`))
	})
	c, _ := newClient(serviceOpts{apiKey: "k", host: httpServerURL(t, handler)})
	w := &ingestWorker{c: c}
	res, err := w.awaitIngestEvent(context.Background(), "x")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Len(t, res.Results, 1)
}

func TestIngestWorker_AwaitIngestEvent_BackendError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	})
	c, _ := newClient(serviceOpts{apiKey: "k", host: httpServerURL(t, handler)})
	w := &ingestWorker{c: c}
	_, err := w.awaitIngestEvent(context.Background(), "x")
	assert.Error(t, err)
}

func TestAwaitQueuedEvents_EmptyEventID_Succeeded(t *testing.T) {
	w := &ingestWorker{}
	err := w.awaitQueuedEvents(context.Background(), createMemoryEvents{
		{Status: "SUCCEEDED"},
	})
	assert.NoError(t, err)
}

func TestAwaitQueuedEvents_EmptyEventID_Failed(t *testing.T) {
	w := &ingestWorker{}
	err := w.awaitQueuedEvents(context.Background(), createMemoryEvents{
		{Status: "FAILED", Message: "boom"},
	})
	assert.Error(t, err)
}

func TestAwaitQueuedEvents_EventIDPropagatesError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","status":"FAILED"}`))
	})
	c, _ := newClient(serviceOpts{apiKey: "k", host: httpServerURL(t, handler)})
	w := &ingestWorker{c: c}
	err := w.awaitQueuedEvents(context.Background(), createMemoryEvents{
		{EventID: "e1"},
	})
	assert.Error(t, err, "error from awaitIngestEvent must propagate")
}

func TestProcess_NilJobIsNoOp(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.NotFoundHandler(), serviceOpts{})
	assert.NotPanics(t, func() { w.process(nil) })
}

func TestProcess_LogsErrorButRecovers(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	w, _ := newWorkerWithServer(t, handler, serviceOpts{
		memoryJobTimeout: 200 * time.Millisecond,
	})
	// process must swallow the backend error without panicking.
	assert.NotPanics(t, func() {
		w.process(&ingestJob{
			Ctx:      context.Background(),
			UserKey:  memory.UserKey{AppName: "a", UserID: "u"},
			Messages: []model.Message{{Role: model.RoleUser, Content: "x"}},
		})
	})
}

func TestIngestWorker_TryEnqueue_HashesBySessionOrUserKey(t *testing.T) {
	w, _ := newWorkerWithServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"x","status":"SUCCEEDED"}]`))
	}), serviceOpts{asyncMemoryNum: 2, memoryQueueSize: 4})

	// Session with Hash set → uses hash path.
	assert.True(t, w.tryEnqueue(context.Background(), &ingestJob{
		Session: &session.Session{Hash: 12345},
		UserKey: memory.UserKey{AppName: "a", UserID: "u"},
	}))
	// Zero-hash session → falls back to hashUserKey.
	assert.True(t, w.tryEnqueue(context.Background(), &ingestJob{
		Session: &session.Session{Hash: 0},
		UserKey: memory.UserKey{AppName: "a", UserID: "u"},
	}))
}
