//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewA2ASurface_Disabled(t *testing.T) {
	t.Parallel()

	surface, err := newA2ASurface(
		&stubA2AAgent{},
		&stubRunner{},
		runOptions{},
	)
	require.NoError(t, err)
	require.Nil(t, surface.Handler)
}

func TestNewA2ASurface_HostWithoutPathFails(t *testing.T) {
	t.Parallel()

	_, err := newA2ASurface(
		&stubA2AAgent{},
		&stubRunner{},
		runOptions{
			A2AEnabled: true,
			A2AHost:    "http://127.0.0.1:8080",
		},
	)
	require.ErrorContains(t, err, "non-root path")
}

func TestNewA2ASurface_CustomCardWithoutTools(t *testing.T) {
	t.Parallel()

	ag := &stubA2AAgent{
		info: agent.Info{
			Name:        "agent-weather",
			Description: "weather agent",
		},
		tools: []tool.Tool{stubTool{name: "weather_tool"}},
	}
	surface, err := newA2ASurface(
		ag,
		&stubRunner{},
		runOptions{
			A2AEnabled:     true,
			A2AHost:        "http://127.0.0.1:8080/a2a",
			A2AStreaming:   true,
			A2AName:        "openclaw-sandbox",
			A2ADescription: "sandbox agent",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "/a2a", surface.BasePath)
	require.Equal(
		t,
		"/a2a/.well-known/agent-card.json",
		surface.AgentCardPath,
	)

	card := fetchAgentCard(t, surface)
	require.Equal(t, "openclaw-sandbox", card.Name)
	require.Equal(t, "sandbox agent", card.Description)
	require.Len(t, card.Skills, 1)
	require.Equal(t, "openclaw-sandbox", card.Skills[0].Name)
}

func TestNewA2ASurface_AdvertisesTools(t *testing.T) {
	t.Parallel()

	ag := &stubA2AAgent{
		info: agent.Info{
			Name:        "agent-weather",
			Description: "weather agent",
		},
		tools: []tool.Tool{
			stubTool{name: "weather_tool"},
			stubTool{name: "forecast_tool"},
		},
	}
	surface, err := newA2ASurface(
		ag,
		&stubRunner{},
		runOptions{
			A2AEnabled:        true,
			A2AHost:           "http://127.0.0.1:8080/a2a",
			A2AStreaming:      true,
			A2AAdvertiseTools: true,
		},
	)
	require.NoError(t, err)

	card := fetchAgentCard(t, surface)
	require.Len(t, card.Skills, 3)
	require.Equal(t, "agent-weather", card.Skills[0].Name)
	require.Equal(t, "weather_tool", card.Skills[1].Name)
	require.Equal(t, "forecast_tool", card.Skills[2].Name)
}

func TestBuildRuntimeHTTPHandler_MountsGatewayAndA2A(t *testing.T) {
	t.Parallel()

	ag := &stubA2AAgent{
		info: agent.Info{
			Name:        "agent-weather",
			Description: "weather agent",
		},
	}
	surface, err := newA2ASurface(
		ag,
		&stubRunner{},
		runOptions{
			A2AEnabled:   true,
			A2AHost:      "http://127.0.0.1:8080/a2a",
			A2AStreaming: true,
		},
	)
	require.NoError(t, err)

	handler, err := buildRuntimeHTTPHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}),
		surface,
	)
	require.NoError(t, err)

	gatewayReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	gatewayRsp := httptest.NewRecorder()
	handler.ServeHTTP(gatewayRsp, gatewayReq)
	require.Equal(t, http.StatusAccepted, gatewayRsp.Code)

	cardReq := httptest.NewRequest(
		http.MethodGet,
		surface.AgentCardPath,
		nil,
	)
	cardRsp := httptest.NewRecorder()
	handler.ServeHTTP(cardRsp, cardReq)
	require.Equal(t, http.StatusOK, cardRsp.Code)
}

func TestNewRuntime_BuildsA2ASurface(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
a2a:
  enabled: true
  host: "http://127.0.0.1:8080/a2a"
`)

	rt, err := NewRuntime(context.Background(), []string{
		"-config", cfgPath,
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})

	require.NotNil(t, rt.A2A.Handler)
	require.Equal(t, "/a2a", rt.A2A.BasePath)
	require.Equal(
		t,
		"/a2a/.well-known/agent-card.json",
		rt.A2A.AgentCardPath,
	)
}

func fetchAgentCard(
	t *testing.T,
	surface A2ASurface,
) a2a.AgentCard {
	t.Helper()

	req := httptest.NewRequest(
		http.MethodGet,
		surface.AgentCardPath,
		nil,
	)
	rsp := httptest.NewRecorder()
	surface.Handler.ServeHTTP(rsp, req)
	require.Equal(t, http.StatusOK, rsp.Code)

	var card a2a.AgentCard
	require.NoError(t, json.Unmarshal(rsp.Body.Bytes(), &card))
	return card
}

type stubA2AAgent struct {
	info  agent.Info
	tools []tool.Tool
}

func (a *stubA2AAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- event.NewResponseEvent(
		invocation.InvocationID,
		a.info.Name,
		&model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "ok",
				},
			}},
			Done: true,
		},
	)
	close(ch)
	return ch, nil
}

func (a *stubA2AAgent) Tools() []tool.Tool {
	return a.tools
}

func (a *stubA2AAgent) Info() agent.Info {
	return a.info
}

func (a *stubA2AAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *stubA2AAgent) FindSubAgent(name string) agent.Agent {
	return nil
}
