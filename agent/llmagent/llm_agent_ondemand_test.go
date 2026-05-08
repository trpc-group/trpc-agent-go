//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type onDemandSessionToolService struct {
	session.Service
}

func (s *onDemandSessionToolService) SearchEvents(
	context.Context,
	session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return nil, nil
}

func (s *onDemandSessionToolService) GetEventWindow(
	context.Context,
	session.EventWindowRequest,
) (*session.EventWindow, error) {
	return &session.EventWindow{}, nil
}

func TestBuildRequestProcessors_OnDemandSessionWiring(t *testing.T) {
	opts := &Options{}
	WithEnableOnDemandSession(true)(opts)

	procs := buildRequestProcessors("tester", opts)
	var found bool
	for _, proc := range procs {
		if _, ok := proc.(*processor.OnDemandSessionRequestProcessor); ok {
			found = true
			break
		}
	}
	require.True(t, found)
}

func TestLLMAgent_OnDemandSessionTools_StaticAndInvocationAware(t *testing.T) {
	a := New("tester", WithEnableOnDemandSession(true))
	require.Nil(t, findTool(a.Tools(), "session_search"))
	require.Nil(t, findTool(a.Tools(), "session_load"))

	unsupportedInv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: sessioninmemory.NewSessionService(),
	}
	tools, userToolNames := a.InvocationToolSurface(context.Background(), unsupportedInv)
	require.Nil(t, findTool(tools, "session_search"))
	require.Nil(t, findTool(tools, "session_load"))
	require.NotNil(t, userToolNames)

	supportedInv := &agent.Invocation{
		Session: session.NewSession("app", "user", "sess"),
		SessionService: &onDemandSessionToolService{
			Service: sessioninmemory.NewSessionService(),
		},
	}
	tools, userToolNames = a.InvocationToolSurface(context.Background(), supportedInv)
	require.NotNil(t, findTool(tools, "session_search"))
	require.NotNil(t, findTool(tools, "session_load"))
	require.NotNil(t, userToolNames)
}

func TestAppendOnDemandSessionProcessor(t *testing.T) {
	requestProcessors := []flow.RequestProcessor{}

	withoutFeature := appendOnDemandSessionProcessor(&Options{}, requestProcessors)
	require.Len(t, withoutFeature, 0)

	opts := &Options{}
	WithEnableOnDemandSession(true)(opts)
	withFeature := appendOnDemandSessionProcessor(opts, requestProcessors)
	require.Len(t, withFeature, 1)
	_, ok := withFeature[0].(*processor.OnDemandSessionRequestProcessor)
	require.True(t, ok)
}

func TestLLMAgent_OnDemandSessionSkippedWithOutputSchema(t *testing.T) {
	opts := &Options{
		OutputSchema: map[string]any{"type": "object"},
	}
	WithEnableOnDemandSession(true)(opts)

	procs := appendOnDemandSessionProcessor(opts, nil)
	require.Empty(t, procs)
	require.Empty(t, appendOnDemandSessionTools(nil, opts, nil))

	a := New(
		"tester",
		WithEnableOnDemandSession(true),
		WithOutputSchema(map[string]any{"type": "object"}),
	)
	supportedInv := &agent.Invocation{
		Session: session.NewSession("app", "user", "sess"),
		SessionService: &onDemandSessionToolService{
			Service: sessioninmemory.NewSessionService(),
		},
	}
	tools, userToolNames := a.InvocationToolSurface(
		context.Background(),
		supportedInv,
	)
	require.Nil(t, findTool(tools, "session_search"))
	require.Nil(t, findTool(tools, "session_load"))
	require.NotNil(t, userToolNames)
}

func TestAppendOnDemandSessionTools_Gating(t *testing.T) {
	baseTools := []tool.Tool{}

	require.Len(
		t,
		appendOnDemandSessionTools(baseTools, nil, nil),
		0,
	)

	require.Len(
		t,
		appendOnDemandSessionTools(baseTools, &Options{}, nil),
		0,
	)

	opts := &Options{}
	WithEnableOnDemandSession(true)(opts)

	staticTools := appendOnDemandSessionTools(baseTools, opts, nil)
	require.NotNil(t, findTool(staticTools, "session_search"))
	require.NotNil(t, findTool(staticTools, "session_load"))

	unsupportedInv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: sessioninmemory.NewSessionService(),
	}
	unsupportedTools := appendOnDemandSessionTools(baseTools, opts, unsupportedInv)
	require.Nil(t, findTool(unsupportedTools, "session_search"))
	require.Nil(t, findTool(unsupportedTools, "session_load"))

	supportedInv := &agent.Invocation{
		Session: session.NewSession("app", "user", "sess"),
		SessionService: &onDemandSessionToolService{
			Service: sessioninmemory.NewSessionService(),
		},
	}
	supportedTools := appendOnDemandSessionTools(baseTools, opts, supportedInv)
	require.NotNil(t, findTool(supportedTools, "session_search"))
	require.NotNil(t, findTool(supportedTools, "session_load"))
}
