//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"sync"

	"trpc.group/trpc-go/trpc-a2a-go/client"
)

// NewAnonymousA2AClient creates an A2A client for anonymous cookie-based
// sessions.
//
// The client installs a cookie jar when the configured HTTP client does not
// have one and serializes requests that race before a valid anonymous cookie
// is available. The serialization guarantee is limited to this client
// instance. Callers using multiple clients or processes must coordinate those
// clients separately.
func NewAnonymousA2AClient(agentURL string, opts ...client.Option) (*client.A2AClient, error) {
	clientOpts := make([]client.Option, 0, len(opts)+1)
	// Register the gate first so it remains the outermost middleware even when
	// callers add their own middleware through opts.
	clientOpts = append(clientOpts, client.WithMiddleware(newAnonymousA2AClientInitMiddleware()))
	clientOpts = append(clientOpts, opts...)
	return client.NewA2AClient(agentURL, clientOpts...)
}

type anonymousA2AClientInitMiddleware struct {
	gate     chan struct{}
	jarMu    sync.Mutex
	waitHook func()
}

func newAnonymousA2AClientInitMiddleware() *anonymousA2AClientInitMiddleware {
	return &anonymousA2AClientInitMiddleware{
		gate: make(chan struct{}, 1),
	}
}

func (m *anonymousA2AClientInitMiddleware) Wrap(next client.HTTPReqHandler) client.HTTPReqHandler {
	return &anonymousA2AClientInitHandler{
		middleware: m,
		next:       next,
	}
}

type anonymousA2AClientInitHandler struct {
	middleware *anonymousA2AClientInitMiddleware
	next       client.HTTPReqHandler
}

func (h *anonymousA2AClientInitHandler) Handle(
	ctx context.Context,
	httpClient *http.Client,
	req *http.Request,
) (*http.Response, error) {
	if h == nil || h.middleware == nil {
		return nil, errors.New("anonymous A2A client: initialization middleware is nil")
	}
	if h.next == nil {
		return nil, errors.New("anonymous A2A client: next HTTP request handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !h.middleware.needsInitialization(httpClient, req) {
		return h.next.Handle(ctx, httpClient, req)
	}

	release, err := h.middleware.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	if err := h.middleware.ensureCookieJar(httpClient); err != nil {
		return nil, err
	}
	// The first request owns the gate until the HTTP client has processed its
	// response and stored Set-Cookie in the jar. A waiter can then send through
	// the same jar and continue under the principal established by the winner.
	return h.next.Handle(ctx, httpClient, req)
}

func (m *anonymousA2AClientInitMiddleware) needsInitialization(
	httpClient *http.Client,
	req *http.Request,
) bool {
	m.jarMu.Lock()
	defer m.jarMu.Unlock()
	return anonymousA2AClientNeedsInitialization(httpClient, req)
}

func (m *anonymousA2AClientInitMiddleware) ensureCookieJar(
	httpClient *http.Client,
) error {
	m.jarMu.Lock()
	defer m.jarMu.Unlock()
	if httpClient.Jar != nil {
		return nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("anonymous A2A client: create cookie jar: %w", err)
	}
	httpClient.Jar = jar
	return nil
}

func (m *anonymousA2AClientInitMiddleware) acquire(ctx context.Context) (func(), error) {
	select {
	case m.gate <- struct{}{}:
		return func() { <-m.gate }, nil
	default:
		if m.waitHook != nil {
			m.waitHook()
		}
	}
	select {
	case m.gate <- struct{}{}:
		return func() { <-m.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func anonymousA2AClientNeedsInitialization(httpClient *http.Client, req *http.Request) bool {
	if httpClient == nil || req == nil || req.URL == nil {
		return false
	}
	if httpClient.Jar == nil {
		return true
	}
	for _, cookie := range httpClient.Jar.Cookies(req.URL) {
		if cookie != nil && cookie.Name == anonymousUserIDCookieName && isAnonymousUserIDCookieValue(cookie.Value) {
			return false
		}
	}
	return true
}

var _ client.Middleware = (*anonymousA2AClientInitMiddleware)(nil)
