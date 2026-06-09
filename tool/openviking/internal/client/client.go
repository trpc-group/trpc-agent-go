//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package client provides a thin HTTP client for the OpenViking context
// database server (https://github.com/volcengine/OpenViking). It maps the
// subset of the /api/v1 endpoints needed by the agent tools onto Go methods.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Default configuration constants.
const (
	// DefaultBaseURL is the OpenViking server endpoint used when none is set.
	DefaultBaseURL = "http://localhost:1933"
	// DefaultTimeout is the per-request timeout used when none is set.
	DefaultTimeout = 60 * time.Second
)

// Recoverable OpenViking error codes. Read-only calls are retried once when
// the server reports one of these codes (matching the official SDK behavior).
const (
	codeUnavailable      = "UNAVAILABLE"
	codeDeadlineExceeded = "DEADLINE_EXCEEDED"
)

// Config configures a Client.
type Config struct {
	// BaseURL is the OpenViking server URL, e.g. http://localhost:1933.
	BaseURL string
	// APIKey is sent as the X-API-Key header when non-empty.
	APIKey string
	// Account is sent as the X-OpenViking-Account header when non-empty.
	Account string
	// User is sent as the X-OpenViking-User header when non-empty.
	User string
	// Agent is sent as the X-OpenViking-Agent header when non-empty.
	Agent string
	// HTTPClient overrides the default *http.Client. Optional.
	HTTPClient *http.Client
}

// Client is an OpenViking HTTP API client. It is safe for concurrent use.
type Client struct {
	baseURL    string
	apiKey     string
	account    string
	user       string
	agent      string
	httpClient *http.Client
}

// New creates a Client from cfg, applying defaults for empty fields.
func New(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     cfg.APIKey,
		account:    cfg.Account,
		user:       cfg.User,
		agent:      cfg.Agent,
		httpClient: httpClient,
	}
}

// Close releases resources held by the client. It is a no-op today but lets
// callers treat the client as a closable resource.
func (c *Client) Close() error { return nil }

// Item is a single retrieval hit returned by Find/Search. Each hit carries the
// matched node's URI and its L0/L1 summary text, not the full L2 content; use
// Read to fetch full content for a chosen URI.
type Item struct {
	URI      string  `json:"uri"`
	Score    float64 `json:"score"`
	Abstract string  `json:"abstract"`
	Overview string  `json:"overview,omitempty"`
	Level    int     `json:"level"`
}

// RetrievalResult groups retrieval hits by context type.
type RetrievalResult struct {
	Memories  []Item `json:"memories"`
	Resources []Item `json:"resources"`
	Skills    []Item `json:"skills"`
}

