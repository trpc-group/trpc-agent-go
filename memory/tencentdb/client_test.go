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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayClientEndpointsAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathRecall:
			_ = json.NewEncoder(w).Encode(recallResponse{
				Context:             "legacy",
				PrependContext:      "prepend",
				AppendSystemContext: "append",
				Strategy:            "hybrid",
				MemoryCount:         2,
			})
		case pathSearchMemories:
			_ = json.NewEncoder(w).Encode(searchMemoriesResponse{
				Results:  "memory hit",
				Total:    1,
				Strategy: "semantic",
			})
		case pathEndSession:
			_ = json.NewEncoder(w).Encode(endSessionResponse{Flushed: true})
		case pathHealth:
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok", Version: "test"})
		default:
			http.Error(w, strings.Repeat("x", 700), http.StatusBadGateway)
		}
	}))
	defer server.Close()

	client, err := newGatewayClient(Options{
		GatewayURL:   server.URL,
		Timeout:      time.Second,
		MaxBodyBytes: defaultMaxBodyBytes,
	})
	require.NoError(t, err, "newGatewayClient")
	recall, err := client.recall(context.Background(), recallRequest{Query: "q", SessionKey: "s"})
	require.NoError(t, err, "recall")
	assert.Equal(t, "append", recall.AppendSystemContext)
	assert.Equal(t, 2, recall.MemoryCount)
	memories, err := client.searchMemories(context.Background(), searchMemoriesRequest{Query: "q"})
	require.NoError(t, err, "searchMemories")
	assert.Equal(t, "memory hit", memories.Results)
	assert.Equal(t, "semantic", memories.Strategy)
	ended, err := client.endSession(context.Background(), endSessionRequest{SessionKey: "s"})
	require.NoError(t, err, "endSession")
	assert.True(t, ended.Flushed)
	health, err := client.health(context.Background())
	require.NoError(t, err, "health")
	assert.Equal(t, "ok", health.Status)
	assert.Equal(t, "test", health.Version)

	err = client.doJSON(context.Background(), httpMethodGet, "/missing", nil, nil)
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Contains(t, apiErr.Error(), "status=502")
	assert.LessOrEqual(t, len(apiErr.Body), maxErrorBodyPreview+len("...(truncated)"))
	err = client.doJSON(context.Background(), httpMethodPost, pathCapture, map[string]any{
		"bad": func() {},
	}, nil)
	require.Error(t, err, "expected marshal error")

	tiny, err := newGatewayClient(Options{GatewayURL: server.URL, MaxBodyBytes: 4})
	require.NoError(t, err, "new tiny client")
	_, err = tiny.health(context.Background())
	require.Error(t, err, "expected response body too large")
	_, err = newGatewayClient(Options{GatewayURL: "://bad"})
	require.Error(t, err, "expected invalid gateway url error")
	_, err = newGatewayClient(Options{GatewayURL: "/path-only"})
	require.Error(t, err, "expected path-only gateway url error")
	_, err = newGatewayClient(Options{GatewayURL: "ftp://example.com"})
	require.Error(t, err, "expected unsupported scheme gateway url error")
	_, err = newGatewayClient(Options{})
	require.Error(t, err, "expected empty gateway url error")
	nullable, err := newGatewayClient(Options{GatewayURL: server.URL})
	require.NoError(t, err, "new nullable client")
	require.NoError(t, nullable.doJSON(context.Background(), httpMethodGet, pathHealth, nil, nil), "nil output should be accepted")
}

