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
	"strings"
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
	assert.Equal(t, "Bearer secret-key", healthAuth)

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
	require.Error(t, client.doJSONOnce(context.Background(), httpMethodGet, "://bad", nil, nil), "expected request build error")
}
