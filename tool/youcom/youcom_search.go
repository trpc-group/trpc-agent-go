//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package youcom provides You.com Web Search API tools for AI agents.
package youcom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Default configuration constants.
const (
	// defaultBaseURL is the default endpoint for the You.com Web Search API.
	defaultBaseURL = "https://api.you.com/api/search"
	// defaultUserAgent is the default user agent for HTTP requests.
	defaultUserAgent = "trpc-agent-go-youcom/1.0"
	// defaultTimeout is the default timeout for HTTP requests.
	defaultTimeout = 30 * time.Second
	// maxResults is the maximum number of search results to return.
	maxResults = 10
	// defaultName is the default tool set name.
	defaultName = "youcom"
	// maxSnippetLength caps each returned snippet so a single result cannot
	// dominate the model context.
	maxSnippetLength = 500
)

// config holds the configuration for the You.com search tool set.
type config struct {
	apiKey     string
	baseURL    string
	userAgent  string
	httpClient *http.Client
	// timeout, when non-nil, overrides the HTTP client's request timeout
	// via shallow copy. A non-nil zero value explicitly disables the timeout.
	timeout    *time.Duration
	numResults int
	country    string
	safeSearch string
}

// Option is a functional option for configuring the You.com tool set.
type Option func(*config)

// WithAPIKey sets the You.com API key used to authenticate requests. The
// You.com Web Search API requires an API key; it can also be provided through
// the YDC_API_KEY environment variable at the call site.
func WithAPIKey(apiKey string) Option {
	return func(c *config) {
		c.apiKey = strings.TrimSpace(apiKey)
	}
}

// WithBaseURL sets the base URL for the You.com Web Search API.
func WithBaseURL(baseURL string) Option {
	return func(c *config) {
		c.baseURL = strings.TrimSpace(baseURL)
	}
}

// WithUserAgent sets the User-Agent string for requests.
func WithUserAgent(userAgent string) Option {
	return func(c *config) {
		c.userAgent = strings.TrimSpace(userAgent)
	}
}

// WithNumResults sets the default number of search results to return.
// Values outside 1..maxResults are clamped when the tool set is created.
func WithNumResults(numResults int) Option {
	return func(c *config) {
		c.numResults = clampNumResults(numResults)
	}
}

// WithCountry sets the country code used to bias search results
// (for example "US" or "DE").
func WithCountry(country string) Option {
	return func(c *config) {
		c.country = strings.TrimSpace(strings.ToUpper(country))
	}
}

// WithSafeSearch sets the safe search strictness: "strict", "moderate",
// or "off".
func WithSafeSearch(safeSearch string) Option {
	return func(c *config) {
		c.safeSearch = normalizeSafeSearch(safeSearch)
	}
}

// WithHTTPClient sets the HTTP client used to call the You.com API.
// When combined with WithTimeout, the caller's *http.Client is never
// mutated - a shallow copy is used to apply timeout overrides, preserving
// custom Transport/Proxy/Jar settings. Passing nil falls back to a default
// client with the default 30s timeout.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *config) {
		c.httpClient = httpClient
	}
}

// WithTimeout sets the HTTP request timeout. When combined with
// WithHTTPClient, the custom client's Transport/Proxy/Jar settings are
// preserved via shallow copy - the caller's original *http.Client is never
// mutated. Passing 0 explicitly disables the default 30s timeout (Go
// http.Client treats Timeout==0 as "no timeout"). Negative values are
// ignored.
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) {
		if timeout < 0 {
			return
		}
		c.timeout = &timeout
	}
}

// resolveHTTPClient builds the final *http.Client from config, applying
// nil fallback and timeout override via shallow copy - the caller's
// original client is never mutated.
func resolveHTTPClient(cfg *config) *http.Client {
	httpClient := cfg.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if cfg.timeout != nil {
		cloned := *httpClient
		cloned.Timeout = *cfg.timeout
		httpClient = &cloned
	}
	return httpClient
}

func clampNumResults(n int) int {
	if n <= 0 {
		return maxResults
	}
	if n > maxResults {
		return maxResults
	}
	return n
}

func normalizeSafeSearch(safeSearch string) string {
	switch strings.ToLower(strings.TrimSpace(safeSearch)) {
	case "strict":
		return "strict"
	case "moderate":
		return "moderate"
	case "off":
		return "off"
	default:
		return ""
	}
}

// ToolSet implements the ToolSet interface for You.com search.
type ToolSet struct {
	tools []tool.Tool
}

// Tools implements the ToolSet interface.
func (y *ToolSet) Tools(_ context.Context) []tool.Tool {
	return y.tools
}

// Name implements the ToolSet interface.
func (y *ToolSet) Name() string {
	return defaultName
}

// Close implements the ToolSet interface.
func (y *ToolSet) Close() error {
	// No resources to clean up for You.com tools.
	return nil
}

