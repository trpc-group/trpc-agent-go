//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/manager"
)

const (
	headerAllow                = "Allow"
	headerContentType          = "Content-Type"
	headerAccessControlOrigin  = "Access-Control-Allow-Origin"
	headerAccessControlMethods = "Access-Control-Allow-Methods"
	headerAccessControlHeaders = "Access-Control-Allow-Headers"
	contentTypeJSON            = "application/json"
)

// Server exposes a control-plane HTTP API for a single PromptIter target app.
type Server struct {
	appName       string
	basePath      string
	structurePath string
	runsPath      string
	asyncRunsPath string
	timeout       time.Duration
	engine        engine.Engine
	manager       manager.Manager
	handler       http.Handler
}

// New creates a new PromptIter HTTP server.
func New(opts ...Option) (*Server, error) {
	options := newOptions(opts...)
	if strings.TrimSpace(options.appName) == "" {
		return nil, errors.New("promptiter server: app name must not be empty")
	}
	if options.engine == nil {
		return nil, errors.New("promptiter server: engine must not be nil")
	}
	basePath := normalizeBasePath(options.basePath)
	appBasePath, err := joinURLPath(basePath, options.appName)
	if err != nil {
		return nil, fmt.Errorf("promptiter server: join app base path: %w", err)
	}
	structurePath, err := joinURLPath(appBasePath, options.structurePath)
	if err != nil {
		return nil, fmt.Errorf("promptiter server: join structure path: %w", err)
	}
	runsPath, err := joinURLPath(appBasePath, options.runsPath)
	if err != nil {
		return nil, fmt.Errorf("promptiter server: join runs path: %w", err)
	}
	asyncRunsPath, err := joinURLPath(appBasePath, options.asyncRunsPath)
	if err != nil {
		return nil, fmt.Errorf("promptiter server: join async runs path: %w", err)
	}
	server := &Server{
		appName:       options.appName,
		basePath:      appBasePath,
		structurePath: structurePath,
		runsPath:      runsPath,
		asyncRunsPath: asyncRunsPath,
		timeout:       options.timeout,
		engine:        options.engine,
		manager:       options.manager,
	}
	server.setupHandler()
	return server, nil
}

// Handler returns the HTTP handler exposed by the PromptIter server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// BasePath returns the base path exposed by the PromptIter server.
func (s *Server) BasePath() string {
	return s.basePath
}

// StructurePath returns the structure endpoint path.
func (s *Server) StructurePath() string {
	return s.structurePath
}

// RunsPath returns the runs endpoint path.
func (s *Server) RunsPath() string {
	return s.runsPath
}

// AsyncRunsPath returns the asynchronous runs endpoint path.
func (s *Server) AsyncRunsPath() string {
	return s.asyncRunsPath
}

// Close closes the PromptIter server.
func (s *Server) Close() error {
	return nil
}

func (s *Server) setupHandler() {
	mux := http.NewServeMux()
	mux.HandleFunc(s.structurePath, s.handleStructure)
	mux.HandleFunc(s.structurePath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
	mux.HandleFunc(s.runsPath, s.handleRuns)
	mux.HandleFunc(s.runsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
	if s.manager != nil {
		mux.HandleFunc(s.asyncRunsPath, s.handleAsyncRuns)
		mux.HandleFunc(s.asyncRunsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
		mux.HandleFunc(s.asyncRunsPath+"/", s.handleRunResource)
	}
	s.handler = mux
}

func normalizeBasePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return defaultBasePath
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return strings.TrimRight(trimmed, "/")
}

func joinURLPath(basePath, child string) (string, error) {
	return url.JoinPath(basePath, child)
}
