//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package langfuse fetches text prompts from the Langfuse prompt management
// API and maps them to [prompt.Text] with double-curly variable syntax.
package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/prompt"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse/config"
)

// ClientOption configures a [Client].
type ClientOption func(*clientConfig)

type clientConfig struct {
	httpClient *http.Client
}

const defaultHTTPTimeout = 30 * time.Second

// Client fetches text prompts from the Langfuse REST API.
type Client struct {
	baseURL    string
	publicKey  string
	secretKey  string
	httpClient *http.Client
}

// NewClient creates a Langfuse prompt client from a shared ConnectionConfig.
func NewClient(cfg config.ConnectionConfig, opts ...ClientOption) *Client {
	cc := clientConfig{
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}
	for _, opt := range opts {
		opt(&cc)
	}
	return &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		publicKey:  cfg.PublicKey,
		secretKey:  cfg.SecretKey,
		httpClient: cc.httpClient,
	}
}

// FetchOption configures a prompt fetch request.
type FetchOption func(*fetchConfig)

type fetchConfig struct {
	label   string // "" means unset
	version int    // 0 means unset
}

// WithLabel fetches the prompt version carrying the given label.
// Langfuse resolves prompts by either label or version, so this clears any
// previously selected version. The default label is "production" when neither
// label nor version is specified.
func WithLabel(label string) FetchOption {
	return func(cfg *fetchConfig) {
		cfg.label = label
		cfg.version = 0
	}
}

// WithVersion fetches a specific prompt version number.
// Langfuse resolves prompts by either version or label, so this clears any
// previously selected label.
func WithVersion(version int) FetchOption {
	return func(cfg *fetchConfig) {
		cfg.version = version
		cfg.label = ""
	}
}

// TextPromptResult holds a fetched text prompt and its associated metadata.
type TextPromptResult struct {
	Text    prompt.Text
	Config  map[string]any
	Version int
	Labels  []string
}

// FetchTextPrompt fetches a text prompt by name from the Langfuse API.
// Prompt names containing folder paths are URL-escaped as a single path segment.
// Only prompts with type "text" are accepted; chat prompts return an error.
func (c *Client) FetchTextPrompt(ctx context.Context, name string, opts ...FetchOption) (TextPromptResult, error) {
	cfg := fetchConfig{label: "production"}
	for _, opt := range opts {
		opt(&cfg)
	}

	reqURL := c.baseURL + "/api/public/v2/prompts/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return TextPromptResult{}, fmt.Errorf("langfuse: create request: %w", err)
	}
	q := req.URL.Query()
	if cfg.version > 0 {
		q.Set("version", strconv.Itoa(cfg.version))
	} else if cfg.label != "" {
		q.Set("label", cfg.label)
	}
	req.URL.RawQuery = q.Encode()
	req.SetBasicAuth(c.publicKey, c.secretKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TextPromptResult{}, fmt.Errorf("langfuse: fetch prompt %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return TextPromptResult{}, fmt.Errorf("langfuse: fetch prompt %q: HTTP %d: %s", name, resp.StatusCode, string(body))
	}

	var raw apiPromptResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return TextPromptResult{}, fmt.Errorf("langfuse: decode prompt %q: %w", name, err)
	}

	if raw.Type != "text" {
		return TextPromptResult{}, fmt.Errorf("langfuse: prompt %q has type %q, expected \"text\"", name, raw.Type)
	}

	templateStr, ok := raw.Prompt.(string)
	if !ok {
		return TextPromptResult{}, fmt.Errorf("langfuse: prompt %q: expected string template, got %T", name, raw.Prompt)
	}

	return TextPromptResult{
		Text: prompt.Text{
			Template: templateStr,
			Syntax:   prompt.SyntaxDoubleBrace,
			Meta: prompt.Meta{
				Name:    raw.Name,
				Version: strconv.Itoa(raw.Version),
			},
		},
		Config:  raw.Config,
		Version: raw.Version,
		Labels:  raw.Labels,
	}, nil
}

// apiPromptResponse mirrors the relevant fields of the Langfuse
// GET /api/public/v2/prompts/{name} response.
type apiPromptResponse struct {
	Name    string         `json:"name"`
	Version int            `json:"version"`
	Type    string         `json:"type"`
	Prompt  any            `json:"prompt"`
	Config  map[string]any `json:"config"`
	Labels  []string       `json:"labels"`
}

// --- Source factory with caching ---

// SourceOption configures a cached [prompt.Source] created via [Client.TextPromptSource].
type SourceOption func(*sourceConfig)

type sourceConfig struct {
	cacheTTL time.Duration
}

// WithCacheTTL sets the time-to-live for cached prompt results.
// The default TTL is 60 seconds.
func WithCacheTTL(ttl time.Duration) SourceOption {
	return func(cfg *sourceConfig) {
		cfg.cacheTTL = ttl
	}
}

// TextPromptSource returns a [prompt.Source] that fetches the named text prompt
// with the given fetch options. Results are cached with a default TTL of 60s.
// On fetch failure, a valid cached value is returned if available. Caller
// cancellation or deadline expiry is returned directly and does not use stale
// cache.
func (c *Client) TextPromptSource(name string, opts ...FetchOption) prompt.Source {
	return c.TextPromptSourceWithOptions(name, opts)
}

// TextPromptSourceWithOptions is like [Client.TextPromptSource] but accepts
// additional [SourceOption] values to configure caching behavior.
func (c *Client) TextPromptSourceWithOptions(name string, fetchOpts []FetchOption, sourceOpts ...SourceOption) prompt.Source {
	cfg := sourceConfig{cacheTTL: 60 * time.Second}
	for _, opt := range sourceOpts {
		opt(&cfg)
	}
	return &cachedSource{
		client:   c,
		name:     name,
		fetchOpt: fetchOpts,
		ttl:      cfg.cacheTTL,
	}
}

type cachedSource struct {
	client   *Client
	name     string
	fetchOpt []FetchOption
	ttl      time.Duration

	mu        sync.RWMutex
	cached    prompt.Text
	fetchedAt time.Time
	valid     bool
}

func (s *cachedSource) FetchPrompt(ctx context.Context) (prompt.Text, error) {
	s.mu.RLock()
	if s.valid && time.Since(s.fetchedAt) < s.ttl {
		t := s.cached
		s.mu.RUnlock()
		return t, nil
	}
	s.mu.RUnlock()

	result, err := s.client.FetchTextPrompt(ctx, s.name, s.fetchOpt...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return prompt.Text{}, ctxErr
		}
		s.mu.RLock()
		if s.valid {
			t := s.cached
			s.mu.RUnlock()
			return t, nil
		}
		s.mu.RUnlock()
		return prompt.Text{}, err
	}

	s.mu.Lock()
	s.cached = result.Text
	s.fetchedAt = time.Now()
	s.valid = true
	s.mu.Unlock()

	return result.Text, nil
}
