//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type sseTestRunner struct {
	events <-chan *event.Event
	runCtx chan context.Context
}

func (r *sseTestRunner) Run(
	ctx context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	if r.runCtx != nil {
		r.runCtx <- ctx
	}
	return r.events, nil
}

func (r *sseTestRunner) Close() error {
	return nil
}

func TestServer_handleStreaming_KeepaliveDuringLongToolExecution(t *testing.T) {
	events := make(chan *event.Event)
	srv, err := New(
		WithRunner(&sseTestRunner{events: events}),
		WithHeartbeatInterval(5*time.Millisecond),
	)
	require.NoError(t, err)

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		httpSrv.URL+"/v1/chat/completions",
		streamingRequestBody(t, "long tool"),
	)
	require.NoError(t, err)
	req.Header.Set(headerContentType, contentTypeJSON)

	toolStarted := make(chan error, 1)
	go func() {
		select {
		case events <- &event.Event{
			ID: "evt-tool",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Delta: model.Message{
							ToolCalls: []model.ToolCall{
								{
									ID:   "call-1",
									Type: "function",
									Function: model.FunctionDefinitionParam{
										Name:      "slow_tool",
										Arguments: []byte(`{}`),
									},
								},
							},
						},
					},
				},
				Created: time.Now().Unix(),
			},
		}:
			toolStarted <- nil
		case <-ctx.Done():
			toolStarted <- ctx.Err()
		}
	}()

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, xAccelBufferingNo, resp.Header.Get(headerXAccelBuffering))
	require.NoError(t, <-toolStarted)

	reader := bufio.NewReader(resp.Body)
	var sawToolCall, sawHeartbeat bool
	for !sawHeartbeat {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		if strings.Contains(line, `"tool_calls"`) {
			sawToolCall = true
		}
		if strings.HasPrefix(line, ":") {
			sawHeartbeat = true
		}
	}
	require.True(t, sawToolCall, "expected the tool-call event before the heartbeat")

	sendResult := make(chan error, 1)
	go func() {
		finishReason := finishReasonStop
		select {
		case events <- &event.Event{
			ID: "evt-final",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Delta:        model.Message{Content: "done"},
						FinishReason: &finishReason,
					},
				},
				Done:    true,
				Created: time.Now().Unix(),
				Usage:   &model.Usage{TotalTokens: 1},
			},
		}:
			close(events)
			sendResult <- nil
		case <-ctx.Done():
			sendResult <- ctx.Err()
		}
	}()

	var sawDone bool
	for {
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if strings.HasPrefix(line, sseDataPrefix) && strings.Contains(line, sseDoneMarker) {
			sawDone = true
			break
		}
	}
	require.True(t, sawDone, "expected the final SSE [DONE] marker")
	require.NoError(t, <-sendResult)
}

func TestServer_handleStreaming_ClientDisconnectCancelsRun(t *testing.T) {
	events := make(chan *event.Event)
	runCtxCh := make(chan context.Context, 1)
	srv, err := New(
		WithRunner(&sseTestRunner{events: events, runCtx: runCtxCh}),
		WithHeartbeatInterval(5*time.Millisecond),
	)
	require.NoError(t, err)

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		httpSrv.URL+"/v1/chat/completions",
		streamingRequestBody(t, "disconnect"),
	)
	require.NoError(t, err)
	req.Header.Set(headerContentType, contentTypeJSON)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	runCtx := <-runCtxCh
	require.NoError(t, resp.Body.Close())
	select {
	case <-runCtx.Done():
		require.ErrorIs(t, runCtx.Err(), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("runner context was not canceled after the client disconnected")
	}
	close(events)
}

