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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newSvcWithServer(t *testing.T, handler http.Handler, svcOpts ...ServiceOpt) (*Service, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	all := append([]ServiceOpt{
		WithAPIKey("k"),
		WithHost(srv.URL),
		WithAsyncMode(false),
		WithAsyncMemoryNum(1),
		WithMemoryQueueSize(1),
		WithMemoryJobTimeout(time.Second),
	}, svcOpts...)
	svc, err := NewService(all...)
	if err != nil {
		srv.Close()
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
		srv.Close()
	})
	return svc, srv
}

func invocationContext(appName, userID string) context.Context {
	sess := &session.Session{
		AppName: appName,
		UserID:  userID,
		ID:      "s1",
	}
	inv := &agent.Invocation{AgentName: "a", Session: sess}
	return agent.NewInvocationContext(context.Background(), inv)
}

func callable(t *testing.T, tl tool.Tool) tool.CallableTool {
	t.Helper()
	ct, ok := tl.(tool.CallableTool)
	require.True(t, ok, "tool is not callable: %T", tl)
	return ct
}

func TestBuildReadOnlyTools_OnlySearchByDefault(t *testing.T) {
	svc, _ := newSvcWithServer(t, http.NotFoundHandler())
	tools := svc.Tools()
	require.Len(t, tools, 1)
	assert.Equal(t, memory.SearchToolName, tools[0].Declaration().Name)
}

func TestBuildReadOnlyTools_LoadEnabled(t *testing.T) {
	svc, _ := newSvcWithServer(t, http.NotFoundHandler(), WithLoadToolEnabled(true))
	tools := svc.Tools()
	require.Len(t, tools, 2)
	assert.Equal(t, memory.SearchToolName, tools[0].Declaration().Name)
	assert.Equal(t, memory.LoadToolName, tools[1].Declaration().Name)
}

func TestSearchTool_EmptyQueryReturnsEmpty(t *testing.T) {
	svc, _ := newSvcWithServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be hit for empty query")
	}))
	tl := callable(t, svc.Tools()[0])
	req, _ := json.Marshal(memorytool.SearchMemoryRequest{Query: "   "})

	res, err := tl.Call(invocationContext("app", "user"), req)
	require.NoError(t, err)
	resp, ok := res.(*memorytool.SearchMemoryResponse)
	require.True(t, ok, "unexpected resp type %T", res)
	assert.Zero(t, resp.Count)
	assert.Empty(t, resp.Results)
}

func TestSearchTool_MissingContextReturnsError(t *testing.T) {
	svc, _ := newSvcWithServer(t, http.NotFoundHandler())
	tl := callable(t, svc.Tools()[0])
	req, _ := json.Marshal(memorytool.SearchMemoryRequest{Query: "x"})
	_, err := tl.Call(context.Background(), req)
	assert.Error(t, err)
}

func TestSearchTool_SuccessfullyCallsBackend(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v2/memories/search/") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[{"id":"m1","memory":"hello","score":0.9}]}`))
	})
	svc, _ := newSvcWithServer(t, handler)
	tl := callable(t, svc.Tools()[0])
	req, _ := json.Marshal(memorytool.SearchMemoryRequest{Query: "hello"})

	got, err := tl.Call(invocationContext("app", "user"), req)
	require.NoError(t, err)
	resp, ok := got.(*memorytool.SearchMemoryResponse)
	require.True(t, ok)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, "m1", resp.Results[0].ID)
	assert.Equal(t, 1, resp.Count)
}

func TestSearchTool_InvalidTimeReturnsError(t *testing.T) {
	svc, _ := newSvcWithServer(t, http.NotFoundHandler())
	tl := callable(t, svc.Tools()[0])
	req, _ := json.Marshal(memorytool.SearchMemoryRequest{Query: "q", TimeAfter: "not-a-date"})
	_, err := tl.Call(invocationContext("app", "user"), req)
	assert.Error(t, err)
}

func TestSearchTool_BackendErrorPropagates(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})
	svc, _ := newSvcWithServer(t, handler)
	tl := callable(t, svc.Tools()[0])
	req, _ := json.Marshal(memorytool.SearchMemoryRequest{Query: "q"})
	_, err := tl.Call(invocationContext("app", "user"), req)
	assert.Error(t, err)
}

