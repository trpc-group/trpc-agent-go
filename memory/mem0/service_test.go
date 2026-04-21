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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- NewService / lifecycle ---

func TestNewService_FailsWithoutAPIKey(t *testing.T) {
	_, err := NewService()
	assert.Error(t, err, "missing api key must fail")
}

func TestNewService_DefaultsToolsList(t *testing.T) {
	svc, err := NewService(WithAPIKey("k"))
	require.NoError(t, err)
	defer svc.Close()
	assert.Len(t, svc.Tools(), 1)

	// Tools() must return a fresh slice (not share backing array).
	tools1 := svc.Tools()
	tools1[0] = nil
	tools2 := svc.Tools()
	assert.NotNil(t, tools2[0], "Tools() must return an independent slice")
}

func TestService_CloseIsIdempotent(t *testing.T) {
	svc, err := NewService(WithAPIKey("k"))
	require.NoError(t, err)
	assert.NoError(t, svc.Close())
	assert.NoError(t, svc.Close(), "second Close must be a no-op")
}

// --- IngestSession ---

func TestIngestSession_InvalidUserKeyReturnsError(t *testing.T) {
	svc, err := NewService(WithAPIKey("test-key"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	cases := []struct {
		name string
		sess *session.Session
	}{
		{"empty app name", &session.Session{UserID: "u", ID: "s"}},
		{"empty user id", &session.Session{AppName: "a", ID: "s"}},
		{"both empty", &session.Session{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Error(t, svc.IngestSession(context.Background(), tc.sess))
		})
	}
}

func TestIngestSession_NilSessionReturnsNil(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	assert.NoError(t, svc.IngestSession(context.Background(), nil))
}

func TestIngestSession_NoMessagesReturnsNil(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	// A session with no events and a valid user key → nothing to ingest.
	sess := &session.Session{AppName: "app", UserID: "user", ID: "s"}
	assert.NoError(t, svc.IngestSession(context.Background(), sess))
}

func TestIngestSession_NilContext(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	sess := &session.Session{AppName: "app", UserID: "user", ID: "s"}
	// Route through an interface var so staticcheck can't statically see the
	// literal nil; we're intentionally exercising the nil-context fallback.
	var nilCtx context.Context
	assert.NoError(t, svc.IngestSession(nilCtx, sess))
}

func TestIngestSession_ForwardsMessagesToBackend(t *testing.T) {
	var gotBody []byte
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = buf[:n]
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"x","status":"SUCCEEDED"}]`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithAPIKey("k"),
		WithHost(srv.URL),
		WithAsyncMode(false),
		WithMemoryJobTimeout(time.Second),
	)
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{AppName: "app", UserID: "user", ID: "s"}
	sess.Events = []event.Event{{
		Timestamp: time.Now(),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "hello"},
		}}},
	}}

	require.NoError(t, svc.IngestSession(context.Background(), sess,
		session.WithIngestMetadata(map[string]any{"k": "v"}),
		session.WithIngestAgentID("agent-1"),
		session.WithIngestRunID("run-1"),
	))

	// Wait for the async worker to hit the backend.
	assert.Eventually(t, func() bool { return len(gotBody) > 0 },
		2*time.Second, 10*time.Millisecond, "backend was not hit")

	body := string(gotBody)
	for _, want := range []string{"hello", "agent-1", "run-1", `"k":"v"`} {
		assert.Contains(t, body, want)
	}
}

func TestIngestSession_SyncFallbackWhenQueueFull(t *testing.T) {
	// Build a service pointing at a server that returns a quick SUCCEEDED
	// response, then manually stop the async worker so every subsequent
	// IngestSession call is forced onto the synchronous fallback path.
	var ingestCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/memories/" {
			atomic.AddInt32(&ingestCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"x","status":"SUCCEEDED"}]`))
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithAPIKey("k"),
		WithHost(srv.URL),
		WithAsyncMode(false),
		WithAsyncMemoryNum(1),
		WithMemoryQueueSize(1),
		WithMemoryJobTimeout(500*time.Millisecond),
	)
	require.NoError(t, err)
	defer svc.Close()

	// Kill the async worker so tryEnqueue always returns false, forcing
	// every IngestSession call to take the synchronous fallback path.
	svc.ingestWorker.Stop()

	sess := &session.Session{AppName: "app", UserID: "user", ID: "s1"}
	sess.Events = []event.Event{{
		Timestamp: time.Now(),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "hello"},
		}}},
	}}

	require.NoError(t, svc.IngestSession(context.Background(), sess))
	assert.Equal(t, int32(1), atomic.LoadInt32(&ingestCalls),
		"sync fallback must hit the backend exactly once")
}

// --- ReadMemories ---

func TestReadMemories_InvalidUserKeyReturnsError(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	_, err := svc.ReadMemories(context.Background(), memory.UserKey{}, 10)
	assert.Error(t, err)
}

func TestReadMemories_PaginatesAndSorts(t *testing.T) {
	var calls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		body := `[
			{"id":"a","memory":"first","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-05T00:00:00Z"},
			{"id":"b","memory":"second","created_at":"2024-02-01T00:00:00Z","updated_at":"2024-01-10T00:00:00Z"}
		]`
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	// b.UpdatedAt > a.UpdatedAt → b first.
	assert.Equal(t, "b", entries[0].ID)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(2), "pagination must hit more than one page")
}

func TestReadMemories_AppliesLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":"a","memory":"one","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"},
			{"id":"b","memory":"two","created_at":"2024-01-02T00:00:00Z","updated_at":"2024-01-02T00:00:00Z"},
			{"id":"c","memory":"three","created_at":"2024-01-03T00:00:00Z","updated_at":"2024-01-03T00:00:00Z"}
		]`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 1)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestReadMemories_StopsOnInvalidPage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Invalid page."}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"a","memory":"x"}]`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestReadMemories_BackendErrorPropagates(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()
	_, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, 0)
	assert.Error(t, err)
}

// --- SearchMemories ---

func TestSearchMemories_InvalidUserKey(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	_, err := svc.SearchMemories(context.Background(), memory.UserKey{}, "q")
	assert.Error(t, err)
}

func TestSearchMemories_EmptyQueryReturnsEmpty(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, "   ")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSearchMemories_ReturnsSortedMatches(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v2/memories/search/") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[
			{"id":"a","memory":"low","score":0.1,"created_at":"2024-01-01T00:00:00Z"},
			{"id":"b","memory":"high","score":0.9,"created_at":"2024-01-02T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL), WithOrgProject("o", "p"))
	defer svc.Close()

	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, "q")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b", got[0].ID, "higher score should come first")
}

func TestSearchMemories_AppliesMaxResultsAndSimilarity(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[
			{"id":"a","memory":"low","score":0.1,"created_at":"2024-01-01T00:00:00Z"},
			{"id":"b","memory":"mid","score":0.5,"created_at":"2024-01-02T00:00:00Z"},
			{"id":"c","memory":"high","score":0.9,"created_at":"2024-01-03T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()

	// SimilarityThreshold=0.3 drops "a", MaxResults=1 keeps only "c".
	opts := memory.SearchOptions{
		Query:               "q",
		MaxResults:          1,
		SimilarityThreshold: 0.3,
	}
	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, "q",
		memory.WithSearchOptions(opts),
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "c", got[0].ID)
}

func TestSearchMemories_BackendErrorPropagates(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()
	_, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, "q")
	assert.Error(t, err)
}
