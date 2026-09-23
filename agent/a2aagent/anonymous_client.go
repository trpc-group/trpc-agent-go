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
	"net/url"
	"sync"

	"trpc.group/trpc-go/trpc-a2a-go/client"
)

// NewAnonymousA2AClient creates an A2A client for anonymous cookie-based
// sessions.
//
// The client installs a cookie jar when the configured HTTP client does not
// have one and serializes requests that race before anonymous cookie
// initialization is confirmed. The serialization guarantee is limited to this
// client instance. Callers using multiple clients or processes must coordinate
// those clients separately.
//
// Initialization is confirmed only after the client observes a valid anonymous
// cookie from a response or custom handler and the configured jar accepts the
// same value for the agent URL. A preloaded cookie is sent but does not by
// itself release the gate; custom servers must return an accepted anonymous
// cookie with Set-Cookie. The built-in A2A server already does this.
func NewAnonymousA2AClient(agentURL string, opts ...client.Option) (*client.A2AClient, error) {
	clientOpts := make([]client.Option, 0, len(opts)+1)
	// Register the gate first so it remains the outermost middleware even when
	// callers add their own middleware through opts.
	clientOpts = append(clientOpts, client.WithMiddleware(newAnonymousA2AClientInitMiddleware()))
	clientOpts = append(clientOpts, opts...)
	return client.NewA2AClient(agentURL, clientOpts...)
}

type anonymousA2AClientInitMiddleware struct {
	gate                   chan struct{}
	jarMu                  sync.Mutex
	jar                    http.CookieJar
	initializedCookieValue string
	waitHook               func()
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
	if httpClient == nil || req == nil {
		return h.next.Handle(ctx, httpClient, req)
	}
	if err := h.middleware.ensureCookieJar(httpClient.Jar); err != nil {
		return nil, err
	}
	var (
		release func()
		err     error
	)
	if h.middleware.needsInitialization(req) {
		release, err = h.middleware.acquire(ctx)
		if err != nil {
			return nil, err
		}
		if h.middleware.needsInitialization(req) {
			defer release()
		} else {
			release()
		}
	}

	// The first request owns the gate until the downstream handler has
	// processed its response and this middleware has stored Set-Cookie in the
	// jar. A waiter can then send through the same jar and continue under the
	// principal established by the winner.
	requestClient, err := h.middleware.clientWithCookieJar(httpClient)
	if err != nil {
		return nil, err
	}
	requestJar := newAnonymousA2AClientRequestCookieJar(requestClient.Jar)
	requestClient.Jar = requestJar
	request := req.Clone(ctx)
	resp, handleErr := h.next.Handle(ctx, requestClient, request)
	h.middleware.captureResponseCookies(req.URL, resp, requestJar)
	return resp, handleErr
}

func (m *anonymousA2AClientInitMiddleware) needsInitialization(
	req *http.Request,
) bool {
	if req == nil || req.URL == nil {
		return false
	}
	m.jarMu.Lock()
	initializedCookieValue := m.initializedCookieValue
	jar := m.jar
	m.jarMu.Unlock()
	if initializedCookieValue == "" || jar == nil {
		return true
	}
	for _, cookie := range jar.Cookies(req.URL) {
		if cookie != nil && cookie.Name == anonymousUserIDCookieName &&
			cookie.Value == initializedCookieValue {
			return false
		}
	}
	// A request outside the cookie's URL scope must not invalidate the
	// client-wide confirmation. Requests in scope still recheck the jar above.
	return true
}

func (m *anonymousA2AClientInitMiddleware) ensureCookieJar(configured http.CookieJar) error {
	m.jarMu.Lock()
	defer m.jarMu.Unlock()
	if m.jar != nil {
		return nil
	}
	if configured != nil {
		m.jar = configured
		return nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("anonymous A2A client: create cookie jar: %w", err)
	}
	m.jar = jar
	return nil
}

func (m *anonymousA2AClientInitMiddleware) clientWithCookieJar(
	httpClient *http.Client,
) (*http.Client, error) {
	if httpClient == nil {
		return nil, nil
	}
	if err := m.ensureCookieJar(httpClient.Jar); err != nil {
		return nil, err
	}
	m.jarMu.Lock()
	jar := m.jar
	m.jarMu.Unlock()
	requestClient := *httpClient
	requestClient.Jar = jar
	return &requestClient, nil
}

