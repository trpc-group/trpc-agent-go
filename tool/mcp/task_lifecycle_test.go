//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
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
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func taskLifecycleServer(t *testing.T, onRequest func(http.ResponseWriter, *http.Request, string) bool) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if onRequest(w, r, req.Method) {
			return
		}
		result := map[string]any{"resultType": "complete"}
		switch req.Method {
		case "server/discover":
			result["supportedVersions"] = []string{mcp.ProtocolVersion_2026_07_28}
			result["capabilities"] = map[string]any{}
		case "tools/list":
			result["tools"] = []any{map[string]any{"name": "charge", "inputSchema": map[string]any{"type": "object"}}}
		case "tools/call":
			result = map[string]any{"resultType": "task", "taskId": "charge-1", "status": "working", "pollIntervalMs": 0}
		case "tasks/get":
			result["taskId"], result["status"] = "charge-1", "completed"
			result["result"] = map[string]any{"content": []any{}}
		default:
			t.Errorf("unexpected method %q", req.Method)
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func taskLifecycleSet(t *testing.T, ctx context.Context, config ConnectionConfig, options ...mcp.ClientOption) *ToolSet {
	t.Helper()
	config.Transport = "streamable_http"
	options = append([]mcp.ClientOption{
		mcp.WithTasks(true), mcp.WithProtocolVersion(mcp.ProtocolVersion_2026_07_28), mcp.WithClientGetSSEEnabled(false),
	}, options...)
	set := NewMCPToolSet(config, WithSessionReconnect(1), WithMCPOptions(options...))
	require.NoError(t, set.Init(ctx))
	t.Cleanup(func() { require.NoError(t, set.Close()) })
	return set
}

func awaitTaskLifecycle[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatalf("task lifecycle operation did not complete: %v", ctx.Err())
		var zero T
		return zero
	}
}

func TestTasksCloseCancelsActivePoll(t *testing.T) {
	var submissions atomic.Int32
	polling := make(chan struct{}, 1)
	url := taskLifecycleServer(t, func(_ http.ResponseWriter, r *http.Request, method string) bool {
		switch method {
		case "tools/call":
			submissions.Add(1)
		case "tasks/get":
			polling <- struct{}{}
			<-r.Context().Done()
			return true
		}
		return false
	})
	deadline, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	callCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set := taskLifecycleSet(t, deadline, ConnectionConfig{ServerURL: url})
	call := set.Tools(deadline)[0].(tool.CallableTool)
	callDone, closeDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := call.Call(callCtx, []byte("{}")); callDone <- err }()
	awaitTaskLifecycle(t, deadline, polling)
	go func() { closeDone <- set.Close() }()
	require.NoError(t, awaitTaskLifecycle(t, deadline, closeDone))
	err := awaitTaskLifecycle(t, deadline, callDone)
	require.ErrorIs(t, err, context.Canceled)
	var taskErr *mcp.TaskError
	require.ErrorAs(t, err, &taskErr)
	require.Equal(t, "charge-1", taskErr.TaskID())
	require.EqualValues(t, 1, submissions.Load())
}

func TestTasksReconnectRetainsStaticHeaders(t *testing.T) {
	var polls, changedIdentity, submissions atomic.Int32
	url := taskLifecycleServer(t, func(w http.ResponseWriter, r *http.Request, method string) bool {
		switch method {
		case "tools/call":
			submissions.Add(1)
		case "tasks/get":
			if r.Header.Get("Authorization") != "Bearer original" {
				changedIdentity.Add(1)
			}
			if polls.Add(1) == 1 {
				writeTruncatedMCPTaskResponse(w)
				return true
			}
		}
		return false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	headers := map[string]string{"Authorization": "Bearer original"}
	set := taskLifecycleSet(t, ctx, ConnectionConfig{ServerURL: url, Headers: headers},
		mcp.WithHTTPBeforeRequest(func(_ context.Context, req *http.Request) error {
			if req.GetBody == nil {
				return nil
			}
			body, err := req.GetBody()
			if err != nil {
				return err
			}
			defer body.Close()
			var request struct{ Method string }
			if err := json.NewDecoder(body).Decode(&request); err != nil {
				return err
			}
			if request.Method == "tasks/get" {
				headers["Authorization"] = "Bearer replacement"
			}
			return nil
		}))
	_, err := set.Tools(ctx)[0].(tool.CallableTool).Call(ctx, []byte("{}"))
	require.NoError(t, err)
	require.EqualValues(t, 1, submissions.Load())
	require.EqualValues(t, 2, polls.Load())
	require.Zero(t, changedIdentity.Load())
}

func TestTasksReconnectWaiterTakesOverAfterOwnerCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	var discoveries atomic.Int32
	url := taskLifecycleServer(t, func(_ http.ResponseWriter, r *http.Request, method string) bool {
		if method == "server/discover" && discoveries.Add(1) == 2 {
			started <- struct{}{}
			<-r.Context().Done()
			return true
		}
		return false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	set := taskLifecycleSet(t, ctx, ConnectionConfig{ServerURL: url})
	ownerCtx, stopOwner := context.WithCancel(ctx)
	defer stopOwner()
	ownerDone := make(chan error, 1)
	waiterDone := make(chan error, 2)
	go func() { ownerDone <- set.sessionManager.recreateSession(ownerCtx) }()
	awaitTaskLifecycle(t, ctx, started)
	for i := 0; i < cap(waiterDone); i++ {
		waiting := make(chan struct{}, 1)
		go func() {
			waiterDone <- set.sessionManager.recreateSession(reconnectWaitContext{ctx, waiting})
		}()
		awaitTaskLifecycle(t, ctx, waiting)
	}
	stopOwner()
	require.Error(t, awaitTaskLifecycle(t, ctx, ownerDone))
	for i := 0; i < cap(waiterDone); i++ {
		require.NoError(t, awaitTaskLifecycle(t, ctx, waiterDone))
	}
	require.EqualValues(t, 3, discoveries.Load())
}