// FindRequest is the payload for Find/Search.
type FindRequest struct {
	Query          string   `json:"query"`
	TargetURI      string   `json:"target_uri,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	ScoreThreshold *float64 `json:"score_threshold,omitempty"`
}

// Find performs semantic recall without session context (POST /search/find).
func (c *Client) Find(ctx context.Context, req FindRequest) (*RetrievalResult, error) {
	var out RetrievalResult
	if err := c.call(ctx, http.MethodPost, "/api/v1/search/find", findBody(req, false), nil, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Search performs session-aware hierarchical retrieval (POST /search/search).
func (c *Client) Search(ctx context.Context, req FindRequest) (*RetrievalResult, error) {
	var out RetrievalResult
	if err := c.call(ctx, http.MethodPost, "/api/v1/search/search", findBody(req, true), nil, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func findBody(req FindRequest, withSession bool) map[string]any {
	body := map[string]any{"query": req.Query}
	if req.TargetURI != "" {
		body["target_uri"] = req.TargetURI
	}
	if req.Limit > 0 {
		body["limit"] = req.Limit
	}
	if req.ScoreThreshold != nil {
		body["score_threshold"] = *req.ScoreThreshold
	}
	if withSession && req.SessionID != "" {
		body["session_id"] = req.SessionID
	}
	return body
}

// Read returns file content for a URI (GET /content/read). offset is the
// 0-indexed start line; limit is the line count (-1 reads to end).
func (c *Client) Read(ctx context.Context, uri string, offset, limit int) (string, error) {
	params := url.Values{}
	params.Set("uri", uri)
	params.Set("offset", strconv.Itoa(offset))
	params.Set("limit", strconv.Itoa(limit))
	return c.readString(ctx, "/api/v1/content/read", params)
}

// Abstract returns the L0 abstract for a URI (GET /content/abstract).
func (c *Client) Abstract(ctx context.Context, uri string) (string, error) {
	return c.readString(ctx, "/api/v1/content/abstract", url.Values{"uri": {uri}})
}

// Overview returns the L1 overview for a URI (GET /content/overview).
func (c *Client) Overview(ctx context.Context, uri string) (string, error) {
	return c.readString(ctx, "/api/v1/content/overview", url.Values{"uri": {uri}})
}

func (c *Client) readString(ctx context.Context, path string, params url.Values) (string, error) {
	var out string
	if err := c.call(ctx, http.MethodGet, path, nil, params, true, &out); err != nil {
		return "", err
	}
	return out, nil
}

// Ls lists directory contents under a URI (GET /fs/ls).
func (c *Client) Ls(ctx context.Context, uri string, recursive bool) (json.RawMessage, error) {
	params := url.Values{}
	params.Set("uri", uri)
	params.Set("recursive", strconv.FormatBool(recursive))
	return c.raw(ctx, http.MethodGet, "/api/v1/fs/ls", nil, params, true)
}

// Glob matches nodes by a glob pattern under a URI (POST /search/glob).
func (c *Client) Glob(ctx context.Context, pattern, uri string) (json.RawMessage, error) {
	if uri == "" {
		uri = "viking://"
	}
	return c.raw(ctx, http.MethodPost, "/api/v1/search/glob", map[string]any{
		"pattern": pattern,
		"uri":     uri,
	}, nil, true)
}

// Grep searches node content with a pattern (POST /search/grep).
func (c *Client) Grep(ctx context.Context, uri, pattern string, caseInsensitive bool, nodeLimit int) (json.RawMessage, error) {
	body := map[string]any{
		"uri":              uri,
		"pattern":          pattern,
		"case_insensitive": caseInsensitive,
	}
	if nodeLimit > 0 {
		body["node_limit"] = nodeLimit
	}
	return c.raw(ctx, http.MethodPost, "/api/v1/search/grep", body, nil, true)
}

// CreateSession creates a session, optionally with an explicit id
// (POST /sessions). It returns the decoded result (containing session_id).
func (c *Client) CreateSession(ctx context.Context, sessionID string) (json.RawMessage, error) {
	body := map[string]any{}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	return c.raw(ctx, http.MethodPost, "/api/v1/sessions", body, nil, false)
}

// AddMessage appends a message to a session
// (POST /sessions/{id}/messages).
func (c *Client) AddMessage(ctx context.Context, sessionID, role, content string) (json.RawMessage, error) {
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/messages"
	return c.raw(ctx, http.MethodPost, path, map[string]any{
		"role":    role,
		"content": content,
	}, nil, false)
}

// CommitSession archives a session and triggers memory extraction
// (POST /sessions/{id}/commit).
func (c *Client) CommitSession(ctx context.Context, sessionID string) (json.RawMessage, error) {
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/commit"
	return c.raw(ctx, http.MethodPost, path, map[string]any{}, nil, false)
}

// AddResource imports a file, directory, URL, or repository into OpenViking
// resources (POST /resources). Only remote paths/URLs are supported here;
// local file upload is intentionally out of scope for this client.
func (c *Client) AddResource(ctx context.Context, path, to, parent string, wait bool) (json.RawMessage, error) {
	body := map[string]any{
		"path": path,
		"wait": wait,
	}
	if to != "" {
		body["to"] = to
	}
	if parent != "" {
		body["parent"] = parent
	}
	return c.raw(ctx, http.MethodPost, "/api/v1/resources", body, nil, false)
}

// AddSkill registers a reusable skill (POST /skills). data is the skill
// definition payload (text or a path/URL accepted by OpenViking).
func (c *Client) AddSkill(ctx context.Context, data string, wait bool) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/skills", map[string]any{
		"data": data,
		"wait": wait,
	}, nil, false)
}

// Status returns server status (GET /system/status).
func (c *Client) Status(ctx context.Context) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/system/status", nil, nil, true)
}

// Remove deletes a URI from OpenViking (DELETE /fs). This is a destructive
// operation and should only be exposed to trusted agents.
func (c *Client) Remove(ctx context.Context, uri string, recursive bool) (json.RawMessage, error) {
	params := url.Values{}
	params.Set("uri", uri)
	params.Set("recursive", strconv.FormatBool(recursive))
	return c.raw(ctx, http.MethodDelete, "/api/v1/fs", nil, params, false)
}

// raw issues a request and returns the decoded "result" field as raw JSON.
func (c *Client) raw(
	ctx context.Context,
	method, path string,
	body any,
	params url.Values,
	readonly bool,
) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.call(ctx, method, path, body, params, readonly, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// envelope is the OpenViking response wrapper.
type envelope struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *apiError       `json:"error"`
}

// apiError represents a structured OpenViking error and implements error.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("openviking: %s: %s", e.Code, e.Message)
}

func (e *apiError) recoverable() bool {
	switch e.Code {
	case codeUnavailable, codeDeadlineExceeded:
		return true
	// Transient HTTP statuses (rate limiting and gateway errors) surface with a
	// numeric Code via the HTTP fallback in do(); retry these once too.
	case strconv.Itoa(http.StatusTooManyRequests), // 429
		strconv.Itoa(http.StatusBadGateway),         // 502
		strconv.Itoa(http.StatusServiceUnavailable), // 503
		strconv.Itoa(http.StatusGatewayTimeout):     // 504
		return true
	default:
		return false
	}
}

// call performs the HTTP request with a single retry for read-only operations
// on recoverable errors, then unmarshals the "result" field into out.
func (c *Client) call(
	ctx context.Context,
	method, path string,
	body any,
	params url.Values,
	readonly bool,
	out any,
) error {
	result, err := c.do(ctx, method, path, body, params)
	if err != nil && readonly && isRecoverable(err) {
		result, err = c.do(ctx, method, path, body, params)
	}
	if err != nil {
		return err
	}
	if out == nil || len(result) == 0 {
		return nil
	}
	if err := json.Unmarshal(result, out); err != nil {
		return fmt.Errorf("openviking: decode result: %w", err)
	}
	return nil
}

func (c *Client) do(
	ctx context.Context,
	method, path string,
	body any,
	params url.Values,
) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("openviking: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	reqURL := c.baseURL + path
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return nil, fmt.Errorf("openviking: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openviking: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openviking: read response: %w", err)
	}

	var env envelope
	var parseErr error
	if len(data) > 0 {
		parseErr = json.Unmarshal(data, &env)
	}
	// A structured envelope error carries the most specific code; prefer it.
	if parseErr == nil && env.Status == "error" && env.Error != nil {
		return nil, env.Error
	}
	// Any HTTP error surfaces as an apiError carrying the numeric status, so a
	// gateway-level 429/503 with a non-JSON body still flows through the
	// recoverable() retry check rather than becoming an opaque error.
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, &apiError{Code: strconv.Itoa(resp.StatusCode), Message: strings.TrimSpace(string(data))}
	}
	if parseErr != nil {
		return nil, fmt.Errorf("openviking: decode response: %w", parseErr)
	}
	return env.Result, nil
}

func (c *Client) setAuthHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if c.agent != "" {
		req.Header.Set("X-OpenViking-Agent", c.agent)
	}
	if c.account != "" {
		req.Header.Set("X-OpenViking-Account", c.account)
	}
	if c.user != "" {
		req.Header.Set("X-OpenViking-User", c.user)
	}
}

// isRecoverable reports whether err is a transient error worth one retry. It
// deliberately matches the documented policy: only the UNAVAILABLE /
// DEADLINE_EXCEEDED server codes and connection-level transport failures are
// retried. Caller cancellation/deadline and deterministic local failures
// (request build, JSON encode/decode) are never retried, so cancellation
// propagates and non-transient bugs are not masked.
func isRecoverable(err error) bool {
	// Respect caller-driven cancellation and deadlines: do not retry.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Structured server errors: retry only the transient codes.
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.recoverable()
	}
	// Connection-level transport failures (refused, reset, timeout) surface as
	// net.Error (including via *url.Error); retry those once.
	var netErr net.Error
	return errors.As(err, &netErr)
}
