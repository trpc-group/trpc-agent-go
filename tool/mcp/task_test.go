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
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

type interruptedTask struct{ id string }

func (e *interruptedTask) Error() string            { return "session_expired: interrupted" }
func (e *interruptedTask) TaskID() string           { return e.id }
func (e *interruptedTask) ToolCallReplaySafe() bool { return false }

type taskClient struct {
	mcp.Connector
	calls, resumes int
	fail           error
}

func (c *taskClient) CallTool(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c.calls++
	return nil, c.fail
}
func (c *taskClient) ResumeToolTask(_ context.Context, id string) (*mcp.CallToolResult, error) {
	c.resumes++
	if id != "task-1" {
		return nil, errors.New("wrong handle")
	}
	return mcp.NewTextResult("finished"), c.fail
}

func TestToolTaskReconnectDoesNotReplay(t *testing.T) {
	var attempt toolCallAttempt
	old := &taskClient{fail: fmt.Errorf("wrapped: %w", &interruptedTask{id: "task-1"})}
	_, err := attempt.call(context.Background(), old, &mcp.CallToolRequest{})
	require.Error(t, err)
	replacement := &taskClient{}
	result, err := attempt.call(context.Background(), replacement, &mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, old.calls)
	require.Equal(t, 0, replacement.calls)
	require.Equal(t, 1, replacement.resumes)
}
func TestToolTaskUnknownOutcomeDoesNotReplay(t *testing.T) {
	var attempt toolCallAttempt
	old := &taskClient{fail: &interruptedTask{}}
	_, err := attempt.call(context.Background(), old, &mcp.CallToolRequest{})
	require.Error(t, err)
	replacement := &taskClient{}
	_, err = attempt.call(context.Background(), replacement, &mcp.CallToolRequest{})
	require.Error(t, err)
	require.Zero(t, replacement.calls)
	require.Zero(t, replacement.resumes)
}
func TestToolTaskUnsupportedResumerDoesNotReplay(t *testing.T) {
	attempt := toolCallAttempt{taskID: "task-1"}
	_, err := attempt.call(context.Background(), nil, &mcp.CallToolRequest{})
	require.ErrorContains(t, err, "does not support task resumption")
}
func TestToolTaskLegacyRetryUnchanged(t *testing.T) {
	var attempt toolCallAttempt
	c := &taskClient{fail: errors.New("session_expired: old sdk")}
	_, _ = attempt.call(context.Background(), c, &mcp.CallToolRequest{})
	_, _ = attempt.call(context.Background(), c, &mcp.CallToolRequest{})
	require.Equal(t, 2, c.calls)
}

func TestToolTaskReconnectPolicy(t *testing.T) {
	manager := newMCPSessionManager(ConnectionConfig{}, nil, &SessionReconnectConfig{EnableAutoReconnect: true, MaxReconnectAttempts: 2})
	require.False(t, manager.shouldAttemptSessionReconnect(fmt.Errorf("wrapped: %w", &interruptedTask{})))
	require.True(t, manager.shouldAttemptSessionReconnect(fmt.Errorf("wrapped: %w", &interruptedTask{id: "task-1"})))
}

func TestToolTaskCancelledReconnectRetainsHandle(t *testing.T) {
	manager := newMCPSessionManager(ConnectionConfig{}, nil, &SessionReconnectConfig{EnableAutoReconnect: true, MaxReconnectAttempts: 2})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := manager.executeWithSessionReconnect(ctx, func() error { return &interruptedTask{id: "task-1"} })
	require.ErrorIs(t, err, context.Canceled)
	var taskErr toolTaskFailure
	require.ErrorAs(t, err, &taskErr)
	require.Equal(t, "task-1", taskErr.TaskID())
}

type taskConfiguredConnector struct {
	*stubConnector
	enabled bool
}

func (c *taskConfiguredConnector) TasksEnabled() bool { return c.enabled }

type taskDeadlineHandler struct {
	deadline time.Time
	calls    int
}

func (h *taskDeadlineHandler) Handle(ctx context.Context, _ *http.Client, _ *http.Request) (*http.Response, error) {
	h.deadline, _ = ctx.Deadline()
	h.calls++
	return nil, errors.New("stop after observing reconnect initialization")
}

func TestToolTaskDeadlineAcrossReconnect(t *testing.T) {
	for _, mode := range []string{"legacy", "disabled", "enabled", "caller-deadline"} {
		t.Run(mode, func(t *testing.T) {
			var initialDeadline time.Time
			client := &stubConnector{callToolFn: func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				initialDeadline, _ = ctx.Deadline()
				return nil, &interruptedTask{id: "task-1"}
			}}
			handler := &taskDeadlineHandler{}
			manager := newMCPSessionManager(ConnectionConfig{
				Transport: "streamable_http", ServerURL: "http://localhost/unused", Timeout: time.Minute,
			}, []mcp.ClientOption{mcp.WithHTTPReqHandler(handler), mcp.WithClientGetSSEEnabled(false)},
				&SessionReconnectConfig{EnableAutoReconnect: true, MaxReconnectAttempts: 1})
			manager.client = client
			if mode != "legacy" {
				manager.client = &taskConfiguredConnector{stubConnector: client, enabled: mode == "enabled"}
			}
			ctx := context.Background()
			if mode == "caller-deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Minute)
				defer cancel()
			}
			_, err := manager.callTool(ctx, "export", nil)
			require.Error(t, err)
			require.Equal(t, 1, handler.calls)
			require.False(t, initialDeadline.IsZero())
			if mode == "enabled" || mode == "caller-deadline" {
				require.Equal(t, initialDeadline, handler.deadline)
			} else {
				require.True(t, handler.deadline.After(initialDeadline), "legacy reconnect should receive its own timeout")
			}
		})
	}
}