func TestServer_handleStreaming_SlowConsumerCancelsRunAndDrainsEvents(t *testing.T) {
	events := make(chan *event.Event)
	runCtxCh := make(chan context.Context, 1)
	srv, err := New(
		WithRunner(&sseTestRunner{events: events, runCtx: runCtxCh}),
		WithHeartbeatInterval(time.Hour),
	)
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		streamingRequestBody(t, "slow consumer"),
	)
	w := newDeadlineResponseWriter()
	handleDone := make(chan struct{})
	go func() {
		srv.handleChatCompletions(w, req)
		close(handleDone)
	}()

	sendErr := make(chan error, 1)
	go func() {
		select {
		case events <- &event.Event{
			ID: "evt-slow",
			Response: &model.Response{
				Choices: []model.Choice{
					{Delta: model.Message{Content: "first"}},
				},
				IsPartial: true,
				Created:   time.Now().Unix(),
			},
		}:
			sendErr <- nil
		case <-time.After(time.Second):
			sendErr <- errors.New("timed out sending the first event")
		}
	}()
	require.NoError(t, <-sendErr)

	select {
	case <-handleDone:
	case <-time.After(time.Second):
		t.Fatal("streaming handler did not stop after the slow consumer write failed")
	}

	runCtx := <-runCtxCh
	require.ErrorIs(t, runCtx.Err(), context.Canceled)

	secondSent := make(chan struct{})
	go func() {
		select {
		case events <- &event.Event{
			ID: "evt-after-write-error",
			Response: &model.Response{
				Choices: []model.Choice{
					{Delta: model.Message{Content: "second"}},
				},
				IsPartial: true,
				Created:   time.Now().Unix(),
			},
		}:
			close(secondSent)
		case <-time.After(time.Second):
		}
	}()
	select {
	case <-secondSent:
	case <-time.After(time.Second):
		t.Fatal("event producer remained blocked after the streaming handler exited")
	}
	close(events)
}

func TestServer_handleStreaming_FlushErrorCancelsRunAndDrainsEvents(t *testing.T) {
	events := make(chan *event.Event)
	runCtxCh := make(chan context.Context, 1)
	srv, err := New(
		WithRunner(&sseTestRunner{events: events, runCtx: runCtxCh}),
		WithHeartbeatInterval(time.Hour),
	)
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		streamingRequestBody(t, "flush failure"),
	)
	w := newFlushErrorResponseWriter(errors.New("flush failed"))
	handleDone := make(chan struct{})
	go func() {
		srv.handleChatCompletions(w, req)
		close(handleDone)
	}()

	runCtx := <-runCtxCh
	sendEvent(t, events, &event.Event{
		ID: "evt-flush-error",
		Response: &model.Response{
			Choices: []model.Choice{
				{Delta: model.Message{Content: "first"}},
			},
			IsPartial: true,
			Created:   time.Now().Unix(),
		},
	})

	select {
	case <-w.flushStarted:
	case <-time.After(time.Second):
		t.Fatal("flush was not attempted")
	}
	select {
	case <-handleDone:
	case <-time.After(time.Second):
		t.Fatal("streaming handler did not stop after the flush failed")
	}
	require.ErrorIs(t, runCtx.Err(), context.Canceled)

	drained := make(chan struct{})
	go func() {
		select {
		case events <- &event.Event{ID: "evt-drained"}:
			close(drained)
		case <-time.After(time.Second):
		}
	}()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("event producer remained blocked after the flush failure")
	}
	close(events)
}

