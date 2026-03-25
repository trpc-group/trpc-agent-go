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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	protocol "trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

const (
	defaultA2AUserIDHeader = "X-User-ID"

	a2aVersionKey = "version"
)

// A2ASurface provides the HTTP handler and routes served by OpenClaw A2A.
type A2ASurface struct {
	Handler       http.Handler
	BasePath      string
	URL           string
	AgentCardPath string
	UserIDHeader  string
}

func newA2ASurface(
	ag agent.Agent,
	procRunner runner.Runner,
	opts runOptions,
) (A2ASurface, error) {
	if !opts.A2AEnabled {
		return A2ASurface{}, nil
	}
	if ag == nil {
		return A2ASurface{}, errors.New("a2a agent is nil")
	}
	if procRunner == nil {
		return A2ASurface{}, errors.New("a2a runner is nil")
	}

	normalizeA2AOptions(&opts)
	host := opts.A2AHost
	if host == "" {
		return A2ASurface{}, errors.New(
			"a2a host is required when a2a is enabled",
		)
	}

	basePath, err := extractA2ABasePath(host)
	if err != nil {
		return A2ASurface{}, err
	}

	userIDHeader := opts.A2AUserIDHeader
	if userIDHeader == "" {
		userIDHeader = defaultA2AUserIDHeader
	}

	card, err := buildOpenClawA2ACard(ag, opts, host)
	if err != nil {
		return A2ASurface{}, fmt.Errorf("build a2a agent card failed: %w", err)
	}
	srv, err := a2aserver.New(
		a2aserver.WithRunner(procRunner),
		a2aserver.WithAgentCard(card),
		a2aserver.WithUserIDHeader(userIDHeader),
	)
	if err != nil {
		return A2ASurface{}, fmt.Errorf(
			"create a2a server failed: %w",
			err,
		)
	}

	return A2ASurface{
		Handler:       srv.Handler(),
		BasePath:      basePath,
		URL:           host,
		AgentCardPath: path.Join(basePath, protocol.AgentCardPath),
		UserIDHeader:  userIDHeader,
	}, nil
}

func buildOpenClawA2ACard(
	ag agent.Agent,
	opts runOptions,
	host string,
) (a2a.AgentCard, error) {
	info := ag.Info()
	name := strings.TrimSpace(opts.A2AName)
	if name == "" {
		name = info.Name
	}
	desc := strings.TrimSpace(opts.A2ADescription)
	if desc == "" {
		desc = info.Description
	}

	var cardOpts []a2aserver.AgentCardOption
	if opts.A2AAdvertiseTools && ag != nil {
		cardOpts = append(cardOpts, a2aserver.WithCardTools(ag.Tools()...))
	}
	return a2aserver.NewAgentCard(name, desc, host, opts.A2AStreaming, cardOpts...)
}

func extractA2ABasePath(host string) (string, error) {
	parsed, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("parse a2a host failed: %w", err)
	}
	basePath := strings.TrimSpace(parsed.Path)
	basePath = strings.TrimRight(basePath, "/")
	if basePath == "" {
		return "", errors.New(
			"a2a host must include a non-root path",
		)
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	return basePath, nil
}

func buildRuntimeHTTPHandler(
	gatewayHandler http.Handler,
	surface A2ASurface,
) (http.Handler, error) {
	if gatewayHandler == nil {
		return nil, errors.New("gateway handler is nil")
	}
	if surface.Handler == nil {
		return gatewayHandler, nil
	}

	mux := http.NewServeMux()
	mux.Handle("/", gatewayHandler)
	if err := mountA2ASurface(mux, surface); err != nil {
		return nil, err
	}
	return mux, nil
}

func mountA2ASurface(
	mux *http.ServeMux,
	surface A2ASurface,
) error {
	if mux == nil {
		return errors.New("a2a mux is nil")
	}
	if surface.Handler == nil {
		return errors.New("a2a handler is nil")
	}
	basePath := strings.TrimSpace(surface.BasePath)
	if basePath == "" || basePath == "/" {
		return errors.New("a2a base path must be non-root")
	}
	mux.Handle(basePath, surface.Handler)
	mux.Handle(basePath+"/", surface.Handler)
	return nil
}

func a2aStartupLines(surface A2ASurface) []startupLogLine {
	if surface.Handler == nil {
		return nil
	}
	return []startupLogLine{
		{text: fmt.Sprintf("A2A base path: %s", surface.BasePath)},
		{text: fmt.Sprintf(
			"A2A agent card: %s",
			surface.AgentCardPath,
		)},
		{text: fmt.Sprintf("A2A URL: %s", surface.URL)},
	}
}
