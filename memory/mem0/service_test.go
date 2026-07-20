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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestNewService_SelfHostedOSSAllowsEmptyAPIKey(t *testing.T) {
	svc, err := NewService(WithSelfHostedOSS())
	require.NoError(t, err)
	defer svc.Close()
	assert.Equal(t, apiModeSelfHostedOSS, svc.opts.apiMode)
	assert.Equal(t, defaultSelfHostedOSSHost, svc.opts.host)
}

func TestNewService_SelfHostedOSSRejectsCloudDefaultHost(t *testing.T) {
	_, err := NewService(WithSelfHostedOSS(), WithHost(defaultHost))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default host")

	_, err = NewService(WithSelfHostedOSS(), WithHost(defaultHost+"/"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default host")
}

func TestNewService_SelfHostedOSSRejectsOrgProject(t *testing.T) {
	_, err := NewService(
		WithSelfHostedOSS(),
		WithHost("http://localhost:8888"),
		WithOrgProject("org", "project"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "org/project")
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
	gotBody := make(chan []byte, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody <- body
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

	var body string
	select {
	case requestBody := <-gotBody:
		body = string(requestBody)
	case <-time.After(2 * time.Second):
		t.Fatal("backend was not hit")
	}
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

func TestIngestSession_ValidatesOptionalFieldsBeforeWatermark(t *testing.T) {
	tests := []struct {
		name    string
		service []ServiceOpt
		opts    []session.IngestOption
		wantErr string
	}{
		{
			name:    "invalid expiration date",
			service: []ServiceOpt{WithSelfHostedOSS(), WithHost("http://localhost:8888")},
			opts: []session.IngestOption{func(opts *session.IngestOptions) {
				opts.ExpirationDate = "07/17/2026"
			}},
			wantErr: "invalid expiration date",
		},
		{
			name:    "unsupported memory type",
			service: []ServiceOpt{WithSelfHostedOSS(), WithHost("http://localhost:8888")},
			opts:    []session.IngestOption{WithIngestMemoryType(MemoryType("core"))},
			wantErr: "unsupported memory type",
		},
		{
			name:    "procedural memory without agent",
			service: []ServiceOpt{WithSelfHostedOSS(), WithHost("http://localhost:8888")},
			opts:    []session.IngestOption{WithIngestMemoryType(MemoryTypeProcedural)},
			wantErr: "requires an agent ID",
		},
		{
			name:    "OSS-only prompt in cloud mode",
			service: []ServiceOpt{WithAPIKey("key")},
			opts:    []session.IngestOption{WithIngestPrompt("extract deadlines")},
			wantErr: "require self-hosted OSS mode",
		},
		{
			name:    "OSS-only expiration in cloud mode",
			service: []ServiceOpt{WithAPIKey("key")},
			opts: []session.IngestOption{WithIngestExpirationDate(
				time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
			)},
			wantErr: "require self-hosted OSS mode",
		},
		{
			name:    "OSS-only memory type in cloud mode",
			service: []ServiceOpt{WithAPIKey("key")},
			opts: []session.IngestOption{
				session.WithIngestAgentID("agent-1"),
				WithIngestMemoryType(MemoryTypeProcedural),
			},
			wantErr: "require self-hosted OSS mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewService(tt.service...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = svc.Close() })
			sess := newIngestTestSession()
			err = svc.IngestSession(context.Background(), sess, tt.opts...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.True(t, readLastExtractAt(sess).IsZero())
		})
	}
}

func TestResolveIngestOptionsSnapshotsCallerData(t *testing.T) {
	metadata := map[string]any{"nested": map[string]any{"value": "before"}}
	infer := false
	opts := resolveIngestOptions([]session.IngestOption{func(opts *session.IngestOptions) {
		opts.Metadata = metadata
		opts.Infer = &infer
	}})
	metadata["nested"].(map[string]any)["value"] = "after"
	infer = true

	assert.Equal(t, "before", opts.Metadata["nested"].(map[string]any)["value"])
	require.NotNil(t, opts.Infer)
	assert.False(t, *opts.Infer)
}

func newIngestTestSession() *session.Session {
	return &session.Session{
		AppName: "app",
		UserID:  "user",
		ID:      "session",
		Events: []event.Event{{
			Timestamp: time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "hello"},
			}}},
		}},
	}
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
	var calls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
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
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "limit should stop cloud pagination")
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

func TestReadMemories_SelfHostedOSSFiltersByAppMetadata(t *testing.T) {
	var gotPath string
	var gotQuery string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"a","memory":"keep","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-01T00:00:00Z"},
			{"id":"legacy","memory":"drop legacy without metadata","created_at":"2024-01-02T00:00:00Z"},
			{"id":"b","memory":"drop","metadata":{"trpc_app_name":"other"},"created_at":"2024-01-02T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "a", entries[0].ID)
	assert.Equal(t, "/memories", gotPath)
	assert.Contains(t, gotQuery, "user_id=user")
	assert.Contains(t, gotQuery, "top_k=")
}

func TestReadMemories_SelfHostedOSSCanIncludeUnscopedMemories(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/memories", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"app","memory":"keep app","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-03T00:00:00Z"},
			{"id":"legacy-missing","memory":"keep legacy missing","created_at":"2024-01-02T00:00:00Z"},
			{"id":"legacy-non-string","memory":"keep legacy non string","metadata":{"trpc_app_name":123},"created_at":"2024-01-01T00:00:00Z"},
			{"id":"other","memory":"drop other app","metadata":{"trpc_app_name":"other"},"created_at":"2024-01-04T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithSelfHostedOSSIncludeUnscopedMemories(),
	)
	require.NoError(t, err)
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 10)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "app", entries[0].ID)
	assert.Equal(t, "legacy-missing", entries[1].ID)
	assert.Equal(t, "legacy-non-string", entries[2].ID)
}

