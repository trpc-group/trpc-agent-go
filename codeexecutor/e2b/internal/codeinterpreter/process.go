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
	return envdprocess.NewClient(baseURL, s.connection.HTTPClient, s.processHeaders(version),
		envdprocess.WithEnvdVersion(version))
}

func (s *Sandbox) processHeaders(version string) http.Header {
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
	// E2B SDKs explicitly select user on envd versions predating template
	// default-user support (0.4.0). An operator-supplied Authorization header
	// can select another account on compatible deployments.
	if headers.Get("Authorization") == "" && legacyEnvdUser(version) {
		headers.Set("Authorization", "Basic dXNlcjo=") // user:
	}
	return headers
}

// legacyEnvdUser only selects a default header; NewClient validates the full
// version before sending any request.
func legacyEnvdUser(version string) bool {
	version = strings.TrimPrefix(version, "v")
	core, _, _ := strings.Cut(version, "+")
	core, prerelease, _ := strings.Cut(core, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return major == 0 && (minor < 4 || minor == 4 && patch == 0 && prerelease != "")
}