func (m *anonymousA2AClientInitMiddleware) captureResponseCookies(
	requestURL *url.URL,
	resp *http.Response,
	requestJar *anonymousA2AClientRequestCookieJar,
) {
	if requestURL == nil || requestJar == nil {
		return
	}
	if resp != nil {
		responseURL := requestURL
		if resp.Request != nil && resp.Request.URL != nil {
			responseURL = resp.Request.URL
		}
		responseCookies := resp.Cookies()
		if responseURL != nil && len(responseCookies) > 0 &&
			!requestJar.storedResponseCookies(responseURL, responseCookies) {
			requestJar.SetCookies(responseURL, responseCookies)
		}
	}
	m.markInitialized(requestURL, requestJar)
}

func (m *anonymousA2AClientInitMiddleware) markInitialized(
	requestURL *url.URL,
	requestJar *anonymousA2AClientRequestCookieJar,
) {
	initializedCookieValue := requestJar.acceptedAnonymousCookieValue(requestURL)
	if initializedCookieValue == "" {
		return
	}
	m.jarMu.Lock()
	m.initializedCookieValue = initializedCookieValue
	m.jarMu.Unlock()
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

// anonymousA2AClientRequestCookieJar delegates to the configured jar and
// records cookies presented through SetCookies during one request.
type anonymousA2AClientRequestCookieJar struct {
	base          http.CookieJar
	observedJar   http.CookieJar
	mu            sync.Mutex
	storedCookies map[string]map[string]struct{}
}

func newAnonymousA2AClientRequestCookieJar(
	base http.CookieJar,
) *anonymousA2AClientRequestCookieJar {
	observedJar, err := cookiejar.New(nil)
	if err != nil {
		observedJar = nil
	}
	return &anonymousA2AClientRequestCookieJar{
		base:          base,
		observedJar:   observedJar,
		storedCookies: make(map[string]map[string]struct{}),
	}
}

func (j *anonymousA2AClientRequestCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if j == nil || j.base == nil || u == nil {
		return nil
	}
	return j.base.Cookies(u)
}

func (j *anonymousA2AClientRequestCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if j == nil || j.base == nil || u == nil {
		return
	}
	j.base.SetCookies(u, cookies)
	if len(cookies) == 0 {
		return
	}
	if j.observedJar != nil {
		j.observedJar.SetCookies(u, cookies)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	key := cookieJarURLKey(u)
	stored := j.storedCookies[key]
	if stored == nil {
		stored = make(map[string]struct{}, len(cookies))
		j.storedCookies[key] = stored
	}
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		stored[cookie.String()] = struct{}{}
	}
}

func (j *anonymousA2AClientRequestCookieJar) acceptedAnonymousCookieValue(u *url.URL) string {
	if j == nil || j.base == nil || j.observedJar == nil || u == nil {
		return ""
	}
	observedValues := make(map[string]struct{})
	for _, cookie := range j.observedJar.Cookies(u) {
		if cookie != nil && cookie.Name == anonymousUserIDCookieName &&
			isAnonymousUserIDCookieValue(cookie.Value) {
			observedValues[cookie.Value] = struct{}{}
		}
	}
	if len(observedValues) == 0 {
		return ""
	}
	for _, cookie := range j.base.Cookies(u) {
		if cookie == nil || cookie.Name != anonymousUserIDCookieName {
			continue
		}
		if _, ok := observedValues[cookie.Value]; ok {
			return cookie.Value
		}
	}
	return ""
}

func (j *anonymousA2AClientRequestCookieJar) storedResponseCookies(
	u *url.URL,
	cookies []*http.Cookie,
) bool {
	if j == nil || u == nil || len(cookies) == 0 {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	stored := j.storedCookies[cookieJarURLKey(u)]
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		if _, ok := stored[cookie.String()]; !ok {
			return false
		}
	}
	return true
}

func cookieJarURLKey(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

var _ client.Middleware = (*anonymousA2AClientInitMiddleware)(nil)
var _ http.CookieJar = (*anonymousA2AClientRequestCookieJar)(nil)
