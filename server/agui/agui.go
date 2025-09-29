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
	irunner "trpc.group/trpc-go/trpc-agent-go/server/agui/internal/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service/sse"
)

// DefaultNewService is the default function to create a new service.
var DefaultNewService = sse.New

// Server provides AG-UI server.
type Server struct {
	path    string
	runner  runner.Runner
	service service.Service
	handler http.Handler
}

// New creates a AG-UI server instance.
func New(runner runner.Runner, opt ...Option) (*Server, error) {
	if runner == nil {
		return nil, errors.New("agui: runner must not be nil")
	}
	opts := newOptions(opt...)
	aguiService := opts.service
	if aguiService == nil {
		aguiRunner := irunner.New(runner, opts.aguiRunnerOptions...)
		aguiService = DefaultNewService(aguiRunner, service.WithPath(opts.path))
	}
	return &Server{
		path:    opts.path,
		runner:  runner,
		service: aguiService,
		handler: aguiService.Handler(),
	}, nil
}

// Handler returns the http.Handler serving AG-UI requests.
func (s *Server) Handler() http.Handler {
	return s.handler
}
