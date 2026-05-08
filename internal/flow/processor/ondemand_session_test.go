//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type onDemandSessionService struct {
	session.Service
}

func (s *onDemandSessionService) SearchEvents(
	context.Context,
	session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return nil, nil
}

func (s *onDemandSessionService) GetEventWindow(
	context.Context,
	session.EventWindowRequest,
) (*session.EventWindow, error) {
	return &session.EventWindow{}, nil
}

func TestOnDemandSessionRequestProcessor_ProcessRequest(t *testing.T) {
	p := NewOnDemandSessionRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("base instruction"),
			model.NewUserMessage("hello"),
		},
	}
	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: &onDemandSessionService{Service: sessioninmemory.NewSessionService()},
	}

	p.ProcessRequest(context.Background(), inv, req, nil)
	require.Len(t, req.Messages, 2)
	assert.Contains(t, req.Messages[0].Content, "Progressive disclosure for session history is available.")
	assert.Contains(t, req.Messages[0].Content, "base instruction")

	first := req.Messages[0].Content
	p.ProcessRequest(context.Background(), inv, req, nil)
	assert.Equal(t, first, req.Messages[0].Content)
}

func TestOnDemandSessionRequestProcessor_SkipsWithoutSupport(t *testing.T) {
	p := NewOnDemandSessionRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("hello"),
		},
	}
	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: sessioninmemory.NewSessionService(),
	}

	p.ProcessRequest(context.Background(), inv, req, nil)
	require.Len(t, req.Messages, 1)
	assert.Equal(t, model.RoleUser, req.Messages[0].Role)
}

func TestOnDemandSessionRequestProcessor_InsertsSystemMessage(t *testing.T) {
	p := NewOnDemandSessionRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("hello"),
		},
	}
	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: &onDemandSessionService{Service: sessioninmemory.NewSessionService()},
	}

	p.ProcessRequest(context.Background(), inv, req, nil)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "scope=current_session")
	assert.Equal(t, model.RoleUser, req.Messages[1].Role)
}

func TestOnDemandSessionRequestProcessor_RebuildForContextCompaction(t *testing.T) {
	p := NewOnDemandSessionRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("base instruction"),
			model.NewUserMessage("hello"),
		},
	}
	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: &onDemandSessionService{Service: sessioninmemory.NewSessionService()},
	}

	require.True(t, p.SupportsContextCompactionRebuild(inv))
	p.RebuildRequestForContextCompaction(context.Background(), inv, req)
	require.Len(t, req.Messages, 2)
	assert.Contains(t, req.Messages[0].Content, "Progressive disclosure for session history is available.")
}