func TestServer_handleStreaming_FinalEventOrder(t *testing.T) {
	finishReason := finishReasonStop
	events := make(chan *event.Event, 2)
	events <- &event.Event{
		ID: "evt-content",
		Response: &model.Response{
			Choices: []model.Choice{
				{Delta: model.Message{Content: "hello"}},
			},
			Created: time.Now().Unix(),
		},
	}
	events <- &event.Event{
		ID: "evt-final",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta:        model.Message{},
					FinishReason: &finishReason,
				},
			},
			Done:    true,
			Created: time.Now().Unix(),
			Usage:   &model.Usage{TotalTokens: 1},
		},
	}
	close(events)

	srv, err := New(WithRunner(&sseTestRunner{events: events}))
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		streamingRequestBody(t, "final order"),
	)
	w := httptest.NewRecorder()
	srv.handleChatCompletions(w, req)

	frames := strings.Split(strings.TrimSpace(w.Body.String()), "\n\n")
	require.NotEmpty(t, frames)
	require.Equal(t, sseDataPrefix+sseDoneMarker, frames[len(frames)-1])

	contentIndex := -1
	finishIndex := -1
	for i, frame := range frames {
		if strings.Contains(frame, `"content":"hello"`) {
			contentIndex = i
		}
		if strings.Contains(frame, `"finish_reason":"stop"`) {
			finishIndex = i
		}
	}
	require.NotEqual(t, -1, contentIndex, "expected a content chunk")
	require.NotEqual(t, -1, finishIndex, "expected a finish_reason chunk")
	require.Less(t, contentIndex, finishIndex, "content chunk must precede finish_reason")
	require.Less(t, finishIndex, len(frames)-1, "finish_reason must precede [DONE]")
}

func TestServer_streamEvents_ContextCancellation(t *testing.T) {
	srv, err := New(
		WithRunner(&sseTestRunner{events: make(chan *event.Event)}),
		WithHeartbeatInterval(time.Hour),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := newControlledResponseWriter()
	flusher := &mockFlusher{ResponseWriter: w}
	require.NoError(t, srv.streamEvents(
		ctx,
		w,
		flusher,
		make(chan *event.Event),
		"response-id",
		time.Now().Unix(),
	))
	require.Zero(t, w.writes)
	require.False(t, flusher.flushed)
}

func TestServer_streamEvents_HeartbeatWriteError(t *testing.T) {
	writeErr := errors.New("heartbeat write failed")
	w := newControlledResponseWriter()
	w.writeErr = writeErr
	w.writeErrAt = 1
	flusher := &mockFlusher{ResponseWriter: w}
	srv, err := New(
		WithRunner(&sseTestRunner{events: make(chan *event.Event)}),
		WithHeartbeatInterval(time.Millisecond),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = srv.streamEvents(
		ctx,
		w,
		flusher,
		make(chan *event.Event),
		"response-id",
		time.Now().Unix(),
	)
	require.ErrorIs(t, err, writeErr)
	require.ErrorContains(t, err, "write SSE heartbeat")
	require.Equal(t, 1, w.writes)
}

func TestServer_writeStreamingEvent_ErrorPropagation(t *testing.T) {
	writeErr := errors.New("stream write failed")
	tests := []struct {
		name       string
		writeErrAt int
		wantError  string
	}{
		{
			name:       "final chunk",
			writeErrAt: 2,
			wantError:  "write final streaming chunk",
		},
		{
			name:       "done marker",
			writeErrAt: 3,
			wantError:  "write SSE done marker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := New(WithAgent(&mockAgent{name: "test-agent"}))
			require.NoError(t, err)

			w := newControlledResponseWriter()
			w.writeErr = writeErr
			w.writeErrAt = tt.writeErrAt
			flusher := &mockFlusher{ResponseWriter: w}
			finishReason := finishReasonStop
			evt := &event.Event{
				ID: "evt-final",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Delta:        model.Message{Content: "done"},
							FinishReason: &finishReason,
						},
					},
					Done:    true,
					Created: time.Now().Unix(),
					Usage:   &model.Usage{TotalTokens: 1},
				},
			}

			_, err = srv.writeStreamingEvent(
				context.Background(),
				w,
				flusher,
				evt,
				"response-id",
				time.Now().Unix(),
			)
			require.ErrorIs(t, err, writeErr)
			require.ErrorContains(t, err, tt.wantError)
			require.Equal(t, tt.writeErrAt, w.writes)
		})
	}
}

