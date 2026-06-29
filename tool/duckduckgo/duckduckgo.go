//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package duckduckgo provides DuckDuckGo search tools for AI agents.
package duckduckgo

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo/internal/client"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// maxResults is the maximum number of search results to return.
	maxResults = 5
	// maxTitleLength is the maximum length for extracted titles.
	maxTitleLength = 50
	// defaultBaseURL is the default base URL for DuckDuckGo Instant Answer API.
	defaultBaseURL = "https://api.duckduckgo.com"
	// defaultHTMLBaseURL is the default base URL for DuckDuckGo HTML search.
	defaultHTMLBaseURL = "https://html.duckduckgo.com/html/"
	// defaultLiteBaseURL is the default base URL for DuckDuckGo Lite search.
	defaultLiteBaseURL = "https://lite.duckduckgo.com/lite/"
	// defaultUserAgent is the default user agent for HTTP requests.
	defaultUserAgent = "trpc-agent-go-duckduckgo/1.0"
	// defaultSERPUserAgent is used for HTML/Lite SERP backends. A
	// browser-compatible UA avoids being served simplified or challenge-prone
	// variants more often than the API-oriented default UA.
	defaultSERPUserAgent = "Mozilla/5.0 (X11; Linux x86_64) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/126.0.0.0 Safari/537.36"
	// defaultTimeout is the default timeout for HTTP requests.
	defaultTimeout = 30 * time.Second
	// defaultKeepAlive is the default TCP keep-alive interval.
	defaultKeepAlive = 30 * time.Second
	// defaultMaxIdleConns is the maximum number of idle connections.
	defaultMaxIdleConns = 100
	// defaultIdleConnTimeout is the default idle connection timeout.
	defaultIdleConnTimeout = 90 * time.Second
	// defaultTLSHandshakeTimeout is the default TLS handshake timeout.
	defaultTLSHandshakeTimeout = 10 * time.Second
	// defaultExpectContinueTimeout is the default expect-continue timeout.
	defaultExpectContinueTimeout = 1 * time.Second

	backendAPI  = "api"
	backendHTML = "html"
	backendLite = "lite"
)

// Option is a functional option for configuring the DuckDuckGo tool.
type Option func(*config)

// config holds the configuration for the DuckDuckGo tool.
type config struct {
	baseURL    string
	backend    string
	userAgent  string
	httpClient *http.Client
	timeout    time.Duration
	timeoutSet bool
}

// WithBaseURL sets the base URL for the DuckDuckGo API.
func WithBaseURL(baseURL string) Option {
	return func(c *config) {
		c.baseURL = baseURL
	}
}

// WithUserAgent sets the user agent for HTTP requests.
func WithUserAgent(userAgent string) Option {
	return func(c *config) {
		c.userAgent = userAgent
	}
}

// WithBackend sets the DuckDuckGo backend. Supported values are api, html,
// and lite. The default is api.
func WithBackend(backend string) Option {
	return func(c *config) {
		c.backend = normalizeBackend(backend)
	}
}

// WithTimeout sets the HTTP timeout while preserving the default transport.
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) {
		if timeout <= 0 {
			return
		}
		c.timeout = timeout
		c.timeoutSet = true
	}
}

// WithHTTPClient sets the HTTP client to use.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *config) {
		c.httpClient = httpClient
	}
}

// searchRequest represents the input for the DuckDuckGo search tool.
type searchRequest struct {
	Query string `json:"query" jsonschema:"description=The search query to execute on DuckDuckGo"`
}

// searchResponse represents the output from the DuckDuckGo search tool.
type searchResponse struct {
	Query   string       `json:"query"`
	Results []resultItem `json:"results"`
	Summary string       `json:"summary"`
}

// resultItem represents a single search result.
type resultItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// ddgTool represents the DuckDuckGo search tool.
type ddgTool struct {
	client     *client.Client
	httpClient *http.Client
	baseURL    string
	backend    string
	userAgent  string
}

