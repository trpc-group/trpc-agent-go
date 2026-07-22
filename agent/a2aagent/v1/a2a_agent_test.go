//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2aagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	protocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type testRunner struct {
	events []*event.Event
}

func (r *testRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event, len(r.events))
	for _, evt := range r.events {
		out <- evt
	}
	close(out)
	return out, nil
}

func (*testRunner) Close() error { return nil }

func TestRunNonStreamingRequestsNoTaskHistory(t *testing.T) {
	var received protocol.SendMessageParams
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll failed: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("request unmarshal failed: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method != protocol.MethodMessageSend {
			t.Errorf("method = %q, want %q", request.Method, protocol.MethodMessageSend)
		}
		if err := json.Unmarshal(request.Params, &received); err != nil {
			t.Errorf("params unmarshal failed: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		message := protocol.NewMessage(
			protocol.MessageRoleAgent,
			[]*protocol.Part{protocol.NewTextPart("ok")},
		)
		result, err := json.Marshal(protocol.NewSendMessageResponseMessage(&message))
		if err != nil {
			t.Errorf("response marshal failed: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result":  json.RawMessage(result),
		})
	}))
	defer httpServer.Close()

	remote, err := New(
		WithAgentCard(&protocolserver.AgentCard{
			Name: "remote",
			URL:  httpServer.URL,
		}),
		WithEnableStreaming(false),
	)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	events, err := remote.Run(context.Background(), &agent.Invocation{
		InvocationID: "invocation",
		Message:      model.NewUserMessage("hello"),
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	for range events {
	}

	if received.Configuration == nil || received.Configuration.HistoryLength == nil {
		t.Fatalf("configuration = %#v, want historyLength", received.Configuration)
	}
	if got := *received.Configuration.HistoryLength; got != 0 {
		t.Fatalf("historyLength = %d, want 0", got)
	}
}

func TestMessageModeServerAndAgent(t *testing.T) {
	runner := &testRunner{events: []*event.Event{
		{
			Response: &model.Response{
				ID:        "response",
				IsPartial: true,
				Choices: []model.Choice{{
					Delta: model.Message{Content: "hel"},
				}},
			},
		},
		{
			Response: &model.Response{
				ID:        "response",
				IsPartial: true,
				Choices: []model.Choice{{
					Delta: model.Message{Content: "lo"},
				}},
			},
		},
		{
			Response: &model.Response{
				Object: model.ObjectTypeRunnerCompletion,
				Done:   true,
			},
			StateDelta: map[string][]byte{"state-key": []byte(`"value"`)},
		},
	}}

	var handler http.Handler
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	card := protocolserver.AgentCard{Name: "remote", URL: httpServer.URL}
	server, err := a2aserver.New(
		a2aserver.WithRunner(runner),
		a2aserver.WithAgentCard(card),
		a2aserver.WithStreamingEventType(a2aserver.StreamingEventTypeMessage),
	)
	if err != nil {
		t.Fatalf("server New failed: %v", err)
	}
	handler = server.Handler()

	for _, test := range []struct {
		name      string
		streaming bool
		contextID string
	}{
		{name: "non-streaming", contextID: "context-unary"},
		{name: "streaming", streaming: true, contextID: "context-stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote, err := New(
				WithAgentCard(&card),
				WithEnableStreaming(test.streaming),
			)
			if err != nil {
				t.Fatalf("agent New failed: %v", err)
			}
			eventChannel, err := remote.Run(context.Background(), &agent.Invocation{
				InvocationID: "invocation",
				Message:      model.NewUserMessage("hello"),
				Session: &session.Session{
					ID:     test.contextID,
					UserID: "user",
				},
			})
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			var events []*event.Event
			for evt := range eventChannel {
				events = append(events, evt)
			}
			if test.streaming {
				assertStreamingMessageModeEvents(t, events)
				return
			}
			assertUnaryMessageModeEvent(t, events)
		})
	}
}

func assertUnaryMessageModeEvent(t *testing.T, events []*event.Event) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	evt := events[0]
	if evt.Response == nil || len(evt.Response.Choices) != 1 {
		t.Fatalf("response = %#v, want one choice", evt.Response)
	}
	if got := evt.Response.Choices[0].Message.Content; got != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
	assertMessageModeFinalMetadata(t, evt)
}

func assertStreamingMessageModeEvents(t *testing.T, events []*event.Event) {
	t.Helper()
	var (
		deltas     []string
		finalEvent *event.Event
		stateEvent *event.Event
	)
	for _, evt := range events {
		if evt == nil || evt.Response == nil {
			continue
		}
		if len(evt.StateDelta) > 0 {
			stateEvent = evt
		}
		if evt.Response.Done {
			finalEvent = evt
			continue
		}
		if len(evt.Response.Choices) > 0 {
			if content := evt.Response.Choices[0].Delta.Content; content != "" {
				deltas = append(deltas, content)
			}
		}
	}
	if len(deltas) != 2 || deltas[0] != "hel" || deltas[1] != "lo" {
		t.Fatalf("stream deltas = %#v, want [hel lo]", deltas)
	}
	if stateEvent == nil {
		t.Fatal("stream did not preserve final state delta")
	}
	if got := string(stateEvent.StateDelta["state-key"]); got != `"value"` {
		t.Fatalf("state delta = %q, want %q", got, `"value"`)
	}
	if stateEvent.Response.ID != "response" {
		t.Fatalf("metadata response ID = %q, want response", stateEvent.Response.ID)
	}
	if finalEvent == nil || len(finalEvent.Response.Choices) != 1 {
		t.Fatalf("final event = %#v, want one final choice", finalEvent)
	}
	if got := finalEvent.Response.Choices[0].Message.Content; got != "hello" {
		t.Fatalf("final content = %q, want hello", got)
	}
}

func assertMessageModeFinalMetadata(t *testing.T, evt *event.Event) {
	t.Helper()
	if got := string(evt.StateDelta["state-key"]); got != `"value"` {
		t.Fatalf("state delta = %q, want %q", got, `"value"`)
	}
	if evt.Response.ID != "response" {
		t.Fatalf("response ID = %q, want response", evt.Response.ID)
	}
	if !evt.Done || evt.IsPartial {
		t.Fatalf("event finality = (done=%v partial=%v), want final", evt.Done, evt.IsPartial)
	}
}