func TestLoadTool_ReturnsEntries(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v1/memories/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") != "1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"m1","memory":"content","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-02T00:00:00Z"}]`))
	})
	svc, _ := newSvcWithServer(t, handler, WithLoadToolEnabled(true))
	tl := callable(t, svc.Tools()[1])
	req, _ := json.Marshal(memorytool.LoadMemoryRequest{Limit: 5})

	got, err := tl.Call(invocationContext("app", "user"), req)
	require.NoError(t, err)
	resp, ok := got.(*memorytool.LoadMemoryResponse)
	require.True(t, ok)
	assert.Equal(t, 5, resp.Limit)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, "m1", resp.Results[0].ID)
}

func TestLoadTool_DefaultLimit(t *testing.T) {
	var gotQuery string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
	svc, _ := newSvcWithServer(t, handler, WithLoadToolEnabled(true))
	tl := callable(t, svc.Tools()[1])
	req, _ := json.Marshal(memorytool.LoadMemoryRequest{})

	_, err := tl.Call(invocationContext("app", "user"), req)
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "page_size=10", "default limit should be 10")
}

func TestLoadTool_MissingContextReturnsError(t *testing.T) {
	svc, _ := newSvcWithServer(t, http.NotFoundHandler(), WithLoadToolEnabled(true))
	tl := callable(t, svc.Tools()[1])
	req, _ := json.Marshal(memorytool.LoadMemoryRequest{})
	_, err := tl.Call(context.Background(), req)
	assert.Error(t, err)
}

func TestLoadTool_BackendErrorPropagates(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	svc, _ := newSvcWithServer(t, handler, WithLoadToolEnabled(true))
	tl := callable(t, svc.Tools()[1])
	req, _ := json.Marshal(memorytool.LoadMemoryRequest{Limit: 2})
	_, err := tl.Call(invocationContext("app", "user"), req)
	assert.Error(t, err)
}

func TestBuildToolSearchOptions(t *testing.T) {
	t.Run("valid time bounds", func(t *testing.T) {
		req := &memorytool.SearchMemoryRequest{
			Query:            "q",
			Kind:             "fact",
			TimeAfter:        "2024-01-01",
			TimeBefore:       "2024-02",
			OrderByEventTime: true,
		}
		got, err := buildToolSearchOptions(req)
		require.NoError(t, err)
		assert.Equal(t, "q", got.Query)
		assert.Equal(t, memory.KindFact, got.Kind)
		assert.True(t, got.KindFallback)
		assert.NotNil(t, got.TimeAfter)
		assert.NotNil(t, got.TimeBefore)
		assert.True(t, got.OrderByEventTime)
	})
	t.Run("invalid time_after", func(t *testing.T) {
		req := &memorytool.SearchMemoryRequest{Query: "q", TimeAfter: "garbage"}
		_, err := buildToolSearchOptions(req)
		assert.Error(t, err)
	})
	t.Run("invalid time_before", func(t *testing.T) {
		req := &memorytool.SearchMemoryRequest{Query: "q", TimeBefore: "garbage"}
		_, err := buildToolSearchOptions(req)
		assert.Error(t, err)
	})
	t.Run("no kind means no fallback", func(t *testing.T) {
		got, err := buildToolSearchOptions(&memorytool.SearchMemoryRequest{Query: "q"})
		require.NoError(t, err)
		assert.False(t, got.KindFallback)
	})
}

func TestEntryToResult(t *testing.T) {
	evt := time.Date(2024, 5, 7, 12, 34, 56, 0, time.UTC)
	e := &memory.Entry{
		ID:        "id1",
		CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Memory: &memory.Memory{
			Memory:       "hello",
			Topics:       []string{"t1"},
			Kind:         memory.KindEpisode,
			EventTime:    &evt,
			Participants: []string{"alice"},
			Location:     "tokyo",
		},
		Score: 0.42,
	}
	want := memorytool.Result{
		ID:           "id1",
		Memory:       "hello",
		Topics:       []string{"t1"},
		Created:      e.CreatedAt,
		Kind:         "episode",
		EventTime:    evt.Format(time.RFC3339),
		Participants: []string{"alice"},
		Location:     "tokyo",
		Score:        0.42,
	}
	assert.Equal(t, want, entryToResult(e))
}

func TestEntryToResult_OmitsEmptyOptionalFields(t *testing.T) {
	e := &memory.Entry{
		ID:     "id1",
		Memory: &memory.Memory{Memory: "hello"},
	}
	got := entryToResult(e)
	assert.Empty(t, got.Kind)
	assert.Empty(t, got.EventTime)
	assert.Nil(t, got.Participants)
	assert.Empty(t, got.Location)
}
