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
	"fmt"
	"net/http"
	"net/url"

	"trpc.group/trpc-go/trpc-agent-go/runner"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Server provides AG-UI server.
type Server struct {
	basePath       string          // basePath is the base path for the service.
	appName        string          // appName is required when history snapshots are enabled.
	path           string          // path is the chat message endpoint path.
	handler        http.Handler    // handler serves chat and optional history routes.
	sessionService session.Service // sessionService backs stored conversations for snapshots.
}

// New creates a AG-UI server instance.
func New(runner runner.Runner, opt ...Option) (*Server, error) {
	if runner == nil {
		return nil, errors.New("agui: runner must not be nil")
	}
	opts := newOptions(opt...)
	aguiService, err := newService(runner, opts)
	if err != nil {
		return nil, fmt.Errorf("new service: %w", err)
	}
	chatPath, err := joinURLPath(opts.basePath, opts.path)
	if err != nil {
		return nil, fmt.Errorf("agui: url join chat path: %w", err)
	}
	return &Server{
		basePath:       opts.basePath,
		appName:        opts.appName,
		path:           chatPath,
		sessionService: opts.sessionService,
		handler:        aguiService.Handler(),
	}, nil
}

// newService creates a new service instance.
func newService(runner runner.Runner, opts *options) (service.Service, error) {
	if opts.serviceFactory == nil {
		return nil, errors.New("agui: serviceFactory must not be nil")
	}
	aguiRunner := aguirunner.New(runner, opts.aguiRunnerOptions...)
	chatPath, err := joinURLPath(opts.basePath, opts.path)
	if err != nil {
		return nil, fmt.Errorf("agui: url join chat path: %w", err)
	}
	serviceOpts := []service.Option{service.WithPath(chatPath)}
	if opts.messagesSnapshotEnabled {
		if opts.appName == "" {
			return nil, errors.New("agui: app name is required when messages snapshot is enabled")
		}
		if opts.sessionService == nil {
			return nil, errors.New("agui: session service is required when messages snapshot is enabled")
		}
		if _, ok := opts.sessionService.(session.TrackService); !ok {
			return nil, errors.New("agui: session service must implement TrackService")
		}
		messagesSnapshotPath, err := joinURLPath(opts.basePath, opts.messagesSnapshotPath)
		if err != nil {
			return nil, fmt.Errorf("agui: url join messages snapshot path: %w", err)
		}
		serviceOpts = append(
			serviceOpts,
			service.WithMessagesSnapshotEnabled(true),
			service.WithMessagesSnapshotPath(messagesSnapshotPath),
		)
	}
	return opts.serviceFactory(aguiRunner, serviceOpts...), nil
}

// joinURLPath joins the base path and the path into a URL path.
func joinURLPath(basePath, path string) (string, error) {
	return url.JoinPath(basePath, path)
}

// Handler returns the http.Handler serving AG-UI requests.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Path returns the chat message endpoint path joined with BasePath.
func (s *Server) Path() string {
	return s.path
}

// BasePath returns the base URL path prefix shared by chat and history endpoints.
func (s *Server) BasePath() string {
	return s.basePath
}
