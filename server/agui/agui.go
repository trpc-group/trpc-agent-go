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
)

// Server provides AG-UI server.
type Server struct {
	path    string
	handler http.Handler
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
	aguiService := opts.serviceFactory(aguiRunner, service.WithPath(opts.path))
	return &Server{
		path:    opts.path,
		handler: aguiService.Handler(),
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
