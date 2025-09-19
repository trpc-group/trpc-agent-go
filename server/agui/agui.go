//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agui provides the ability to communicate with the front end through the AG-UI protocol.
package agui

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service/sse"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// Server provides AG-UI server.
type Server struct {
	address        string
	path           string
	agent          agent.Agent
	sessionService session.Service
	service        service.Service
}

// New creates a AG-UI server instance.
func New(agent agent.Agent, opt ...Option) (*Server, error) {
	if agent == nil {
		return nil, errors.New("agui: agent must not be nil")
	}
	opts := newOptions(opt...)
	sessionService := opts.sessionService
	if sessionService == nil {
		sessionService = inmemory.NewSessionService()
	}
	service := opts.service
	if service == nil {
		runner := runner.NewRunner(
			agent.Info().Name,
			agent,
			runner.WithSessionService(sessionService),
		)
		aguiRunner := aguirunner.New(runner, opts.runnerOptions...)
		service = sse.New(aguiRunner, sse.WithAddress(opts.address), sse.WithPath(opts.path))
	}
	server := &Server{
		address:        opts.address,
		path:           opts.path,
		agent:          agent,
		sessionService: sessionService,
		service:        service,
	}
	return server, nil
}

// Serve starts the server.
func (s *Server) Serve(ctx context.Context) error {
	log.Infof("AG-UI: serving agent %q on %s%s", s.agent.Info().Name, s.address, s.path)
	return s.service.Serve(ctx)
}

// Close stops the server.
func (s *Server) Close(ctx context.Context) error {
	return s.service.Close(ctx)
}
