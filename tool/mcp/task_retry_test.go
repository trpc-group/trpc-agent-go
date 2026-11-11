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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolretry"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// TestTasksFrameworkRetryDoesNotReplay submits once when a tool response is lost
// or polling a known task exhausts recovery, even with outer retries enabled.
func TestTasksFrameworkRetryDoesNotReplay(t *testing.T) {
	for _, tc := range []struct {
		name                string
		taskID              string
		wantPolls           int32
		wantInits           int32
		closeAfterReconnect bool
	}{
		{name: "submission response lost", wantInits: 1},
		{name: "task recovery exhausted", taskID: "charged-once", wantPolls: 2, wantInits: 2},
		{name: "session closed before resume", taskID: "charged-once", wantPolls: 1, wantInits: 2, closeAfterReconnect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, polls, discoveries atomic.Int32
			rediscovering := make(chan struct{})
			finishDiscovery := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var req struct {
					ID     any            `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var result any
				switch req.Method {
				case "server/discover":
					if discoveries.Add(1) == 2 && tc.closeAfterReconnect {
						close(rediscovering)
						select {
						case <-finishDiscovery:
						case <-r.Context().Done():
							return
						}
					}
					result = map[string]any{
						"resultType": "complete", "capabilities": map[string]any{},
						"supportedVersions": []string{mcp.ProtocolVersion_2026_07_28},
					}
				case "tools/list":
					result = map[string]any{"resultType": "complete", "tools": []any{map[string]any{
						"name": "charge", "inputSchema": map[string]any{"type": "object"},
					}}}
				case "tools/call":
					// Count the committed side effect before losing the response.
					if calls.Add(1) > 1 {
						result = map[string]any{"resultType": "complete", "content": []any{map[string]any{
							"type": "text", "text": "charged again",
						}}}
						break
					}
					if tc.taskID == "" {
						writeTruncatedMCPTaskResponse(w)
						return
					}
					result = map[string]any{
						"resultType": "task", "taskId": tc.taskID,
						"status": "working", "pollIntervalMs": 0,
					}
				case "tasks/get":
					polls.Add(1)
					if req.Params["taskId"] != tc.taskID {
						t.Errorf("taskId = %v, want %q", req.Params["taskId"], tc.taskID)
					}
					writeTruncatedMCPTaskResponse(w)
					return
				default:
					t.Errorf("unexpected method %s", req.Method)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID, "result": result,
				}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			set := NewMCPToolSet(
				ConnectionConfig{Transport: "streamable_http", ServerURL: server.URL, Timeout: time.Second},
				WithMCPOptions(mcp.WithProtocolVersion(mcp.ProtocolVersion_2026_07_28),
					mcp.WithTasks(true), mcp.WithClientGetSSEEnabled(false)),
				WithSessionReconnect(1),
			)
			defer func() { require.NoError(t, set.Close()) }()
			require.NoError(t, set.Init(ctx))
			tools := set.Tools(ctx)
			require.Len(t, tools, 1)
			callable, ok := tools[0].(tool.CallableTool)
			require.True(t, ok)
			policy := &tool.RetryPolicy{MaxAttempts: 2}
			closed := make(chan error, 1)
			if tc.closeAfterReconnect {
				policy.RetryOn = func(context.Context, *tool.RetryInfo) (bool, error) { return true, nil }
				go func() {
					select {
					case <-rediscovering:
					case <-ctx.Done():
						closed <- ctx.Err()
						return
					}
					// Hold reconnect completion until Close clears the initialized client.
					set.sessionManager.reconnectMu.Lock()
					defer set.sessionManager.reconnectMu.Unlock()
					close(finishDiscovery)
					closed <- set.Close()
				}()
			}
			// Exercise HTTP faults through the shared runner; caller-specific tests
			// separately verify LLMAgent and Graph error propagation.
			outcome := toolretry.Execute(ctx, toolretry.ExecuteInput{
				ToolName: "charge", ToolCallID: "one-logical-call", Arguments: []byte(`{}`),
				Policy: policy, Call: callable.Call,
			})
			if tc.closeAfterReconnect {
				require.NoError(t, <-closed)
			}
			result, err := outcome.Result, outcome.Error
			require.EqualValues(t, 1, calls.Load(), "the outer retry must not replay tools/call")
			require.Equal(t, tc.wantPolls, polls.Load())
			require.Equal(t, tc.wantInits, discoveries.Load())
			require.Nil(t, result)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			if tc.closeAfterReconnect {
				require.ErrorIs(t, err, context.Canceled)
			}
			var taskErr *mcp.TaskError
			require.ErrorAs(t, err, &taskErr)
			require.Equal(t, tc.taskID, taskErr.TaskID())
			require.False(t, taskErr.ToolCallReplaySafe())
			_, stopped := agent.AsStopError(err)
			require.False(t, stopped, "a task failure must not stop the entire agent")
		})
	}
}

func writeTruncatedMCPTaskResponse(w http.ResponseWriter) {
	// A short body with a longer declared length yields io.ErrUnexpectedEOF,
	// which the framework's default tool retry policy normally retries.
	w.Header().Set("Content-Length", "1024")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0"`))
}
