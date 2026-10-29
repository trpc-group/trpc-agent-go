//go:build mcptasks

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// This integration fixture requires the companion SDK Tasks branch. It is
// excluded until an upstream SDK version can replace the released dependency.
func TestTasksHTTPReconnect(t *testing.T) {
	var calls, polls, initializations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var result any
		switch req.Method {
		case "initialize":
			initializations.Add(1)
			w.Header().Set("Mcp-Session-Id", "test-session")
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			calls.Add(1)
			result = map[string]any{"resultType": "task", "taskId": "charged-once", "status": "working", "pollIntervalMs": 0}
		case "tasks/get":
			require.Equal(t, "charged-once", req.Params["taskId"])
			require.Equal(t, "charged-once", r.Header.Get("Mcp-Name"))
			if polls.Add(1) == 1 {
				http.Error(w, "session expired", http.StatusNotFound)
				return
			}
			result = map[string]any{"resultType": "complete", "taskId": "charged-once", "status": "completed", "result": map[string]any{"content": []any{}, "structuredContent": map[string]any{"receipt": "one"}, "_meta": map[string]any{"trace": "kept"}}}
		default:
			t.Errorf("unexpected method %s", req.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	manager := newMCPSessionManager(ConnectionConfig{Transport: "streamable_http", ServerURL: server.URL, Timeout: time.Second}, []mcp.ClientOption{mcp.WithTasks(true), mcp.WithClientGetSSEEnabled(false)}, &SessionReconnectConfig{EnableAutoReconnect: true, MaxReconnectAttempts: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, manager.connect(ctx))
	defer manager.close()
	result, err := manager.callTool(ctx, "charge", nil)
	require.NoError(t, err)
	require.Equal(t, "kept", result.Meta["trace"])
	require.NotEmpty(t, result.Content)
	require.EqualValues(t, 1, calls.Load())
	require.EqualValues(t, 2, polls.Load())
	require.EqualValues(t, 2, initializations.Load())
}