func TestGatewayClientSendsAPIKeyHeader(t *testing.T) {
	var captureAuth, healthAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathHealth:
			healthAuth = r.Header.Get(httpHeaderAuthorization)
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
		case pathCapture:
			captureAuth = r.Header.Get(httpHeaderAuthorization)
			_ = json.NewEncoder(w).Encode(captureResponse{})
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := newGatewayClient(Options{GatewayURL: server.URL, APIKey: "  secret-key  "})
	require.NoError(t, err, "newGatewayClient")
	assert.Equal(t, "secret-key", client.apiKey, "api key should be trimmed")
	_, err = client.capture(context.Background(), captureRequest{SessionKey: "s"})
	require.NoError(t, err, "capture")
	assert.Equal(t, "Bearer secret-key", captureAuth)
	_, err = client.health(context.Background())
	require.NoError(t, err, "health")
	assert.Empty(t, healthAuth, "health should remain unauthenticated")

	captureAuth = ""
	noKey, err := newGatewayClient(Options{GatewayURL: server.URL})
	require.NoError(t, err, "newGatewayClient without key")
	_, err = noKey.capture(context.Background(), captureRequest{SessionKey: "s"})
	require.NoError(t, err, "capture without key")
	assert.Empty(t, captureAuth, "no Authorization header without an API key")

	svc, err := NewService(WithGatewayURL(server.URL), WithAPIKey("  k  "))
	require.NoError(t, err, "NewService WithAPIKey")
	defer svc.Close()
	assert.Equal(t, "k", svc.client.apiKey, "WithAPIKey should trim and wire the key through")
}

func TestGatewayClientDecodeAndRequestEdges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/bad-json":
			_, _ = w.Write([]byte("{"))
		default:
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
		}
	}))
	defer server.Close()

	client, err := newGatewayClient(Options{GatewayURL: server.URL})
	require.NoError(t, err, "newGatewayClient")
	var out HealthResponse
	require.NoError(t, client.doJSON(context.Background(), httpMethodGet, "/empty", nil, &out), "empty response should be accepted")
	require.Error(t, client.doJSON(context.Background(), httpMethodGet, "/bad-json", nil, &out), "expected unmarshal error")
	require.Error(t, client.doJSONOnce(context.Background(), httpMethodGet, "://bad", nil, nil, true), "expected request build error")
}

func TestWithServiceIdentityValidation(t *testing.T) {
	tests := []struct {
		name      string
		serviceID string
		teamID    string
		agentID   string
		apiKey    string
		wantError string
	}{
		{
			name:      "missing service ID",
			teamID:    "team-1",
			agentID:   "agent-1",
			apiKey:    "key",
			wantError: "service id is required",
		},
		{
			name:      "missing team ID",
			serviceID: "service-1",
			agentID:   "agent-1",
			apiKey:    "key",
			wantError: "team id is required",
		},
		{
			name:      "missing agent ID",
			serviceID: "service-1",
			teamID:    "team-1",
			apiKey:    "key",
			wantError: "agent id is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(
				WithGatewayURL("http://127.0.0.1:8420"),
				WithAPIKey(tt.apiKey),
				WithServiceIdentity(tt.serviceID, tt.teamID, tt.agentID),
			)
			require.ErrorContains(t, err, tt.wantError)
		})
	}

	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:8420"),
		WithServiceIdentity("service-1", "team-1", "agent-1"),
	)
	require.NoError(t, err)
	assert.Empty(t, svc.client.apiKey)
	require.NoError(t, svc.Close())

	svc, err = NewService(
		WithGatewayURL("http://127.0.0.1:8420"),
		WithAPIKey(" key "),
		WithServiceIdentity(" service-1 ", " team-1 ", " agent-1 "),
	)
	require.NoError(t, err)
	require.NotNil(t, svc.client.identity)
	assert.Equal(t, "service-1", svc.client.identity.serviceID)
	assert.Equal(t, "team-1", svc.client.identity.teamID)
	assert.Equal(t, "agent-1", svc.client.identity.agentID)
	require.NoError(t, svc.Close())

	svc, err = NewService(
		WithGatewayURL("http://127.0.0.1:8420"),
		WithAPIKey("key"),
		WithServiceIdentity("service-old", "team-old", "agent-old"),
		WithServiceIdentity("service-new", "team-new", "agent-new"),
	)
	require.NoError(t, err)
	assert.Equal(t, "service-new", svc.client.identity.serviceID)
	assert.Equal(t, "team-new", svc.client.identity.teamID)
	assert.Equal(t, "agent-new", svc.client.identity.agentID)
	require.NoError(t, svc.Close())
}

