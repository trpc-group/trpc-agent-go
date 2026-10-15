//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package envdfs accesses files through the native envd HTTP and RPC services.
package envdfs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdfs/spec/filesystemconnect"
)

// Client accesses one sandbox's native filesystem. It is safe for concurrent
// use, but callers must synchronize operations on the same remote paths.
// Operations never retry mutations or fall back to Code Interpreter.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	rpc        filesystemconnect.FilesystemClient
	headers    http.Header
	username   string
}

// NewClient constructs a client without contacting envd. It copies headers and
// the HTTP client configuration, borrowing its transport without owning it.
// Remote endpoints require HTTPS; credentialless loopback HTTP is allowed for
// debugging. Requests, including redirects, cannot leave the configured origin.
// Basic Authorization selects the same user for RPC and HTTP file requests;
// otherwise envd selects its default user. Framing headers belong to the client.
// The caller supplies operation deadlines through contexts or HTTPClient.Timeout.
// Unsupported endpoints return errors at invocation, without a silent fallback.
func NewClient(baseURL string, httpClient *http.Client, headers http.Header) (*Client, error) {
	u, err := parseOrigin(baseURL, len(headers) != 0)
	if err != nil {
		return nil, err
	}
	snapshot, username, err := snapshotHeaders(headers)
	if err != nil {
		return nil, err
	}
	copied := newHTTPClient(httpClient, u)
	u.Path = ""
	c := &Client{baseURL: u, httpClient: copied, headers: snapshot, username: username}
	c.rpc = filesystemconnect.NewFilesystemClient(c.httpClient, u.String())
	return c, nil
}

func parseOrigin(baseURL string, hasHeaders bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("envd filesystem: base URL must be an HTTP origin without credentials, path, query, or fragment")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("envd filesystem: unsupported base URL scheme")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if !strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("envd filesystem: remote base URL must use HTTPS")
		}
		if hasHeaders {
			return nil, errors.New("envd filesystem: configured headers require HTTPS")
		}
	}
	return u, nil
}

func snapshotHeaders(headers http.Header) (http.Header, string, error) {
	snapshot := make(http.Header)
	for key, values := range headers {
		key = http.CanonicalHeaderKey(key)
		if key == "Content-Type" || key == "Content-Length" || key == "Range" || key == "Accept-Encoding" || strings.HasPrefix(key, "Connect-") {
			continue
		}
		snapshot[key] = append([]string(nil), values...)
	}
	username := ""
	if auth := strings.Fields(snapshot.Get("Authorization")); len(auth) > 0 && strings.EqualFold(auth[0], "Basic") {
		var ok bool
		username, _, ok = (&http.Request{Header: snapshot}).BasicAuth()
		if !ok || username == "" || strings.ContainsRune(username, '\x00') {
			return nil, "", errors.New("envd filesystem: invalid Basic authorization user")
		}
	}
	return snapshot, username, nil
}

func newHTTPClient(httpClient *http.Client, u *url.URL) *http.Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	copied := *httpClient
	redirect := copied.CheckRedirect
	copied.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if redirect != nil {
			if err := redirect(req, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return errors.New("envd filesystem: too many redirects")
		}
		if len(via) > 0 && via[0].Method != http.MethodGet {
			return errors.New("envd filesystem: refusing redirect of a mutation")
		}
		return nil
	}
	transport := copied.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	copied.Transport = &originTransport{base: transport, origin: origin(u)}
	return &copied
}

type originTransport struct {
	base   http.RoundTripper
	origin string
}

func (t *originTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r == nil || r.URL == nil || origin(r.URL) != t.origin {
		return nil, errors.New("envd filesystem: refusing request outside configured origin")
	}
	return t.base.RoundTrip(r)
}
func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}
func (c *Client) validate(ctx context.Context, paths ...string) error {
	if ctx == nil {
		return errors.New("envd filesystem: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.rpc == nil {
		return errors.New("envd filesystem: client is not initialized")
	}
	for _, p := range paths {
		if p == "" || strings.ContainsRune(p, '\x00') {
			return errors.New("envd filesystem: empty path or NUL in path")
		}
	}
	return nil
}
func (c *Client) addHeaders(h http.Header) {
	for k, values := range c.headers {
		h[k] = append([]string(nil), values...)
	}
}
func operationError(ctx context.Context, op string, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return fmt.Errorf("envd filesystem: %s: %w", op, err)
}