// NewToolSet creates a new You.com search tool set with the given options.
// It returns an error when no API key is configured, so callers cannot
// silently build a tool set that fails on every request.
func NewToolSet(opts ...Option) (*ToolSet, error) {
	cfg := &config{
		baseURL:    defaultBaseURL,
		userAgent:  defaultUserAgent,
		numResults: maxResults,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.baseURL == "" {
		cfg.baseURL = defaultBaseURL
	}
	if cfg.numResults <= 0 {
		cfg.numResults = maxResults
	}
	if cfg.apiKey == "" {
		return nil, fmt.Errorf("youcom: api key is required")
	}

	searcher := &youcomSearcher{
		cfg:        cfg,
		httpClient: resolveHTTPClient(cfg),
	}

	return &ToolSet{
		tools: []tool.Tool{function.NewFunctionTool(
			searcher.search,
			function.WithName("search"),
			function.WithDescription(fmt.Sprintf(
				"🌐 YOU.COM WEB SEARCH - Search the live web via the You.com Search API "+
					"and return current results with URLs, titles, and snippets. "+
					"Use when: you need up-to-date information beyond your knowledge cutoff, "+
					"current events, recent releases or docs, or any question where a live "+
					"web source should be cited. Default limit: %d results. "+
					"Follow up with a web fetch tool to read a selected result in full.",
				cfg.numResults)),
		)},
	}, nil
}

// youcomSearcher performs requests against the You.com Web Search API.
type youcomSearcher struct {
	cfg        *config
	httpClient *http.Client
}

// searchRequest represents the input for the You.com search tool.
type searchRequest struct {
	Query      string `json:"query" jsonschema:"description=The search query to execute on You.com"`
	NumResults int    `json:"num_results,omitempty" jsonschema:"description=Maximum number of results to return (default: 10, max: 10)"`
	Country    string `json:"country,omitempty" jsonschema:"description=Country code used to bias results (e.g. US, DE)"`
	SafeSearch string `json:"safe_search,omitempty" jsonschema:"description=Safe search strictness: strict, moderate, or off"`
}

// searchResponse represents the output from the You.com search tool.
type searchResponse struct {
	Query      string       `json:"query"`                 // Query is the original query string
	Results    []resultItem `json:"results"`               // Results is the list of search results
	SearchTime string       `json:"search_time,omitempty"` // SearchTime is the time taken for the search
	Error      string       `json:"error,omitempty"`       // Error is a human-readable error description
}

// resultItem represents a single You.com search result.
type resultItem struct {
	URL          string   `json:"url"`                     // URL is the address of the result
	Title        string   `json:"title"`                   // Title is the title of the result
	Snippets     []string `json:"snippets,omitempty"`      // Snippets are text excerpts from the result
	ThumbnailURL string   `json:"thumbnail_url,omitempty"` // ThumbnailURL is a preview image, when available
}

// youcomAPIResult mirrors a single result in the You.com Web Search API
// response. Unknown fields are ignored so new upstream fields do not break
// decoding.
type youcomAPIResult struct {
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Snippets     []string `json:"snippets"`
	ThumbnailURL string   `json:"thumbnail_url"`
}

type youcomAPIResponse struct {
	Results []youcomAPIResult `json:"results"`
}

// search executes a search query on You.com and returns the results.
func (s *youcomSearcher) search(ctx context.Context, req searchRequest) (searchResponse, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Error:   "empty search query provided",
		}, fmt.Errorf("empty search query provided")
	}

	numResults := s.cfg.numResults
	if req.NumResults > 0 {
		numResults = clampNumResults(req.NumResults)
	}
	country := s.cfg.country
	if c := strings.TrimSpace(strings.ToUpper(req.Country)); c != "" {
		country = c
	}
	safeSearch := s.cfg.safeSearch
	if ss := normalizeSafeSearch(req.SafeSearch); ss != "" {
		safeSearch = ss
	}

	startTime := time.Now()
	results, err := s.query(ctx, query, numResults, country, safeSearch)
	searchDuration := time.Since(startTime)

	if err != nil {
		return searchResponse{
			Query:   query,
			Results: []resultItem{},
			Error:   err.Error(),
		}, fmt.Errorf("youcom search failed: %w", err)
	}

	return searchResponse{
		Query:      query,
		Results:    results,
		SearchTime: fmt.Sprintf("%.2fms", float64(searchDuration.Microseconds())/1000.0),
	}, nil
}

// query calls the You.com Web Search API and maps the response to result
// items.
func (s *youcomSearcher) query(
	ctx context.Context,
	query string,
	numResults int,
	country string,
	safeSearch string,
) ([]resultItem, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("num_results", fmt.Sprintf("%d", numResults))
	if country != "" {
		params.Set("country", country)
	}
	if safeSearch != "" {
		params.Set("safesearch", safeSearch)
	}

	requestURL := s.cfg.baseURL
	if strings.ContainsRune(requestURL, '?') {
		requestURL += "&" + params.Encode()
	} else {
		requestURL += "?" + params.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("X-API-Key", s.cfg.apiKey)
	if s.cfg.userAgent != "" {
		httpReq.Header.Set("User-Agent", s.cfg.userAgent)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf(
			"unexpected status %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	var apiResp youcomAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]resultItem, 0, len(apiResp.Results))
	for _, r := range apiResp.Results {
		item := resultItem{
			URL:          r.URL,
			Title:        r.Title,
			Snippets:     trimSnippets(r.Snippets),
			ThumbnailURL: r.ThumbnailURL,
		}
		if item.URL == "" && item.Title == "" {
			continue
		}
		results = append(results, item)
	}
	return results, nil
}

// trimSnippets drops empty snippets and caps each snippet length.
func trimSnippets(snippets []string) []string {
	trimmed := make([]string, 0, len(snippets))
	for _, snippet := range snippets {
		snippet = strings.TrimSpace(snippet)
		if snippet == "" {
			continue
		}
		if len(snippet) > maxSnippetLength {
			snippet = snippet[:maxSnippetLength] + "…"
		}
		trimmed = append(trimmed, snippet)
	}
	if len(trimmed) == 0 {
		return nil
	}
	return trimmed
}