func TestV3VersionUnmarshal(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      v3Version
		wantError bool
	}{
		{name: "string", input: `{"version":"v2"}`, want: "v2"},
		{name: "number", input: `{"version":2}`, want: "2"},
		{name: "null", input: `{"version":null}`, want: ""},
		{name: "fraction", input: `{"version":2.5}`, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got struct {
				Version v3Version `json:"version"`
			}
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Version)
		})
	}
}

func TestGatewayClientV3WithoutAPIKey(t *testing.T) {
	var authorization string
	var serviceID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get(httpHeaderAuthorization)
		serviceID = r.Header.Get(httpHeaderServiceID)
		writeV3TestEnvelope(w, v3ConversationAddData{
			AcceptedIDs:      []string{"message-1"},
			AcceptedVersions: []string{"v1"},
			TotalCount:       1,
		})
	}))
	defer server.Close()

	client, err := newGatewayClient(Options{
		GatewayURL: server.URL,
		identity: &serviceIdentity{
			serviceID: "service-1",
			teamID:    "team-1",
			agentID:   "agent-1",
		},
	})
	require.NoError(t, err)
	_, err = client.capture(context.Background(), captureRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Messages: []tdaiMessage{{
			Role:    "user",
			Content: "remember this",
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, authorization)
	assert.Equal(t, "service-1", serviceID)
}

func TestGatewayClientV3DataPlane(t *testing.T) {
	var (
		mu       sync.Mutex
		paths    []string
		addReq   v3ConversationAddRequest
		searches []v3Isolation
	)
	coreContent := "prefers concise code reviews"
	scenarioContent := "Always include a transaction-boundary check."
	scenarioCreatedAt := "2026-08-01T00:00:00Z"
	scenarioUpdatedAt := "2026-08-13T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(httpHeaderAuthorization); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		if got := r.Header.Get(httpHeaderServiceID); got != "service-1" {
			t.Errorf("service ID = %q, want service-1", got)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case pathV3ConversationAdd:
			mu.Lock()
			body, err := io.ReadAll(r.Body)
			if err != nil {
				mu.Unlock()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(body, &addReq); err != nil {
				mu.Unlock()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var rawAddReq struct {
				Messages []map[string]any `json:"messages"`
			}
			if err := json.Unmarshal(body, &rawAddReq); err != nil {
				mu.Unlock()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			require.Len(t, rawAddReq.Messages, 1)
			assert.NotContains(t, rawAddReq.Messages[0], "id")
			mu.Unlock()
			writeV3TestEnvelope(w, v3ConversationAddData{
				AcceptedIDs:      []string{"m1"},
				AcceptedVersions: []string{"v1"},
				TotalCount:       1,
			})
		case pathV3AtomicSearch:
			var req v3AtomicSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			searches = append(searches, req.v3Isolation)
			mu.Unlock()
			writeV3TestEnvelope(w, v3AtomicSearchData{Items: []v3AtomicSearchHit{{
				ID:      "atomic-1",
				Type:    "preference",
				Content: "uses PostgreSQL for durable state",
				Score:   0.9,
			}}})
		case pathV3ConversationSearch:
			var req v3ConversationSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			searches = append(searches, req.v3Isolation)
			mu.Unlock()
			writeV3TestEnvelope(w, v3ConversationSearchData{Messages: []v3ConversationSearchHit{{
				ID:      "conversation-1",
				Role:    "user",
				Content: "review the transaction boundary",
				Score:   0.8,
			}}})
		case pathV3ScenarioList:
			writeV3TestEnvelope(w, map[string]any{
				"entries": []map[string]any{{
					"path":       "reviews.md",
					"version":    1,
					"created_at": scenarioCreatedAt,
					"updated_at": scenarioUpdatedAt,
				}},
				"total": 1,
			})
		case pathV3ScenarioRead:
			var req v3ScenarioReadRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			searches = append(searches, req.v3Isolation)
			mu.Unlock()
			writeV3TestEnvelope(w, map[string]any{
				"path":       req.Path,
				"version":    2,
				"content":    scenarioContent,
				"created_at": scenarioCreatedAt,
				"updated_at": scenarioUpdatedAt,
			})
		case pathV3CoreRead:
			writeV3TestEnvelope(w, map[string]any{
				"version":    3,
				"content":    coreContent,
				"created_at": scenarioCreatedAt,
				"updated_at": scenarioUpdatedAt,
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := newGatewayClient(Options{
		GatewayURL: server.URL,
		APIKey:     "test-key",
		Timeout:    time.Second,
		identity: &serviceIdentity{
			serviceID: "service-1",
			teamID:    "team-1",
			agentID:   "agent-1",
		},
	})
	require.NoError(t, err)

	timestamp := time.Date(2026, 8, 13, 8, 30, 0, 0, time.UTC)
	captured, err := client.capture(context.Background(), captureRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Messages: []tdaiMessage{{
			ID:        "m1",
			Role:      "user",
			Content:   "remember the boundary",
			Timestamp: timestamp.UnixMilli(),
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, captured.L0Recorded)
	assert.False(t, captured.SchedulerNotified)
	mu.Lock()
	assert.Equal(t, "team-1", addReq.TeamID)
	assert.Equal(t, "agent-1", addReq.AgentID)
	assert.Equal(t, "user-1", addReq.UserID)
	assert.Equal(t, "session-1", addReq.SessionID)
	require.Len(t, addReq.Messages, 1)
	assert.Equal(t, timestamp.Format(time.RFC3339Nano), addReq.Messages[0].Timestamp)
	mu.Unlock()

	memories, err := client.searchMemories(context.Background(), searchMemoriesRequest{
		Query:  "database",
		Limit:  4,
		Type:   "preference",
		UserID: "user-1",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, memories.Total)
	assert.Equal(t, "v3-atomic", memories.Strategy)
	assert.Contains(t, memories.Results, "PostgreSQL")

	conversations, err := client.searchConversations(context.Background(), searchConversationsRequest{
		Query:     "transaction",
		Limit:     3,
		SessionID: "session-1",
		UserID:    "user-1",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, conversations.Total)
	assert.Contains(t, conversations.Results, "transaction boundary")

	recalled, err := client.recall(context.Background(), recallRequest{
		Query:  "how should reviews work",
		UserID: "user-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "v3-identity-scoped", recalled.Strategy)
	assert.Equal(t, 3, recalled.MemoryCount)
	assert.Contains(t, recalled.PrependContext, "PostgreSQL")
	assert.NotContains(t, recalled.AppendSystemContext, "PostgreSQL")
	assert.Contains(t, recalled.AppendSystemContext, coreContent)
	assert.Contains(t, recalled.AppendSystemContext, "<agent-core>")
	assert.NotContains(t, recalled.AppendSystemContext, "<user-core>")
	assert.Contains(t, recalled.AppendSystemContext, "reviews.md")

	scenario, err := client.readScenarioV3(
		context.Background(),
		"user-1",
		" reviews.md ",
	)
	require.NoError(t, err)
	assert.Equal(t, "reviews.md", scenario.Path)
	assert.Equal(t, v3Version("2"), scenario.Version)
	assert.Equal(t, scenarioContent, scenario.Content)
	assert.Equal(t, scenarioCreatedAt, scenario.CreatedAt)
	assert.Equal(t, scenarioUpdatedAt, scenario.UpdatedAt)

	mu.Lock()
	defer mu.Unlock()
	assert.NotContains(t, paths, pathEndSession)
	require.GreaterOrEqual(t, len(searches), 4)
	for _, isolation := range searches {
		assert.Equal(t, "team-1", isolation.TeamID)
		assert.Equal(t, "agent-1", isolation.AgentID)
		assert.Equal(t, "user-1", isolation.UserID)
	}
	assert.Equal(t, "session-1", searches[1].SessionID)
}

func TestGatewayClientV3Errors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(v3ResponseEnvelope[struct{}]{
			Code:      422,
			Message:   "invalid isolation",
			RequestID: "request-1",
			Data:      &struct{}{},
		})
	}))
	defer server.Close()
	client, err := newGatewayClient(Options{
		GatewayURL: server.URL,
		APIKey:     "test-key",
		identity: &serviceIdentity{
			serviceID: "service-1",
			teamID:    "team-1",
			agentID:   "agent-1",
		},
	})
	require.NoError(t, err)

	_, err = client.searchMemories(context.Background(), searchMemoriesRequest{Query: "q", UserID: "user-1"})
	require.ErrorContains(t, err, "code=422")
	require.ErrorContains(t, err, "request_id=request-1")

	_, err = client.searchMemories(context.Background(), searchMemoriesRequest{
		Query:  "q",
		Scene:  "project",
		UserID: "user-1",
	})
	require.ErrorContains(t, err, "scene filtering is not supported")
}

func TestServiceV3IngestAndEndSession(t *testing.T) {
	requests := make(chan v3ConversationAddRequest, 1)
	releaseCapture := make(chan struct{})
	var releaseOnce sync.Once
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.URL.Path != pathV3ConversationAdd {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		var req v3ConversationAddRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- req
		<-releaseCapture
		writeV3TestEnvelope(w, v3ConversationAddData{
			AcceptedIDs: []string{"m1", "m2"},
			TotalCount:  2,
		})
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithAPIKey("test-key"),
		WithServiceIdentity("service-1", "team-1", "agent-1"),
		WithIngestJobTimeout(time.Second),
	)
	require.NoError(t, err)
	defer func() {
		releaseOnce.Do(func() { close(releaseCapture) })
		require.NoError(t, svc.Close())
	}()
	sess := captureReadySession()
	require.NoError(t, svc.IngestSession(context.Background(), sess))
	select {
	case req := <-requests:
		assert.Equal(t, sess.ID, req.SessionID)
		assert.Equal(t, sess.UserID, req.UserID)
		require.Len(t, req.Messages, 2)
		events := sess.GetEvents()
		require.Len(t, events, 2)
		assert.Equal(t, formatV3Timestamp(events[0].Timestamp.UnixMilli()), req.Messages[0].Timestamp)
		assert.Equal(t, formatV3Timestamp(events[1].Timestamp.UnixMilli()), req.Messages[1].Timestamp)
	case <-time.After(time.Second):
		t.Fatal("v3 capture request was not received")
	}
	assert.Zero(t, readBestEffortSyntheticTimestamp(sess))

	endDone := make(chan error, 1)
	go func() {
		endDone <- svc.EndSession(context.Background(), sess)
	}()
	select {
	case err := <-endDone:
		t.Fatalf("EndSession completed before V3 capture: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseCapture) })
	select {
	case err := <-endDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("EndSession did not complete after V3 capture")
	}

	assert.Equal(t, int32(1), requestCount.Load(), "EndSession must not invent a remote v3 endpoint")
	assert.Zero(t, readBestEffortSyntheticTimestamp(sess))
}

func writeV3TestEnvelope[T any](w http.ResponseWriter, data T) {
	w.Header().Set(httpHeaderContentType, httpContentTypeJSON)
	_ = json.NewEncoder(w).Encode(v3ResponseEnvelope[T]{
		Code:      0,
		Message:   "ok",
		RequestID: "request-1",
		Data:      &data,
	})
}

func TestV3FormattingSkipsEmptyValues(t *testing.T) {
	assert.Equal(t, "- [memory] useful", formatV3AtomicItems([]v3AtomicSearchHit{
		{Content: "  "},
		{Content: "useful"},
	}))
	assert.Equal(t, "[message] useful", formatV3ConversationHits([]v3ConversationSearchHit{
		{Content: ""},
		{Content: "useful"},
	}))
	scenarios, scenarioCount := formatV3ScenarioEntries([]v3ScenarioEntry{
		{Path: " "},
		{Path: "path.md"},
		{Path: "folder/"},
		{Path: "a.md"},
		{Path: "b.md"},
		{Path: "c.md"},
	})
	assert.Equal(t, "- path.md\n- folder/\n- a.md\n- b.md\n- c.md", scenarios)
	assert.Equal(t, 5, scenarioCount)
	assert.Empty(t, formatV3Timestamp(0))
	assert.True(t, strings.Contains(formatV3Timestamp(1), "1970-01-01"))
}
