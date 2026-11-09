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
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
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

// TestToolTaskReconnectWaitHonorsDeadline holds a read lock as a polling
// call would. Both the reconnect owner and its waiter must be able to leave
// without closing the client or waiting for the poll to finish.
func TestToolTaskReconnectWaitHonorsDeadline(t *testing.T) {
	manager := newMCPSessionManager(ConnectionConfig{}, nil, nil)
	var closes atomic.Int32
	manager.client = &stubConnector{closeFn: func() error {
		closes.Add(1)
		return nil
	}}

	manager.mu.RLock()
	defer manager.mu.RUnlock()

	ownerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := make(chan error, 1)
	go func() { owner <- manager.recreateSession(ownerCtx) }()
	// Observe the owner holding the reconnect slot before exercising a waiter.
	require.Eventually(t, func() bool {
		manager.reconnectMu.Lock()
		defer manager.reconnectMu.Unlock()
		return manager.reconnect != nil
	}, time.Second, time.Millisecond)

	waiterCtx, stopWaiter := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer stopWaiter()
	waiter := make(chan error, 1)
	go func() { waiter <- manager.recreateSession(waiterCtx) }()
	select {
	case err := <-waiter:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("reconnect waiter stayed blocked past its deadline")
	}
	select {
	case err := <-owner:
		t.Fatalf("the waiter's deadline ended the owner's reconnect: %v", err)
	default:
	}

	cancel()
	select {
	case err := <-owner:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled reconnect owner waited for the active poll")
	}
	require.Zero(t, closes.Load())
}

type finalReconnectHandler struct {
	cancel  context.CancelFunc
	failure error
}

// Handle terminates the parent context during reconnect initialization, after
// the retry loop's initial context check has already passed.
func (h *finalReconnectHandler) Handle(ctx context.Context, _ *http.Client, _ *http.Request) (*http.Response, error) {
	if h.cancel != nil {
		h.cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, h.failure
}

type reconnectDeadlineContext struct {
	context.Context
	done chan struct{}
}

func (c *reconnectDeadlineContext) Done() <-chan struct{} { return c.done }

// Err exposes deadline expiry under explicit test control, avoiding timer races
// when exercising initialization failure in the last reconnect attempt.
func (c *reconnectDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// TestToolTaskFinalReconnectPreservesCauses keeps the context cause, task handle
// and reconnect failure together, while preserving active-context behavior.
func TestToolTaskFinalReconnectPreservesCauses(t *testing.T) {
	for _, mode := range []string{"cancelled", "deadline", "active"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			handler := &finalReconnectHandler{failure: errors.New("initialization failed")}
			var want error
			switch mode {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				handler.cancel, want = cancel, context.Canceled
			case "deadline":
				// Expire the context only after initialization starts, without a timer race.
				deadlineCtx := &reconnectDeadlineContext{Context: ctx, done: make(chan struct{})}
				ctx = deadlineCtx
				handler.cancel = func() { close(deadlineCtx.done) }
				want = context.DeadlineExceeded
			}
			manager := newMCPSessionManager(ConnectionConfig{Transport: "streamable_http", ServerURL: "http://localhost/unused"},
				[]mcp.ClientOption{mcp.WithClientGetSSEEnabled(false), mcp.WithHTTPReqHandler(handler)},
				&SessionReconnectConfig{EnableAutoReconnect: true, MaxReconnectAttempts: 1})
			original := &interruptedTask{id: "task-1"}
			err := manager.executeWithSessionReconnect(ctx, func() error { return original })
			require.ErrorIs(t, err, original)
			var taskErr toolTaskFailure
			require.ErrorAs(t, err, &taskErr)
			require.Equal(t, "task-1", taskErr.TaskID())
			require.False(t, taskErr.ToolCallReplaySafe())
			if want != nil {
				require.ErrorIs(t, err, want)
				require.ErrorIs(t, err, mcp.ErrHTTPRequestFailed)
			} else {
				require.Same(t, original, err)
			}
		})
	}
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

// TestToolTaskDeadlineAcrossReconnect preserves task-wide deadlines through
// initialization while retaining per-attempt timeouts for ordinary clients.
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
			if mode == "legacy" {
				manager.setClient(client)
			} else {
				manager.setClient(&taskConfiguredConnector{stubConnector: client, enabled: mode == "enabled"})
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

// Match the released transport wording without using the new SDK's types.
type missingTaskSession struct{ interruptedTask }

func (e *missingTaskSession) Error() string { return "server returned status code 404" }

func TestToolTaskMissingSessionReconnect(t *testing.T) {
	manager := newMCPSessionManager(ConnectionConfig{}, nil, &SessionReconnectConfig{EnableAutoReconnect: true, MaxReconnectAttempts: 2})
	require.True(t, manager.shouldAttemptSessionReconnect(&missingTaskSession{interruptedTask{id: "task-1"}}))
	require.False(t, manager.shouldAttemptSessionReconnect(&missingTaskSession{}))
	require.False(t, manager.shouldAttemptSessionReconnect(errors.New("server returned status code 404")))
}
