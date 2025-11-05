//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewNilRunner(t *testing.T) {
	srv, err := New(nil)
	assert.Nil(t, srv)
	assert.EqualError(t, err, "agui: runner must not be nil")
}

func TestDefaultPath(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	srv, err := New(r)
	assert.NoError(t, err)
	assert.Equal(t, "/", srv.Path())
}

func TestEndToEndServerSendsSSEEvents(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	srv, err := New(r, WithPath("/agui"))
	assert.NoError(t, err)
	assert.Equal(t, "/agui", srv.Path())

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	payload := `{"threadId":"thread-1","runId":"run-42","messages":[{"role":"user","content":"hi there"}]}`
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/agui", strings.NewReader(payload))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	bodyStr := string(body)

	assert.Contains(t, bodyStr, `"type":"RUN_STARTED"`)
	assert.Contains(t, bodyStr, `"type":"TEXT_MESSAGE_START"`)
	assert.Contains(t, bodyStr, `"type":"TEXT_MESSAGE_CONTENT"`)
	assert.Contains(t, bodyStr, `"type":"TEXT_MESSAGE_END"`)
	assert.Contains(t, bodyStr, `"type":"RUN_FINISHED"`)

	assert.Equal(t, 1, agent.runCalls)
	assert.NotNil(t, agent.lastInvocation)
	assert.Equal(t, "hi there", agent.lastInvocation.Message.Content)
	assert.Equal(t, model.RoleUser, agent.lastInvocation.Message.Role)
}

func TestNewMessagesSnapshotRequiresAppName(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	srv, err := New(r, WithMessagesSnapshotEnabled(true), WithSessionService(inmemory.NewSessionService()))
	assert.Nil(t, srv)
	assert.Error(t, err)
}

func TestNewMessagesSnapshotRequiresSessionService(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	srv, err := New(r, WithMessagesSnapshotEnabled(true), WithAppName("demo"))
	assert.Nil(t, srv)
	assert.Error(t, err)
}

func TestNewMessagesSnapshotEnabledSuccess(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	sessionSvc := inmemory.NewSessionService()
	srv, err := New(r,
		WithPath("/agui"),
		WithMessagesSnapshotEnabled(true),
		WithMessagesSnapshotPath("/history"),
		WithAppName(agent.Info().Name),
		WithSessionService(sessionSvc),
	)
	assert.NoError(t, err)
	assert.NotNil(t, srv)
	assert.Equal(t, "/agui", srv.Path())
}

func TestPathDefault(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	srv, err := New(r)
	assert.NoError(t, err)
	assert.Equal(t, "/", srv.BasePath())
	assert.Equal(t, "/", srv.Path())
}

func TestBasePathDefault(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	srv, err := New(r, WithPath("/chat"))
	assert.NoError(t, err)
	assert.Equal(t, "/", srv.BasePath())
	assert.Equal(t, "/chat", srv.Path())
}

func TestBasePath(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	srv, err := New(r, WithBasePath("/agui"), WithPath("/chat"))
	assert.NoError(t, err)
	assert.Equal(t, "/agui", srv.BasePath())
	assert.Equal(t, "/agui/chat", srv.Path())
}

func TestInvalidChatPath(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	r := runner.NewRunner(agent.Info().Name, agent)
	srv, err := New(r, WithBasePath("\x01"), WithPath("/chat"))
	assert.Nil(t, srv)
	assert.Error(t, err)
}

type mockAgent struct {
	info           agent.Info
	runCalls       int
	lastInvocation *agent.Invocation
}

func (a *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	a.runCalls++
	a.lastInvocation = invocation
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		chunk := &model.Response{
			ID:        "msg-1",
			Object:    model.ObjectTypeChatCompletionChunk,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{Role: model.RoleAssistant, Content: "hello"},
			}},
		}
		final := &model.Response{
			ID:     "msg-1",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant},
			}},
		}
		ch <- event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, chunk)
		ch <- event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, final)
	}()
	return ch, nil
}

func (a *mockAgent) Tools() []tool.Tool {
	return nil
}

func (a *mockAgent) Info() agent.Info {
	return a.info
}

func (a *mockAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *mockAgent) FindSubAgent(string) agent.Agent {
	return nil
}
