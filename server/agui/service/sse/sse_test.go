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
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

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

func TestHandleRunnerNotConfigured(t *testing.T) {
	srv := &sse{}
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader("{}"))
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 status, got %d", res.StatusCode)
	}
	if !strings.Contains(rr.Body.String(), "runner not configured") {
		t.Fatalf("expected runner not configured message, got %q", rr.Body.String())
	}
}

func TestHandleInvalidJSON(t *testing.T) {
	runner := &stubRunner{}
	srv := &sse{runner: runner, writer: aguisse.NewSSEWriter()}
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader("{invalid"))
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 status, got %d", res.StatusCode)
	}
	if runner.calls != 0 {
		t.Fatalf("runner must not be invoked on invalid payload")
	}
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

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 status, got %d", res.StatusCode)
	}
	if runner.calls != 1 {
		t.Fatalf("runner should be invoked once")
	}
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
			if input.ThreadID != "thread" || input.RunID != "run" {
				t.Fatalf("unexpected input: %+v", input)
			}
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

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", res.StatusCode)
	}
	if res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected content type: %s", res.Header.Get("Content-Type"))
	}
	if res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("unexpected cache control header: %s", res.Header.Get("Cache-Control"))
	}
	if res.Header.Get("Connection") != "keep-alive" {
		t.Fatalf("unexpected connection header: %s", res.Header.Get("Connection"))
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("unexpected CORS header: %s", res.Header.Get("Access-Control-Allow-Origin"))
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"type":"RUN_STARTED"`) {
		t.Fatalf("expected RUN_STARTED event in body, got %q", body)
	}
	if !strings.Contains(body, `"type":"TEXT_MESSAGE_START"`) {
		t.Fatalf("expected TEXT_MESSAGE_START event in body, got %q", body)
	}
	if runner.calls != 1 {
		t.Fatalf("runner should be invoked once, got %d", runner.calls)
	}
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
	if handler == nil {
		t.Fatalf("Handler returned nil")
	}

	payload := `{"threadId":"thread","runId":"run","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/custom", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", res.StatusCode)
	}
	if runner.calls != 1 {
		t.Fatalf("runner should be invoked once, got %d", runner.calls)
	}
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
	if handler == nil {
		t.Fatalf("Handler returned nil")
	}

	payload := `{"threadId":"thread","runId":"run","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, defaultPath, strings.NewReader(payload))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", res.StatusCode)
	}
	if runner.calls != 1 {
		t.Fatalf("runner should be invoked once, got %d", runner.calls)
	}
}

func TestHandleCORSPreflight(t *testing.T) {
	srv := &sse{writer: aguisse.NewSSEWriter()}
	req := httptest.NewRequest(http.MethodOptions, "/agui", nil)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization")
	rr := httptest.NewRecorder()

	srv.handle(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 status, got %d", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("unexpected Access-Control-Allow-Origin: %s", res.Header.Get("Access-Control-Allow-Origin"))
	}
	if res.Header.Get("Access-Control-Allow-Methods") != "POST" {
		t.Fatalf("unexpected Access-Control-Allow-Methods: %s", res.Header.Get("Access-Control-Allow-Methods"))
	}
	if res.Header.Get("Access-Control-Allow-Headers") != "Content-Type, Authorization" {
		t.Fatalf("unexpected Access-Control-Allow-Headers: %s", res.Header.Get("Access-Control-Allow-Headers"))
	}
}