// NewTool creates a new DuckDuckGo search tool with the provided options.
func NewTool(opts ...Option) tool.CallableTool {
	// Apply default configuration.
	cfg := &config{
		backend:   backendAPI,
		userAgent: defaultUserAgent,
	}

	// Apply user-provided options.
	for _, opt := range opts {
		opt(cfg)
	}
	cfg.backend = normalizeBackend(cfg.backend)
	if strings.TrimSpace(cfg.baseURL) == "" {
		cfg.baseURL = defaultBaseURLForBackend(cfg.backend)
	}
	if isSERPBackend(cfg.backend) &&
		strings.TrimSpace(cfg.userAgent) == defaultUserAgent {
		cfg.userAgent = defaultSERPUserAgent
	}
	cfg.httpClient = configuredHTTPClient(cfg)

	// Create the client with the configured values.
	ddgClient := client.New(cfg.baseURL, cfg.userAgent, cfg.httpClient)

	searchTool := &ddgTool{
		client:     ddgClient,
		httpClient: cfg.httpClient,
		baseURL:    cfg.baseURL,
		backend:    cfg.backend,
		userAgent:  cfg.userAgent,
	}

	return function.NewFunctionTool(
		searchTool.search,
		function.WithName("duckduckgo_search"),
		function.WithDescription(duckDuckGoDescription(cfg.backend)),
	)
}

func configuredHTTPClient(cfg *config) *http.Client {
	timeout := defaultTimeout
	if cfg.timeoutSet {
		timeout = cfg.timeout
	}
	if cfg.httpClient == nil {
		return newDefaultHTTPClient(timeout)
	}
	if !cfg.timeoutSet {
		return cfg.httpClient
	}
	httpClient := *cfg.httpClient
	httpClient.Timeout = timeout
	return &httpClient
}

func normalizeBackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", "instant", "instant_answer", "instant-answer", backendAPI:
		return backendAPI
	case backendHTML:
		return backendHTML
	case backendLite:
		return backendLite
	default:
		return strings.ToLower(strings.TrimSpace(backend))
	}
}

func isSERPBackend(backend string) bool {
	return backend == backendHTML || backend == backendLite
}

func defaultBaseURLForBackend(backend string) string {
	switch backend {
	case backendHTML:
		return defaultHTMLBaseURL
	case backendLite:
		return defaultLiteBaseURL
	default:
		return defaultBaseURL
	}
}

func duckDuckGoDescription(backend string) string {
	switch backend {
	case backendHTML, backendLite:
		return "Search the web using DuckDuckGo " + backend +
			" search result pages. Returns titles, URLs, and snippets " +
			"for current web discovery; use a web fetch tool to read " +
			"selected result pages in detail."
	default:
		return "Search using DuckDuckGo's Instant Answer API for " +
			"factual, encyclopedic information. Works best for: entity " +
			"information (people, companies, places like 'Steve Jobs', " +
			"'Tesla company', 'Microsoft'), definitions ('algorithm', " +
			"'photosynthesis'), mathematical calculations ('2+2', " +
			"'convert 100 feet to meters'), and historical facts. " +
			"NOT suitable for: real-time data (current weather, live " +
			"stock prices, latest news), recent events, or " +
			"time-sensitive information. Returns structured results " +
			"with abstracts, definitions, and related topics. Falls " +
			"back to DuckDuckGo HTML/Lite result pages when the API " +
			"transport is incompatible with the current network."
	}
}

func newDefaultHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: defaultKeepAlive,
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(
				ctx context.Context,
				network string,
				address string,
			) (net.Conn, error) {
				return dialer.DialContext(ctx, network, address)
			},
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          defaultMaxIdleConns,
			IdleConnTimeout:       defaultIdleConnTimeout,
			TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
			ExpectContinueTimeout: defaultExpectContinueTimeout,
		},
	}
}

// search performs the actual search operation.
func (t *ddgTool) search(ctx context.Context, req searchRequest) (searchResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: "Error: Empty search query provided",
		}, fmt.Errorf("empty search query provided")
	}

	switch t.backend {
	case "", backendAPI:
		return t.searchAPIWithSERPFallback(ctx, req)
	case backendHTML, backendLite:
		return t.searchSERPWithFallback(ctx, req)
	default:
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: fmt.Sprintf("Error: unsupported backend %q", t.backend),
		}, fmt.Errorf("unsupported backend %q", t.backend)
	}
}

