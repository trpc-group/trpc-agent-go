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
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewNilAgent(t *testing.T) {
	srv, err := New(nil)
	assert.Nil(t, srv)
	assert.EqualError(t, err, "agui: agent must not be nil")
}

func TestNewWithProvidedService(t *testing.T) {
	called := false
	original := DefaultNewService
	DefaultNewService = func(aguirunner.Runner, ...service.Option) service.Service {
		called = true
		return nil
	}
	t.Cleanup(func() { DefaultNewService = original })

	handler := http.NewServeMux()
	fakeSvc := &stubService{handler: handler}

	srv, err := New(&stubAgent{info: agent.Info{Name: "demo"}}, WithService(fakeSvc), WithPath("/custom"))
	require.NoError(t, err)
	require.NotNil(t, srv)
	assert.False(t, called)
	assert.Same(t, fakeSvc, srv.service)
	assert.Equal(t, "/custom", srv.path)
	assert.Same(t, handler, srv.Handler())
}

func TestNewCreatesDefaultService(t *testing.T) {
	original := DefaultNewService
	var capturedPath string
	fakeHandler := http.NewServeMux()
	DefaultNewService = func(_ aguirunner.Runner, opts ...service.Option) service.Service {
		var svcOpts service.Options
		for _, opt := range opts {
			opt(&svcOpts)
		}
		capturedPath = svcOpts.Path
		return &stubService{handler: fakeHandler}
	}
	t.Cleanup(func() { DefaultNewService = original })

	srv, err := New(&stubAgent{info: agent.Info{Name: "demo"}}, WithPath("/agui/custom"))
	require.NoError(t, err)
	require.NotNil(t, srv)
	assert.Equal(t, "/agui/custom", srv.path)
	assert.Equal(t, "/agui/custom", capturedPath)
	assert.Equal(t, "demo", srv.agent.Info().Name)
	assert.Same(t, fakeHandler, srv.Handler())
}

func TestEndToEndServerSendsSSEEvents(t *testing.T) {
	agent := &mockAgent{info: agent.Info{Name: "demo"}}
	srv, err := New(agent, WithPath("/agui"))
	require.NoError(t, err)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	payload := `{"threadId":"thread-1","runId":"run-42","messages":[{"role":"user","content":"hi there"}]}`
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/agui", strings.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	bodyStr := string(body)

	assert.Contains(t, bodyStr, `"type":"RUN_STARTED"`)
	assert.Contains(t, bodyStr, `"type":"TEXT_MESSAGE_START"`)
	assert.Contains(t, bodyStr, `"type":"TEXT_MESSAGE_CONTENT"`)
	assert.Contains(t, bodyStr, `"type":"TEXT_MESSAGE_END"`)
	assert.Contains(t, bodyStr, `"type":"RUN_FINISHED"`)

	assert.Equal(t, 1, agent.runCalls)
	require.NotNil(t, agent.lastInvocation)
	assert.Equal(t, "hi there", agent.lastInvocation.Message.Content)
	assert.Equal(t, model.RoleUser, agent.lastInvocation.Message.Role)
}

type stubAgent struct {
	info agent.Info
}

func (a *stubAgent) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a *stubAgent) Tools() []tool.Tool { return nil }

func (a *stubAgent) Info() agent.Info { return a.info }

func (a *stubAgent) SubAgents() []agent.Agent { return nil }

func (a *stubAgent) FindSubAgent(string) agent.Agent { return nil }

type stubService struct {
	handler http.Handler
}

func (s *stubService) Handler() http.Handler { return s.handler }

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

func (a *mockAgent) Tools() []tool.Tool { return nil }

func (a *mockAgent) Info() agent.Info { return a.info }

func (a *mockAgent) SubAgents() []agent.Agent { return nil }

func (a *mockAgent) FindSubAgent(string) agent.Agent { return nil }
