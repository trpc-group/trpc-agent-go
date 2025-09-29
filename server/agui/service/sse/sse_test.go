//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

func TestHandleRunnerNotConfigured(t *testing.T) {
	srv := &sse{}
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader("{}"))
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
	assert.Contains(t, rr.Body.String(), "runner not configured")
}

func TestHandleInvalidJSON(t *testing.T) {
	runner := &stubRunner{}
	srv := &sse{runner: runner, writer: aguisse.NewSSEWriter()}
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader("{invalid"))
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assert.Equal(t, 0, runner.calls)
}

func TestHandleRunnerError(t *testing.T) {
	runner := &stubRunner{
		runFn: func(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
			return nil, errors.New("boom")
		},
	}
	srv := &sse{runner: runner, writer: aguisse.NewSSEWriter()}
	payload := `{"threadId":"thread","runId":"run","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
	assert.Equal(t, 1, runner.calls)
}

func TestHandleSuccess(t *testing.T) {
	eventsCh := make(chan aguievents.Event)
	go func() {
		defer close(eventsCh)
		eventsCh <- aguievents.NewRunStartedEvent("thread", "run")
		eventsCh <- aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant"))
	}()

	runner := &stubRunner{
		runFn: func(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
			assert.Equal(t, "thread", input.ThreadID)
			assert.Equal(t, "run", input.RunID)
			return eventsCh, nil
		},
	}

	srv := &sse{runner: runner, writer: aguisse.NewSSEWriter()}
	payload := `{"threadId":"thread","runId":"run","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", res.Header.Get("Cache-Control"))
	assert.Equal(t, "keep-alive", res.Header.Get("Connection"))
	assert.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
	body := rr.Body.String()
	assert.Contains(t, body, `"type":"RUN_STARTED"`)
	assert.Contains(t, body, `"type":"TEXT_MESSAGE_START"`)
	assert.Equal(t, 1, runner.calls)
}

func TestHandleMethodNotAllowed(t *testing.T) {
	runner := &stubRunner{}
	srv := &sse{runner: runner, writer: aguisse.NewSSEWriter()}
	req := httptest.NewRequest(http.MethodGet, "/agui", nil)
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, res.StatusCode)
	assert.Equal(t, http.MethodPost, res.Header.Get("Allow"))
	assert.Equal(t, 0, runner.calls)
}

func TestHandlerDispatchesToConfiguredPath(t *testing.T) {
	eventsCh := make(chan aguievents.Event)
	go func() {
		defer close(eventsCh)
	}()

	runner := &stubRunner{
		runFn: func(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
			return eventsCh, nil
		},
	}

	svc := New(runner, service.WithPath("/custom"))
	handler := svc.Handler()
	assert.NotNil(t, handler)

	payload := `{"threadId":"thread","runId":"run","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/custom", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, 1, runner.calls)
}

func TestNewUsesDefaultPath(t *testing.T) {
	eventsCh := make(chan aguievents.Event)
	go func() {
		close(eventsCh)
	}()

	runner := &stubRunner{
		runFn: func(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
			return eventsCh, nil
		},
	}

	svc := New(runner)
	handler := svc.Handler()
	assert.NotNil(t, handler)

	payload := `{"threadId":"thread","runId":"run","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, 1, runner.calls)
}

func TestHandleCORSPreflight(t *testing.T) {
	srv := &sse{writer: aguisse.NewSSEWriter()}
	req := httptest.NewRequest(http.MethodOptions, "/agui", nil)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization")
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "POST", res.Header.Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type, Authorization", res.Header.Get("Access-Control-Allow-Headers"))
}

type stubRunner struct {
	runFn     func(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error)
	calls     int
	lastInput *adapter.RunAgentInput
}

func (s *stubRunner) Run(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	s.calls++
	s.lastInput = input
	if s.runFn != nil {
		return s.runFn(ctx, input)
	}
	return nil, nil
}