func TestServer_writeSSE_SetWriteDeadlineError(t *testing.T) {
	deadlineErr := errors.New("deadline failed")
	w := newControlledResponseWriter()
	w.deadlineErr = deadlineErr
	flusher := &mockFlusher{ResponseWriter: w}
	srv, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)
	srv.writeTimeout = time.Second

	err = srv.writeSSE(w, flusher, []byte("data"))
	require.ErrorIs(t, err, deadlineErr)
	require.ErrorContains(t, err, "set SSE write deadline")
	require.Zero(t, w.writes)
	require.False(t, flusher.flushed)
}

func TestServer_writeSSE_ShortWrite(t *testing.T) {
	w := newControlledResponseWriter()
	w.shortWrite = true
	flusher := &mockFlusher{ResponseWriter: w}
	srv, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	err = srv.writeSSE(w, flusher, []byte("data"))
	require.ErrorIs(t, err, io.ErrShortWrite)
	require.Equal(t, 1, w.writes)
	require.False(t, flusher.flushed)
}

func streamingRequestBody(t *testing.T, content string) io.Reader {
	t.Helper()
	body, err := json.Marshal(openAIRequest{
		Model: defaultModelName,
		Messages: []openAIMessage{
			{Role: "user", Content: content},
		},
		Stream: true,
	})
	require.NoError(t, err)
	return bytes.NewReader(body)
}

type deadlineResponseWriter struct {
	header       http.Header
	writeStarted chan struct{}
	releaseWrite chan struct{}
	releaseOnce  sync.Once
}

func newDeadlineResponseWriter() *deadlineResponseWriter {
	return &deadlineResponseWriter{
		header:       make(http.Header),
		writeStarted: make(chan struct{}),
		releaseWrite: make(chan struct{}),
	}
}

func (w *deadlineResponseWriter) Header() http.Header {
	return w.header
}

func (w *deadlineResponseWriter) WriteHeader(int) {}

func (w *deadlineResponseWriter) Write([]byte) (int, error) {
	close(w.writeStarted)
	<-w.releaseWrite
	return 0, os.ErrDeadlineExceeded
}

func (w *deadlineResponseWriter) Flush() {}

func (w *deadlineResponseWriter) SetWriteDeadline(time.Time) error {
	w.releaseOnce.Do(func() {
		close(w.releaseWrite)
	})
	return nil
}

type flushErrorResponseWriter struct {
	header       http.Header
	err          error
	flushStarted chan struct{}
	flushOnce    sync.Once
}

func newFlushErrorResponseWriter(err error) *flushErrorResponseWriter {
	return &flushErrorResponseWriter{
		header:       make(http.Header),
		err:          err,
		flushStarted: make(chan struct{}),
	}
}

func (w *flushErrorResponseWriter) Header() http.Header {
	return w.header
}

func (w *flushErrorResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *flushErrorResponseWriter) WriteHeader(int) {}

func (w *flushErrorResponseWriter) Flush() {}

func (w *flushErrorResponseWriter) FlushError() error {
	w.flushOnce.Do(func() {
		close(w.flushStarted)
	})
	return w.err
}

type controlledResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	writes      int
	writeErr    error
	writeErrAt  int
	shortWrite  bool
	deadlineErr error
}

func newControlledResponseWriter() *controlledResponseWriter {
	return &controlledResponseWriter{header: make(http.Header)}
}

func (w *controlledResponseWriter) Header() http.Header {
	return w.header
}

func (w *controlledResponseWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writeErrAt > 0 && w.writes == w.writeErrAt {
		return 0, w.writeErr
	}
	if w.shortWrite {
		return len(p) - 1, nil
	}
	return w.body.Write(p)
}

func (w *controlledResponseWriter) WriteHeader(int) {}

func (w *controlledResponseWriter) SetWriteDeadline(time.Time) error {
	return w.deadlineErr
}

func sendEvent(t *testing.T, events chan<- *event.Event, evt *event.Event) {
	t.Helper()
	select {
	case events <- evt:
	case <-time.After(time.Second):
		t.Fatal("timed out sending event")
	}
}
