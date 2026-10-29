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
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// This integration fixture requires the companion SDK Tasks branch. It is
// excluded until an upstream SDK version can replace the released dependency.
func TestTasksHTTPReconnect(t *testing.T) {
	for _, modern := range []bool{false, true} {
		name := "legacy"
		if modern {
			name = "stateless"
		}
		t.Run(name, func(t *testing.T) { testTasksHTTPReconnect(t, modern) })
	}
}

func testTasksHTTPReconnect(t *testing.T, modern bool) {
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
		if modern {
			require.Equal(t, "Bearer same-identity", r.Header.Get("Authorization"))
			require.Equal(t, req.Method, r.Header.Get("Mcp-Method"))
			require.Empty(t, r.Header.Get("Mcp-Session-Id"))
		}
		var result any
		switch req.Method {
		case "server/discover":
			require.True(t, modern)
			initializations.Add(1)
			result = map[string]any{"resultType": "complete", "supportedVersions": []string{mcp.ProtocolVersion_2026_07_28}, "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"resultType": "complete", "tools": []any{map[string]any{"name": "charge", "inputSchema": map[string]any{"type": "object"}}}}
		case "initialize":
			require.False(t, modern)
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
				if modern {
					conn, _, err := w.(http.Hijacker).Hijack()
					require.NoError(t, err)
					require.NoError(t, conn.Close())
					return
				}
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
	options := []mcp.ClientOption{mcp.WithTasks(true), mcp.WithClientGetSSEEnabled(false)}
	if modern {
		options = append(options, mcp.WithProtocolVersion(mcp.ProtocolVersion_2026_07_28), mcp.WithHTTPBeforeRequest(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer same-identity")
			return nil
		}))
	}
	manager := newMCPSessionManager(ConnectionConfig{Transport: "streamable_http", ServerURL: server.URL, Timeout: time.Second}, options, &SessionReconnectConfig{EnableAutoReconnect: true, MaxReconnectAttempts: 2})
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

// TestTasksMCPKitToolSet exercises the public ToolSet and CallableTool path
// against the pinned independent server documented by the SDK's TASKS.md.
func TestTasksMCPKitToolSet(t *testing.T) {
	endpoint := os.Getenv("MCP_TASKS_INTEROP_URL")
	if endpoint == "" {
		t.Skip("set MCP_TASKS_INTEROP_URL to the mcpkit stateless endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	set := NewMCPToolSet(ConnectionConfig{Transport: "streamable_http", ServerURL: endpoint, Timeout: 10 * time.Second},
		WithMCPOptions(mcp.WithProtocolVersion(mcp.ProtocolVersion_2026_07_28), mcp.WithTasks(true), mcp.WithSendEmptyToolArguments(true)), WithSessionReconnect(1))
	defer set.Close()
	require.NoError(t, set.Init(ctx))
	available := make(map[string]tool.CallableTool)
	for _, item := range set.Tools(ctx) {
		available[item.Declaration().Name] = item.(tool.CallableTool)
	}
	for _, tc := range []struct {
		name, args string
		isError    bool
	}{
		{"greet", `{"name":"agent-interop"}`, false},
		{"slow_compute", `{"seconds":1}`, false},
		{"failing_job", `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callable, ok := available[tc.name]
			require.True(t, ok)
			output, err := callable.Call(ctx, []byte(tc.args))
			require.NoError(t, err)
			result, ok := output.(*mcpToolResult)
			require.True(t, ok)
			require.NotEmpty(t, result.Content)
			require.Equal(t, tc.isError, result.IsError)
		})
	}
}