func (t *ddgTool) searchAPIWithSERPFallback(
	ctx context.Context,
	req searchRequest,
) (searchResponse, error) {
	result, err := t.searchAPI(req)
	if err == nil || ctx.Err() != nil || !shouldFallbackFromAPIError(err) {
		return result, err
	}
	serpTool := *t
	if strings.TrimSpace(serpTool.userAgent) == "" ||
		serpTool.userAgent == defaultUserAgent {
		serpTool.userAgent = defaultSERPUserAgent
	}
	fallback, fallbackErr := serpTool.searchSERPWithFallbackForBackend(
		ctx,
		req,
		backendHTML,
		apiFallbackSERPBaseURL(t.baseURL),
	)
	if fallbackErr == nil {
		if strings.TrimSpace(fallback.Summary) != "" {
			fallback.Summary += " (fallback from api)"
		}
		return fallback, nil
	}
	result.Summary = fmt.Sprintf(
		"%s; fallback html failed: %v",
		result.Summary,
		fallbackErr,
	)
	return result, fmt.Errorf(
		"%w; fallback html failed: %w",
		err,
		fallbackErr,
	)
}

func (t *ddgTool) searchAPI(req searchRequest) (searchResponse, error) {
	// Perform the search.
	response, err := t.client.Search(req.Query)
	if err != nil {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: fmt.Sprintf("Error performing search: %v", err),
		}, fmt.Errorf("error performing search: %v", err)
	}

	// Convert the response to our format.
	var results []resultItem
	var summaryParts []string

	// Add instant answer if available.
	if response.Answer != "" {
		summaryParts = append(summaryParts, fmt.Sprintf("Answer: %s", response.Answer))
	}

	// Add abstract if available.
	if response.AbstractText != "" {
		summaryParts = append(summaryParts, fmt.Sprintf("Abstract: %s", response.AbstractText))
		if response.AbstractSource != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("Source: %s", response.AbstractSource))
		}
	}

	// Add definition if available.
	if response.Definition != "" {
		summaryParts = append(summaryParts, fmt.Sprintf("Definition: %s", response.Definition))
		if response.DefinitionSource != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("Definition Source: %s", response.DefinitionSource))
		}
	}

	// Process related topics as results.
	for i, topic := range response.RelatedTopics {
		if i >= maxResults {
			break
		}
		if topic.Text != "" && topic.FirstURL != "" {
			results = append(results, resultItem{
				Title:       extractTitleFromTopic(topic.Text),
				URL:         topic.FirstURL,
				Description: topic.Text,
			})
		}
	}

	// If no results from related topics, create a summary result.
	if len(results) == 0 && len(summaryParts) > 0 {
		results = append(results, resultItem{
			Title:       fmt.Sprintf("DuckDuckGo search: %s", req.Query),
			URL:         fmt.Sprintf("https://duckduckgo.com/?q=%s", strings.ReplaceAll(req.Query, " ", "+")),
			Description: strings.Join(summaryParts, " | "),
		})
	}

	summary := fmt.Sprintf("Found %d results for query '%s'", len(results), req.Query)
	if len(summaryParts) > 0 {
		summary = strings.Join(summaryParts, " | ")
	}

	return searchResponse{
		Query:   req.Query,
		Results: results,
		Summary: summary,
	}, nil
}

// extractTitleFromTopic extracts a title from a topic text.
func extractTitleFromTopic(text string) string {
	var title string

	// Split by " - " and take the first part as title.
	parts := strings.Split(text, " - ")
	if len(parts) > 0 && parts[0] != "" {
		title = strings.TrimSpace(parts[0])
	} else {
		title = strings.TrimSpace(text)
	}

	// Apply length limit.
	if len(title) > maxTitleLength {
		return title[:maxTitleLength-3] + "..."
	}

	return title
}
