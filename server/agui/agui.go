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
	"errors"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/runner"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Server provides AG-UI server.
type Server struct {
	appName        string          // appName is required when history snapshots are enabled.
	path           string          // path is the primary AG-UI chat endpoint.
	handler        http.Handler    // handler serves chat and optional history routes.
	sessionService session.Service // sessionService backs stored conversations for snapshots.
}

// New creates a AG-UI server instance.
func New(runner runner.Runner, opt ...Option) (*Server, error) {
	if runner == nil {
		return nil, errors.New("agui: runner must not be nil")
	}
	opts := newOptions(opt...)
	if opts.serviceFactory == nil {
		return nil, errors.New("agui: serviceFactory must not be nil")
	}
	aguiRunner := aguirunner.New(runner, opts.aguiRunnerOptions...)
	serviceOpts := []service.Option{service.WithPath(opts.path)}
	if opts.messagesSnapshotEnabled {
		if opts.appName == "" {
			return nil, errors.New("agui: app name is required when messages snapshot is enabled")
		}
		if opts.sessionService == nil {
			return nil, errors.New("agui: session service is required when messages snapshot is enabled")
		}
		serviceOpts = append(
			serviceOpts,
			service.WithMessagesSnapshotEnabled(true),
			service.WithMessagesSnapshotPath(opts.messagesSnapshotPath),
		)
	}
	aguiService := opts.serviceFactory(aguiRunner, serviceOpts...)
	return &Server{
		appName:        opts.appName,
		path:           opts.path,
		sessionService: opts.sessionService,
		handler:        aguiService.Handler(),
	}, nil
}

// Handler returns the http.Handler serving AG-UI requests.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Path returns the route path for HTTP.
func (s *Server) Path() string {
	return s.path
}