func TestReadMemories_SelfHostedOSSRejectsUnboundedLimit(t *testing.T) {
	svc, err := NewService(WithSelfHostedOSS(), WithHost("http://localhost:8888"))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive limit")

	_, err = svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, maxOSSListTopK+1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestReadMemories_SelfHostedOSSBackendErrorPropagates(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/memories", r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"detail":"upstream unavailable"}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestReadMemories_SelfHostedOSSSortsLimitsAndSkipsInvalidRecords(t *testing.T) {
	var gotQuery string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		assert.Equal(t, "/memories", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"older-update","memory":"older update","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-05T00:00:00Z","updated_at":"2024-01-06T00:00:00Z"},
			{"id":"newer-update","memory":"newer update","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-07T00:00:00Z"},
			{"id":"same-update-newer-created","memory":"same update newer created","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-06T00:00:00Z","updated_at":"2024-01-06T00:00:00Z"},
			{"id":"wrong-app","memory":"drop wrong app","metadata":{"trpc_app_name":"other"},"created_at":"2024-01-08T00:00:00Z","updated_at":"2024-01-08T00:00:00Z"},
			{"id":"","memory":"drop empty id","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-09T00:00:00Z","updated_at":"2024-01-09T00:00:00Z"},
			{"id":"empty-memory","memory":"","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-10T00:00:00Z","updated_at":"2024-01-10T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "newer-update", entries[0].ID)
	assert.Equal(t, "same-update-newer-created", entries[1].ID)
	assert.Contains(t, gotQuery, "user_id=user")
	assert.Contains(t, gotQuery, "top_k=1000")
}

func TestReadOSSMemories_ForwardsProviderScopes(t *testing.T) {
	var gotQuery string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[{
			"id":"memory-1",
			"memory":"remember this",
			"metadata":{"trpc_app_name":"app"},
			"agent_id":"agent-1",
			"run_id":"run-1",
			"expiration_date":"2026-08-01"
		}]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	entries, err := svc.ReadOSSMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		10,
		OSSReadOptions{AgentID: "agent-1", RunID: "run-1", IncludeExpired: true},
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	query, err := url.ParseQuery(gotQuery)
	require.NoError(t, err)
	assert.Equal(t, "user", query.Get(queryKeyUserID))
	assert.Equal(t, "agent-1", query.Get(queryKeyAgentID))
	assert.Equal(t, "run-1", query.Get(queryKeyRunID))
	assert.Equal(t, "true", query.Get(queryKeyShowExpired))
	require.NotNil(t, entries[0].Entry)
	assert.Equal(t, "memory-1", entries[0].Entry.ID)
	assert.Equal(t, "agent-1", entries[0].AgentID)
	assert.Equal(t, "run-1", entries[0].RunID)
	assert.Equal(t, "2026-08-01", entries[0].ExpirationDate)
}

func TestReadOSSMemories_RejectsCloudMode(t *testing.T) {
	svc, err := NewService(WithAPIKey("key"))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.ReadOSSMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		10,
		OSSReadOptions{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "require self-hosted OSS mode")
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

func TestSearchMemories_SelfHostedOSSUsesMetadataFilter(t *testing.T) {
	var gotReq searchV2Request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotReq))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"keep","memory":"match","metadata":{"trpc_app_name":"app"},"score":0.9},
			{"id":"drop","memory":"wrong app","metadata":{"trpc_app_name":"other"},"score":1}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL), WithAPIKey("oss-key"))
	require.NoError(t, err)
	defer svc.Close()

	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, "query")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "keep", got[0].ID)
	assert.Equal(t, "query", gotReq.Query)
	assert.Equal(t, map[string]any{
		queryKeyUserID:         "user",
		metadataKeyTRPCAppName: "app",
	}, gotReq.Filters)
}

