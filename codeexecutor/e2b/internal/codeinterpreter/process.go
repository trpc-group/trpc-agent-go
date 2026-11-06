//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeinterpreter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess"
)

const defaultEnvdPort = 49983

// RunProcess runs a native process using the sandbox's current connection.
// It shares the sandbox transport without taking ownership of it. Finite stdin
// requires a known envd version; missing metadata is refreshed before launch.
// Remote endpoints require HTTPS. No request is routed to Code Interpreter.
// A nil sandbox or context returns an error. Process lifetime is owned by Run.
// Remote processes default to root, matching the standard Code Interpreter
// kernel's workspace ownership. Configured Authorization or req.User overrides
// that account. Credentialless loopback debug uses the server's default user.
func RunProcess(ctx context.Context, s *Sandbox, req envdprocess.Request) (envdprocess.Result, error) {
	if ctx == nil {
		return envdprocess.Result{}, errors.New("e2b: nil context")
	}
	if err := ctx.Err(); err != nil {
		return envdprocess.Result{}, err
	}
	if s == nil || s.connection == nil {
		return envdprocess.Result{}, errors.New("e2b: sandbox not initialized")
	}
	needsStdin := req.Stdin != "" && !req.KeepStdinOpen
	s.RLock()
	version := strings.TrimSpace(s.envdVersion)
	s.RUnlock()
	if needsStdin && version == "" {
		timeout := s.connection.RequestTimeout
		if timeout <= 0 {
			timeout = DefaultRequestTimeout * time.Second
		}
		metadataCtx, cancel := context.WithTimeout(ctx, timeout)
		_, err := s.GetInfo(metadataCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return envdprocess.Result{}, fmt.Errorf("e2b: discover envd stdin capability: %w", err)
		}
	}

	client, err := s.newProcessClient(needsStdin)
	if err != nil {
		return envdprocess.Result{}, fmt.Errorf("e2b: configure process client: %w", err)
	}
	return client.Run(ctx, req)
}

func (s *Sandbox) newProcessClient(needsStdin bool) (*envdprocess.Client, error) {
	s.RLock()
	port, version, domain := s.envdPort, strings.TrimSpace(s.envdVersion), s.sandboxDomain
	s.RUnlock()
	if needsStdin && version == "" {
		return nil, errors.New("e2b: finite stdin requires a known envd version >= 0.5.2")
	}
	if port == 0 {
		port = defaultEnvdPort
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("e2b: invalid envd port %d", port)
	}
	hostID := s.hostID(domain)
	if domain == "" {
		domain = s.connection.Domain
	}
	scheme := "https"
	if s.connection.Debug {
		scheme = "http"
	}
	baseURL := fmt.Sprintf("%s://%d-%s.%s", scheme, port, hostID, domain)
	if s.connection.Debug && (domain == "localhost" || net.ParseIP(domain).IsLoopback()) {
		baseURL = "http://" + net.JoinHostPort(domain, strconv.Itoa(port))
	}
	return envdprocess.NewClient(baseURL, s.connection.HTTPClient, s.processHeaders(),
		envdprocess.WithEnvdVersion(version))
}

func (s *Sandbox) processHeaders() http.Header {
	headers := make(http.Header)
	if s.connection.AccessToken != "" {
		headers.Set("X-Access-Token", s.connection.AccessToken)
	}
	if s.connection.TrafficAccessToken != "" {
		headers.Set("E2B-Traffic-Access-Token", s.connection.TrafficAccessToken)
	}
	for key, value := range s.connection.Headers {
		// Connect owns framing and deadline headers, not the management API.
		canonical := http.CanonicalHeaderKey(key)
		if canonical == "Content-Type" || canonical == "Content-Length" || strings.HasPrefix(canonical, "Connect-") {
			continue
		}
		headers.Set(key, value)
	}
	// Workspace files are still created by Code Interpreter, whose standard
	// kernel runs as root. Preserve that execution identity across the native
	// migration, including existing workspaces and 0600 staged files. Custom
	// templates can explicitly select their kernel account through Headers.
	// Debug HTTP must remain credentialless; its server selects the account.
	if headers.Get("Authorization") == "" && !s.connection.Debug {
		headers.Set("Authorization", "Basic cm9vdDo=") // root:
	}
	return headers
}
