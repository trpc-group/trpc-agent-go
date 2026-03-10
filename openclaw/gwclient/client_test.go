//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gwclient

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

type stubRunner struct {
	mu        sync.Mutex
	callCount int
}

type staticRunner struct {
	events []*event.Event
	err    error
}

func (r *stubRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	r.callCount++
	r.mu.Unlock()

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("ok")},
			},
			Done: true,
		},
		RequestID: "req-1",
	}
	close(ch)
	return ch, nil
}

func (r *stubRunner) Close() error {
	return nil
}

func (r *staticRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	if r.err != nil {
		return nil, r.err
	}
	ch := make(chan *event.Event, len(r.events))
	for _, evt := range r.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (r *staticRunner) Close() error {
	return nil
}

func TestClient_SendMessage_Success(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&stubRunner{})
	require.NoError(t, err)

	cli, err := New(srv.Handler(), srv.MessagesPath(), srv.CancelPath())
	require.NoError(t, err)

	rsp, err := cli.SendMessage(context.Background(), MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.NoError(t, err)
	require.Equal(t, 200, rsp.StatusCode)
	require.Equal(t, "ok", rsp.Reply)
	require.Equal(t, "req-1", rsp.RequestID)
}

func TestClient_SendMessage_MentionGating(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(
		&stubRunner{},
		gateway.WithRequireMentionInThreads(true),
		gateway.WithMentionPatterns("@bot"),
	)
	require.NoError(t, err)

	cli, err := New(srv.Handler(), srv.MessagesPath(), srv.CancelPath())
	require.NoError(t, err)

	rsp, err := cli.SendMessage(context.Background(), MessageRequest{
		From:   "u1",
		Thread: "g1",
		Text:   "hello",
	})
	require.NoError(t, err)
	require.Equal(t, 200, rsp.StatusCode)
	require.True(t, rsp.Ignored)
}

func TestClient_SendMessage_Forbidden(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(
		&stubRunner{},
		gateway.WithAllowUsers("telegram:u1"),
	)
	require.NoError(t, err)

	cli, err := New(srv.Handler(), srv.MessagesPath(), srv.CancelPath())
	require.NoError(t, err)

	_, err = cli.SendMessage(context.Background(), MessageRequest{
		From:   "u1",
		UserID: "telegram:u2",
		Text:   "hello",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unauthorized")
}

func TestClient_SendMessage_InvalidJSONResponse(t *testing.T) {
	t.Parallel()

	cli, err := New(&invalidJSONHandler{}, "/v1/gateway/messages", "")
	require.NoError(t, err)

	_, err = cli.SendMessage(context.Background(), MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Error(t, err)
}

type invalidJSONHandler struct{}

func (h *invalidJSONHandler) ServeHTTP(
	w http.ResponseWriter,
	_ *http.Request,
) {
	w.WriteHeader(200)
	_, _ = w.Write([]byte("{not json"))
}

func TestClient_SendMessage_MarshalError(t *testing.T) {
	t.Parallel()

	cli, err := New(&invalidJSONHandler{}, "/v1/gateway/messages", "")
	require.NoError(t, err)

	_, err = cli.SendMessage(context.Background(), MessageRequest{
		Text: string([]byte{0xff}),
	})
	require.Error(t, err)
}

func TestMessageResponse_JSON(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(MessageResponse{
		SessionID:  "s1",
		RequestID:  "r1",
		Reply:      "ok",
		Ignored:    true,
		Error:      &APIError{Type: "t", Message: "m"},
		StatusCode: 200,
	})
	require.NoError(t, err)
	require.NotContains(t, string(b), "StatusCode")
}

func TestNew_ValidationErrors(t *testing.T) {
	t.Parallel()

	_, err := New(nil, "/v1/gateway/messages", "/v1/gateway/cancel")
	require.Error(t, err)

	_, err = New(http.NewServeMux(), "", "/v1/gateway/cancel")
	require.Error(t, err)
}

type statusHandler struct {
	code int
	body string
}

func (h *statusHandler) ServeHTTP(
	w http.ResponseWriter,
	_ *http.Request,
) {
	w.WriteHeader(h.code)
	_, _ = w.Write([]byte(h.body))
}

func TestClient_SendMessage_StatusError_NoPayload(t *testing.T) {
	t.Parallel()

	cli, err := New(
		&statusHandler{
			code: http.StatusInternalServerError,
			body: "{}",
		},
		"/v1/gateway/messages",
		"/v1/gateway/cancel",
	)
	require.NoError(t, err)

	_, err = cli.SendMessage(context.Background(), MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "status 500")
}

func TestClient_SendMessage_StatusError_WithPayload(t *testing.T) {
	t.Parallel()

	cli, err := New(
		&statusHandler{
			code: http.StatusForbidden,
			body: "{\"error\":{\"type\":\"unauthorized\",\"message\":\"no\"}}",
		},
		"/v1/gateway/messages",
		"/v1/gateway/cancel",
	)
	require.NoError(t, err)

	_, err = cli.SendMessage(context.Background(), MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unauthorized")
}

func TestClient_SendMessage_NewRequestError(t *testing.T) {
	t.Parallel()

	cli, err := New(http.NewServeMux(), "http://[::1", "/v1/gateway/cancel")
	require.NoError(t, err)

	_, err = cli.SendMessage(context.Background(), MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Error(t, err)
}

func TestClient_StreamMessage_Success(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&staticRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletionChunk,
					Choices: []model.Choice{
						{
							Delta: model.Message{Content: "hi"},
						},
					},
				},
				RequestID: "req-1",
			},
		},
	})
	require.NoError(t, err)

	cli, err := New(srv.Handler(), srv.MessagesPath(), srv.CancelPath())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	defer cancel()

	stream, err := cli.StreamMessage(ctx, MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.NoError(t, err)

	events := collectClientStreamEvents(t, stream)
	require.Len(t, events, 4)
	require.Equal(t, StreamEvent{
		Type:      gwproto.StreamEventTypeRunStarted,
		SessionID: "http:dm:u1",
	}, events[0])
	require.Equal(
		t,
		gwproto.StreamEventTypeMessageDelta,
		events[1].Type,
	)
	require.Equal(t, "hi", events[1].Delta)
	require.Equal(
		t,
		gwproto.StreamEventTypeMessageCompleted,
		events[2].Type,
	)
	require.Equal(t, "hi", events[2].Reply)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunCompleted,
		events[3].Type,
	)
	require.Equal(t, "req-1", events[3].RequestID)
}

func TestClient_StreamMessage_StatusError(t *testing.T) {
	t.Parallel()

	cli, err := New(
		&statusHandler{
			code: http.StatusForbidden,
			body: "{\"error\":{\"type\":\"unauthorized\",\"message\":\"no\"}}",
		},
		"/v1/gateway/messages",
		"/v1/gateway/cancel",
	)
	require.NoError(t, err)

	_, err = cli.StreamMessage(context.Background(), MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unauthorized")
}

type managedRunnerStub struct {
	stubRunner
}

func (m *managedRunnerStub) Cancel(requestID string) bool {
	return requestID == "req-1"
}

func (m *managedRunnerStub) RunStatus(
	requestID string,
) (runner.RunStatus, bool) {
	if requestID != "req-1" {
		return runner.RunStatus{}, false
	}
	return runner.RunStatus{RequestID: "req-1"}, true
}

func TestClient_Cancel_Success(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&managedRunnerStub{})
	require.NoError(t, err)

	cli, err := New(srv.Handler(), srv.MessagesPath(), srv.CancelPath())
	require.NoError(t, err)

	canceled, err := cli.Cancel(context.Background(), "req-1")
	require.NoError(t, err)
	require.True(t, canceled)

	canceled, err = cli.Cancel(context.Background(), "req-2")
	require.NoError(t, err)
	require.False(t, canceled)
}

func TestClient_Cancel_ValidationErrors(t *testing.T) {
	t.Parallel()

	cli, err := New(http.NewServeMux(), "/v1/gateway/messages", "")
	require.NoError(t, err)

	_, err = cli.Cancel(context.Background(), "req-1")
	require.Error(t, err)

	cli, err = New(
		http.NewServeMux(),
		"/v1/gateway/messages",
		"/v1/gateway/cancel",
	)
	require.NoError(t, err)

	_, err = cli.Cancel(context.Background(), "")
	require.Error(t, err)
}

func collectClientStreamEvents(
	t *testing.T,
	stream <-chan StreamEvent,
) []StreamEvent {
	t.Helper()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()

	var events []StreamEvent
	for {
		select {
		case evt, ok := <-stream:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-timer.C:
			t.Fatal("timeout waiting for client stream")
		}
	}
}