func TestSearchMemories_SelfHostedOSSCanIncludeUnscopedMemories(t *testing.T) {
	var gotReq searchV2Request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotReq))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"app","memory":"keep app","metadata":{"trpc_app_name":"app"},"score":0.9},
			{"id":"legacy","memory":"keep legacy","score":0.8},
			{"id":"other","memory":"drop other app","metadata":{"trpc_app_name":"other"},"score":1}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithAPIKey("oss-key"),
		WithSelfHostedOSSIncludeUnscopedMemories(),
	)
	require.NoError(t, err)
	defer svc.Close()

	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, "query")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "app", got[0].ID)
	assert.Equal(t, "legacy", got[1].ID)
	assert.Equal(t, map[string]any{queryKeyUserID: "user"}, gotReq.Filters)
}

func TestSearchMemories_SelfHostedOSSForwardsOptionalFields(t *testing.T) {
	var gotReq searchV2Request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotReq))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[{
			"id":"memory-1",
			"memory":"match",
			"metadata":{"trpc_app_name":"app","custom":"value"},
			"score":0.9,
			"score_details":{"semantic":0.8,"lexical":0.1},
			"agent_id":"agent-1",
			"run_id":"run-1",
			"hash":"hash-1",
			"expiration_date":"2026-08-01",
			"actor_id":"actor-1",
			"role":"user",
			"attributed_to":"alice"
		}]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	searchOpts := memory.SearchOptions{
		Query:               "query",
		AgentID:             "agent-1",
		RunID:               "run-1",
		SimilarityThreshold: 0.42,
		IncludeExpired:      true,
		Explain:             true,
	}
	entries, err := svc.SearchOSSMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		"query",
		memory.WithSearchOptions(searchOpts),
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "agent-1", gotReq.Filters[queryKeyAgentID])
	assert.Equal(t, "run-1", gotReq.Filters[queryKeyRunID])
	require.NotNil(t, gotReq.Threshold)
	assert.InDelta(t, 0.42, *gotReq.Threshold, 1e-9)
	assert.True(t, gotReq.Explain)
	assert.True(t, gotReq.ShowExpired)
	require.NotNil(t, entries[0].Entry)
	assert.Equal(t, "memory-1", entries[0].Entry.ID)
	assert.InDelta(t, 0.9, entries[0].Entry.Score, 1e-9)
	assert.Equal(t, map[string]any{"semantic": 0.8, "lexical": 0.1}, entries[0].ScoreDetails)
	assert.Equal(t, "agent-1", entries[0].AgentID)
	assert.Equal(t, "run-1", entries[0].RunID)
	assert.Equal(t, "hash-1", entries[0].Hash)
	assert.Equal(t, "2026-08-01", entries[0].ExpirationDate)
	assert.Equal(t, "actor-1", entries[0].ActorID)
	assert.Equal(t, "user", entries[0].Role)
	assert.Equal(t, "alice", entries[0].AttributedTo)
	assert.Equal(t, map[string]any{
		metadataKeyTRPCAppName: "app",
		"custom":               "value",
	}, entries[0].Metadata)
}

func TestSearchOSSMemories_RejectsCloudMode(t *testing.T) {
	svc, err := NewService(WithAPIKey("key"))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.SearchOSSMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		"query",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "require self-hosted OSS mode")
}

func TestSearchMemories_CloudDoesNotForwardOSSFields(t *testing.T) {
	var gotReq map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotReq))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, err := NewService(WithAPIKey("key"), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		"query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:               "query",
			AgentID:             "agent-1",
			RunID:               "run-1",
			SimilarityThreshold: 0.42,
			IncludeExpired:      true,
			Explain:             true,
		}),
	)
	require.NoError(t, err)
	assert.NotContains(t, gotReq, "threshold")
	assert.NotContains(t, gotReq, "explain")
	assert.NotContains(t, gotReq, "show_expired")
}
