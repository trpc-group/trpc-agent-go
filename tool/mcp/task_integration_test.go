//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// TestTasksHTTPReconnect verifies task continuation using the released SDK.
func TestTasksHTTPReconnect(t *testing.T) {
	for _, modern := range []bool{false, true} {
		name := "legacy"
		if modern {
			name = "stateless"
		}
		t.Run(name, func(t *testing.T) { testTasksHTTPReconnect(t, modern, false) })
	}
	t.Run("terminal failure containing EOF", func(t *testing.T) {
		testTasksHTTPReconnect(t, true, true)
	})
}

// testTasksHTTPReconnect distinguishes a lost poll from a terminal application
// failure. A lost poll resumes the same handle through a replacement client.
func testTasksHTTPReconnect(t *testing.T, modern, terminalFailure bool) {
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
			poll := polls.Add(1)
			if terminalFailure {
				result = map[string]any{
					"resultType": "complete", "taskId": "charged-once", "status": "failed",
					"error": map[string]any{"code": -32603, "message": "worker input: unexpected EOF"},
				}
				break
			}
			if poll == 1 {
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
	if terminalFailure {
		var taskErr *mcp.TaskError
		require.ErrorAs(t, err, &taskErr)
		require.Equal(t, "failed", taskErr.Status)
		require.Equal(t, "charged-once", taskErr.TaskID())
		require.False(t, taskErr.ToolCallReplaySafe())
		require.EqualValues(t, 1, calls.Load())
		require.EqualValues(t, 1, polls.Load())
		require.EqualValues(t, 1, initializations.Load())
		return
	}
	require.NoError(t, err)
	require.Equal(t, "kept", result.Meta["trace"])
	require.NotEmpty(t, result.Content)
	require.EqualValues(t, 1, calls.Load())
	require.EqualValues(t, 2, polls.Load())
	require.EqualValues(t, 2, initializations.Load())
}

// TestTasksHTTPReconnectDeadline verifies that a lost poll can return its
// deadline and task handle while a concurrent SDK poll still uses the client.
func TestTasksHTTPReconnectDeadline(t *testing.T) {
	heldPoll, releaseHeld := make(chan struct{}), make(chan struct{})
	markHeld := sync.OnceFunc(func() { close(heldPoll) })
	release := sync.OnceFunc(func() { close(releaseHeld) })
	var discoveries, submissions, lostPolls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params struct {
				TaskID    string            `json:"taskId"`
				Arguments map[string]string `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		result := map[string]any{"resultType": "complete"}
		switch req.Method {
		case "server/discover":
			discoveries.Add(1)
			result["supportedVersions"] = []string{mcp.ProtocolVersion_2026_07_28}
			result["capabilities"] = map[string]any{}
		case "tools/list":
			result["tools"] = []any{map[string]any{"name": "charge", "inputSchema": map[string]any{"type": "object"}}}
		case "tools/call":
			submissions.Add(1)
			result = map[string]any{
				"resultType": "task", "taskId": req.Params.Arguments["id"],
				"status": "working", "pollIntervalMs": 0,
			}
		case "tasks/get":
			switch req.Params.TaskID {
			case "held":
				markHeld()
				select {
				case <-releaseHeld:
				case <-r.Context().Done():
					return
				}
			case "lost":
				lostPolls.Add(1)
				writeTruncatedMCPTaskResponse(w)
				return
			case "next":
			default:
				t.Errorf("unexpected task ID %q", req.Params.TaskID)
				http.Error(w, "unknown task", http.StatusBadRequest)
				return
			}
			result["taskId"] = req.Params.TaskID
			result["status"] = "completed"
			result["result"] = map[string]any{"content": []any{}}
		default:
			t.Errorf("unexpected method %q", req.Method)
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	set := NewMCPToolSet(ConnectionConfig{Transport: "streamable_http", ServerURL: server.URL},
		WithMCPOptions(mcp.WithTasks(true), mcp.WithProtocolVersion(mcp.ProtocolVersion_2026_07_28)),
		WithSessionReconnect(1))
	t.Cleanup(func() { require.NoError(t, set.Close()) })
	defer release()
	require.NoError(t, set.Init(ctx))
	tools := set.Tools(ctx)
	require.Len(t, tools, 1)
	callable, ok := tools[0].(tool.CallableTool)
	require.True(t, ok)
	call := func(ctx context.Context, args string) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := callable.Call(ctx, []byte(args))
			done <- err
		}()
		return done
	}

	held := call(ctx, `{"id":"held"}`)
	select {
	case <-heldPoll:
	case <-ctx.Done():
		t.Fatal("the first task never started polling")
	}
	lostCtx, stopLost := context.WithTimeout(ctx, time.Second)
	defer stopLost()
	lost := call(lostCtx, `{"id":"lost"}`)
	select {
	case err := <-lost:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		var taskErr *mcp.TaskError
		require.ErrorAs(t, err, &taskErr)
		require.Equal(t, "lost", taskErr.TaskID())
	case <-time.After(3 * time.Second):
		t.Fatal("the lost poll did not return while the first task was still polling")
	}
	select {
	case err := <-held:
		t.Fatalf("the reconnect interrupted the first task: %v", err)
	default:
	}
	release()
	require.NoError(t, <-held)
	_, err := callable.Call(ctx, []byte(`{"id":"next"}`))
	require.NoError(t, err)
	require.EqualValues(t, 1, discoveries.Load(), "the next call should reuse the existing client")
	require.EqualValues(t, 3, submissions.Load(), "each tool call must be submitted only once")
	require.EqualValues(t, 1, lostPolls.Load(), "the lost task must not be polled after expiry")
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
